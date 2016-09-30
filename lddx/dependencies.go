package lddx

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
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
	Jobs            int
}

// Dependency contains information about a file and any
// dependencies that it has.
type Dependency struct {
	Name             string
	Path             string
	RealPath         *string `json:",omitempty"`
	Info             string
	Pruned           bool
	PrunedByFlatDeps bool
	NotResolved      bool
	IsWeakDep        bool
	Deps             []*Dependency
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

var depRe = regexp.MustCompile(`\s*name (.*) \(offset.*\)$`)

func IsSpecialPath(path string) bool {
	return strings.HasPrefix(path, "@")
}

func GetRealPath(dep *Dependency) string {
	if dep.RealPath != nil {
		return *dep.RealPath
	}
	return dep.Path
}

func resolvePath(path string, dep *Dependency, opts *DependencyOptions) (string, error) {
	if IsSpecialPath(path) {
		if strings.HasPrefix(path, "@executable_path/") {
			if opts.ExecutablePath == "" {
				return path, fmt.Errorf("%s: No executable path set", path)
			}
			path = opts.ExecutablePath + path[len("@executable_path"):]
		} else if strings.HasPrefix(path, "@loader_path/") {
			path = filepath.Dir(GetRealPath(dep)) + path[len("@loader_path"):]
		} else {
			return path, fmt.Errorf("%s: Unsupported", path)
		}
	}

	return ResolveAbsPath(path)
}

// pruneDep checks against the dependency graph to see if the current
// dependency meets pruning criteria, and if so, prunes the given dependency.
func pruneDep(dep *Dependency, graph *DependencyGraph, opts *DependencyOptions) *Dependency {
	path := GetRealPath(dep)
	// The toplevel dependencies may not be in FlatDeps.
	// Check for a circular dependency here.
	for _, topDep := range graph.TopDeps {
		if topDep.Path == path {
			dep.Pruned = true
			return dep
		}
	}

	for _, name := range opts.IgnoredFiles {
		if dep.Name == name {
			dep.Pruned = true
			return dep
		}
	}

	for _, prefix := range opts.IgnoredPrefixes {
		if strings.HasPrefix(path, prefix) {
			dep.Pruned = true
			return dep
		}
	}

	graph.fdLock.Lock()
	defer graph.fdLock.Unlock()

	if existingDep, processed := graph.FlatDeps[path]; !processed {
		graph.FlatDeps[path] = dep
		return dep
	} else {
		// The pruned property still needs to be set.
		dep.Pruned = true
		dep.PrunedByFlatDeps = true
		return existingDep
	}
}

func depsRead(dep *Dependency, graph *DependencyGraph, opts *DependencyOptions, limiter chan int, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	path := GetRealPath(dep)
	if IsSpecialPath(path) {
		// We cannot process this dependency any further if we don't have
		// the real path to this dependency.
		return
	}

	if limiter != nil {
		<-limiter
	}

	// Run otool to figure out the deps
	out, err := exec.Command("otool", "-l", path).Output()

	if limiter != nil {
		limiter <- 1
	}

	if err != nil {
		LogError("otool failed for %s: %s", dep.Path, err)
		dep.NotResolved = true
		return
	}

	var depsToProcess []*Dependency
	observedDeps := make(map[string]bool)
	output := strings.Split(string(out), "\n")
	for i := 0; i < len(output); i++ {
		line := strings.TrimSpace(output[i])
		isWeakDep := (line == "cmd LC_LOAD_WEAK_DYLIB")

		if !isWeakDep && line != "cmd LC_LOAD_DYLIB" {
			continue
		} else if i+5 >= len(output) {
			LogWarn("Malformed output from otool at line %d: %s", i+1, line)
			break
		}

		match := depRe.FindStringSubmatch(output[i+2])
		if match == nil {
			LogWarn("Malformed output from otool at line %d: %s", i+1, output[i+2])
			continue
		}

		depPath := strings.TrimSpace(match[1])
		resolvedPath, err := resolvePath(depPath, dep, opts)
		if err != nil {
			LogWarn("Could not resolve dependency %s for %s: %s (weak: %v)",
				depPath, dep.Path, err, isWeakDep)
			resolvedPath = depPath
		}

		// Only process any dep once.
		// I don't know why, but otool can double up dependencies.
		if !observedDeps[resolvedPath] {
			observedDeps[resolvedPath] = true
			subDep := &Dependency{
				Name:        filepath.Base(depPath),
				Path:        depPath,
				Info:        strings.TrimSpace(output[i+5]) + ", " + strings.TrimSpace(output[i+4]),
				NotResolved: err != nil,
				IsWeakDep:   isWeakDep,
			}
			if depPath != resolvedPath {
				subDep.RealPath = &resolvedPath
			}
			dep.Deps = append(dep.Deps, pruneDep(subDep, graph, opts))

			if !subDep.Pruned && !subDep.NotResolved {
				depsToProcess = append(depsToProcess, subDep)
			}
		} else {
			LogWarn("OTOOL IS BEING SHIT")
		}
		i += 5
	}

	sort.Sort(ByPath(dep.Deps))

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
	absFiles := make(map[string]bool)

	// Reduce the file list to make it unique by the absolute path
	for _, file := range files {
		file, err := ResolveAbsPath(file)

		if err != nil {
			return nil, err
		} else if isfm, err := IsFatMachO(file); err != nil || !isfm {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%s: Not a Mach-O/Universal binary", file)
		}

		if !absFiles[file] {
			dep := &Dependency{
				Name: filepath.Base(file),
				Path: file,
			}
			deps = append(deps, dep)
			absFiles[file] = true
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

// DepsCheckOToolVersion obtains the output from otool --version,
// or an error if such output could not be obtained.
func DepsCheckOToolVersion() (string, error) {
	cmd := exec.Command("otool", "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s [%s]", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
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

		for _, subDep := range dep.Deps {
			realPath := GetRealPath(subDep)
			if &subDep.Path != &realPath {
				fmt.Printf("%s%s => %s (%s)\n", strings.Repeat(" ", 4+2*depth), subDep.Name, subDep.Path, realPath)
			} else {
				fmt.Printf("%s%s => %s\n", strings.Repeat(" ", 4+2*depth), subDep.Name, subDep.Path)
			}

			if !hasPrinted[realPath] {
				hasPrinted[realPath] = true
				printer(subDep, depth+1)
			}
		}
	}
	printer(dep, 0)
}
