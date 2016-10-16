package lddx

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DependencyOptions specifies the options to be used
// while calculating the dependency graph.
type DependencyOptions struct {
	ExecutablePath  string
	IgnoredPrefixes []string
	IgnoredFiles    []string
	Recursive       bool
	SkipWeakLibs    bool
	Jobs            int
}

// Dependency contains information about a file and any
// dependencies that it has.
type Dependency struct {
	Name             string         // The name of the library
	Path             string         // The path to the library, as specified by the load command
	RealPath         string         // The real path to the library, if available (or same as Path)
	Info             string         // Compatibility and current version info
	Pruned           bool           // Indicates if checking the dependencies of this library were skipped
	PrunedByFlatDeps bool           // Indicates if the libs were removed because they were listed in another subtree (for JSON serialisation only)
	NotResolved      bool           // Indicates if the dependencies could not be resolved (could not determine dependencies)
	IsWeakDep        bool           // Indicates if this dependency is from a weak load command
	Deps             *[]*Dependency // List of dependencies that this dependency depends on. Ugh we need these pointers because multiple Dependencies can share this.
}

// ByPath sorts a Dependency slice by the Path field
type ByPath []*Dependency

// Len returns the length of the slice
func (v ByPath) Len() int {
	return len(v)
}

// Swap swaps two values in the slice
func (v ByPath) Swap(i, j int) {
	v[i], v[j] = v[j], v[i]
}

// Less compares the Path fields of two entries in the slice
func (v ByPath) Less(i, j int) bool {
	return v[i].Path < v[j].Path
}

// DependencyGraph contains information about the dependencies
// for a collection of files.
type DependencyGraph struct {
	TopDeps  []*Dependency          // Slice of top level dependencies
	FlatDeps map[string]*Dependency // Contains all unique, non-pruned referenced dependencies
	fdLock   sync.RWMutex           // Used to control concurrent access to FlatDeps
}

func IsSpecialPath(path string) bool {
	return strings.HasPrefix(path, "@")
}

func resolvePath(path string, dep *Dependency, opts *DependencyOptions) (string, error) {
	if IsSpecialPath(path) {
		if strings.HasPrefix(path, "@executable_path/") {
			if opts.ExecutablePath == "" {
				return path, fmt.Errorf("%s: No executable path set", path)
			}
			path = opts.ExecutablePath + path[len("@executable_path"):]
		} else if strings.HasPrefix(path, "@loader_path/") {
			path = filepath.Dir(dep.RealPath) + path[len("@loader_path"):]
		} else {
			return path, fmt.Errorf("%s: Unsupported", path)
		}
	}

	return ResolveAbsPath(path)
}

func matchesIgnoredPrefixes(path string, opts *DependencyOptions) bool {
	for _, prefix := range opts.IgnoredPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// pruneDep checks against the dependency graph to see if the current
// dependency meets pruning criteria, and if so, prunes the given dependency.
func pruneDep(lib *Dylib, parent *Dependency, graph *DependencyGraph, opts *DependencyOptions) (*Dependency, bool) {
	ret := &Dependency{
		Name:     filepath.Base(lib.Path),
		Path:     lib.Path,
		RealPath: lib.Path,
		Info: fmt.Sprintf("compatibility version %d.%d.%d, current version %d.%d.%d",
			lib.CompatVersion>>16, (lib.CompatVersion>>8)&0xff, lib.CompatVersion&0xff,
			lib.CurrentVersion>>16, (lib.CurrentVersion>>8)&0xff, lib.CurrentVersion&0xff),
		IsWeakDep: lib.Weak,
	}

	// Check if we skip weak libs
	if lib.Weak && opts.SkipWeakLibs {
		ret.Pruned = true
		return ret, true
	}

	// Check if it matches an ignored file.
	for _, name := range opts.IgnoredFiles {
		if ret.Name == name {
			ret.Pruned = true
			return ret, true
		}
	}

	// The toplevel dependencies may not be in FlatDeps.
	// Check for a circular dependency here.
	for _, topDep := range graph.TopDeps {
		if topDep.Path == ret.Path || topDep.RealPath == ret.RealPath {
			// Todo: Set pruned or not???
			ret.Pruned = true
			return ret, true
		}
	}

	// We now need to get the real path to the file.
	realPath, err := resolvePath(lib.Path, parent, opts)
	if err != nil {
		LogWarn("Could not resolve dependency %s for %s: %s (weak: %v)",
			lib.Path, parent.Path, err, lib.Weak)
		ret.NotResolved = true
		return ret, true
	} else if realPath != lib.Path {
		ret.RealPath = realPath
	}

	// Check if the path matches an ignored prefix.
	if matchesIgnoredPrefixes(ret.Path, opts) || (ret.Path != ret.RealPath && matchesIgnoredPrefixes(ret.RealPath, opts)) {
		ret.Pruned = true
		return ret, true
	}

	// Now we need to check if the dep has already been processed or not.
	graph.fdLock.Lock()
	defer graph.fdLock.Unlock()

	if existingDep, processed := graph.FlatDeps[ret.RealPath]; !processed {
		graph.FlatDeps[ret.RealPath] = ret
		ret.Deps = new([]*Dependency)
		return ret, false
	} else {
		if existingDep.Path != ret.Path {
			ret.Deps = existingDep.Deps
			return ret, true
		}
		return existingDep, true
	}
}

func depsRead(dep *Dependency, graph *DependencyGraph, opts *DependencyOptions, limiter chan int, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	libs, err := ReadDylibs(dep.RealPath, limiter)
	if err != nil {
		LogError("Could not get libs for %s [%s]: %s", dep.Path, dep.RealPath, err)
		dep.NotResolved = true
		return
	}

	var depsToProcess []*Dependency
	observedDeps := make(map[string]bool)
	for _, lib := range libs {
		// Only process any dep once.
		// A dep can be seen twice if it is a fat library (contains multiple aches)
		if observedDeps[lib.Path] {
			continue
		}
		observedDeps[lib.Path] = true

		subDep, pruned := pruneDep(&lib, dep, graph, opts)
		*dep.Deps = append(*dep.Deps, subDep)
		if !pruned {
			depsToProcess = append(depsToProcess, subDep)
		}
	}

	sort.Sort(ByPath(*dep.Deps))

	if opts.Recursive {
		for _, subDep := range depsToProcess {
			if wg == nil {
				depsRead(subDep, graph, opts, limiter, wg)
			} else {
				wg.Add(1)
				go depsRead(subDep, graph, opts, limiter, wg)
			}
		}
	}
}

// DepsRead calculates the dependency graph for the list of files provided.
func DepsRead(opts DependencyOptions, files ...string) (*DependencyGraph, error) {
	var deps []*Dependency
	seenFiles := make(map[string]bool)

	// Reduce the file list to make it unique by the absolute path
	for _, file := range files {
		var info []Dylib
		absPath, err := ResolveAbsPath(file)

		if err != nil {
			return nil, err
		} else if isfm, err := IsFatMachO(file); err != nil {
			return nil, err
		} else if !isfm {
			return nil, fmt.Errorf("%s: Not a Mach-O/Universal binary", file)
		} else if info, err = GetDylibInfo(absPath); err != nil {
			return nil, err
		}

		if !seenFiles[file] {
			dep := &Dependency{
				Name:     filepath.Base(file),
				Path:     file,
				RealPath: file,
				Deps:     new([]*Dependency),
			}
			if absPath != file {
				dep.RealPath = absPath
			}
			if info != nil {
				// FIXME: We only choose the first value...
				dep.Info = fmt.Sprintf("compatibility version %d.%d.%d, current version %d.%d.%d",
					info[0].CompatVersion>>16, (info[0].CompatVersion>>8)&0xff, info[0].CompatVersion&0xff,
					info[0].CurrentVersion>>16, (info[0].CurrentVersion>>8)&0xff, info[0].CurrentVersion&0xff)
			}
			deps = append(deps, dep)
			seenFiles[file] = true
		}
	}

	if deps == nil {
		return nil, fmt.Errorf("No files specified")
	}

	graph := &DependencyGraph{
		TopDeps:  deps,
		FlatDeps: make(map[string]*Dependency),
	}

	if !opts.Recursive || opts.Jobs <= 1 {
		for _, dep := range graph.TopDeps {
			depsRead(dep, graph, &opts, nil, nil)
		}
	} else {
		var wg sync.WaitGroup
		limiter := make(chan int, opts.Jobs)
		for i := 0; i < opts.Jobs; i++ {
			limiter <- 1
		}

		for _, dep := range graph.TopDeps {
			wg.Add(1)
			go depsRead(dep, graph, &opts, limiter, &wg)
		}
		wg.Wait()
	}

	return graph, nil
}

// DepsPrettyPrint prints a dependency graph in a format similar
// to the output from ldd.
func DepsPrettyPrint(dep *Dependency) {
	hasPrinted := make(map[string]bool)
	var printer func(dep *Dependency, depth int)
	printer = func(dep *Dependency, depth int) {
		if dep == nil || dep.Deps == nil {
			return
		}

		for _, subDep := range *dep.Deps {
			if subDep.Path != subDep.RealPath {
				fmt.Printf("%s%s => %s (%s)\n", strings.Repeat(" ", 4+2*depth), subDep.Name, subDep.Path, subDep.RealPath)
			} else {
				fmt.Printf("%s%s => %s\n", strings.Repeat(" ", 4+2*depth), subDep.Name, subDep.Path)
			}

			if !hasPrinted[subDep.RealPath] {
				hasPrinted[subDep.RealPath] = true
				printer(subDep, depth+1)
			}
		}
	}
	printer(dep, 0)
}

// DepsGetJSONSerialisableVersion returns a dependency graph that's amenable to
// serialisation. The graph emitted from DepsRead reuses pointers for subtrees
// to save on computation time. However, on JSON serialisation, this causes subtrees
// to potentially be repeated over and over again. This method ensures that in a
// dependency graph, dependencies are only emitted once.
func DepsGetJSONSerialisableVersion(graph *DependencyGraph) *DependencyGraph {
	seenDeps := make(map[string]bool)
	ret := &DependencyGraph{
		TopDeps:  make([]*Dependency, 0, len(graph.TopDeps)),
		FlatDeps: make(map[string]*Dependency),
	}

	var chopDep func(dep *Dependency) *Dependency
	chopDep = func(dep *Dependency) *Dependency {
		if dep.Deps == nil {
			return dep
		}

		subDeps := make([]*Dependency, 0, len(*dep.Deps))
		changed := false
		for _, subDep := range *dep.Deps {
			if !subDep.Pruned && !subDep.NotResolved && seenDeps[subDep.RealPath] {
				changed = true
				patchedDep := *subDep
				patchedDep.Deps = nil
				patchedDep.PrunedByFlatDeps = true
				subDeps = append(subDeps, &patchedDep)
			} else {
				seenDeps[subDep.RealPath] = true
				patchedDep := chopDep(subDep)
				changed = changed || patchedDep != subDep
				subDeps = append(subDeps, patchedDep)
			}
		}
		if changed {
			patchedDep := *dep
			patchedDep.Deps = &subDeps
			return &patchedDep
		}
		return dep
	}

	for _, topDep := range graph.TopDeps {
		ret.TopDeps = append(ret.TopDeps, chopDep(topDep))
	}

	for k, v := range graph.FlatDeps {
		ret.FlatDeps[k] = chopDep(v)
	}

	return ret
}
