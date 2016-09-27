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
	Name     string
	Path     string
	RealPath *string `json:",omitempty"`
	Info     string
	Parent   *Dependency `json:"-"`
	Pruned   bool
	Deps     []*Dependency
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
	// TopDeps is a slice of top level dependencies
	TopDeps []*Dependency
	// FlatDeps is a map containing all unique dependencies referenced
	// by the top level dependencies
	FlatDeps map[string]*Dependency
	// fdLock is used to control concurrent access to FlatDeps.
	fdLock sync.RWMutex
}

var depRe = regexp.MustCompile(`(.*?)\s+\((compatibility[^)]+)\)$`)

func isSpecialPath(path string) bool {
	return strings.HasPrefix(path, "@")
}

func getRealPath(dep *Dependency) string {
	if dep.RealPath != nil {
		return *dep.RealPath
	}
	return dep.Path
}

func resolvePath(path string, dep *Dependency, opts *DependencyOptions) (string, error) {
	if isSpecialPath(path) {
		if strings.HasPrefix(path, "@executable_path/") {
			if opts.ExecutablePath == "" {
				return path, fmt.Errorf("%s: No executable path set", path)
			}
			path = opts.ExecutablePath + path[len("@executable_path"):]
		} else if strings.HasPrefix(path, "@loader_path/") {
			path = filepath.Dir(getRealPath(dep)) + path[len("@loader_path"):]
		} else {
			return path, fmt.Errorf("%s: Unsupported", path)
		}
	}

	return ResolveAbsPath(path)
}

// pruneDep checks against the dependency graph to see if the current
// dependency meets pruning criteria, and if so, prunes the given dependency.
func pruneDep(dep *Dependency, graph *DependencyGraph, opts *DependencyOptions) bool {
	path := getRealPath(dep)
	// The toplevel dependencies may not be in FlatDeps.
	// Check for a circular dependency here.
	for _, topDep := range graph.TopDeps {
		if topDep.Path == path {
			dep.Pruned = true
			return true
		}
	}

	for _, name := range opts.IgnoredFiles {
		if dep.Name == name {
			dep.Pruned = true
			return true
		}
	}

	for _, prefix := range opts.IgnoredPrefixes {
		if strings.HasPrefix(path, prefix) {
			dep.Pruned = true
			return true
		}
	}

	graph.fdLock.Lock()
	defer graph.fdLock.Unlock()

	if _, processed := graph.FlatDeps[path]; !processed {
		graph.FlatDeps[path] = dep
		return false
	}
	dep.Pruned = true
	return true
}

func depsRead(dep *Dependency, graph *DependencyGraph, opts *DependencyOptions, limiter chan int, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	if limiter != nil {
		<-limiter
		defer func() { limiter <- 1 }()
	}

	path := getRealPath(dep)
	if isSpecialPath(path) {
		// We cannot process this dependency any further if we don't have
		// the real path to this dependency.
		return
	}
	// Run otool to figure out the deps
	out, err := exec.Command("otool", "-L", path).Output()
	if err != nil {
		LogError("otool failed for %s: %s", dep.Path, err)
		return
	}

	processedDeps := make(map[string]bool)
	for _, val := range strings.Split(string(out), "\n") {
		match := depRe.FindStringSubmatch(val)
		if match != nil {
			depPath := strings.TrimSpace(match[1])
			if processedDeps[depPath] {
				// Looks like otool doubled up an entry?
				continue
			}
			processedDeps[depPath] = true

			resolvedPath, err := resolvePath(depPath, dep, opts)
			if err != nil {
				LogError("Could not resolve dependency %s for %s: %s", depPath, dep.Path, err)
			} else if !isSpecialPath(depPath) {
				depPath = resolvedPath
			}

			if depPath != dep.Path {
				subDep := &Dependency{
					Name:   filepath.Base(depPath),
					Path:   depPath,
					Info:   strings.TrimSpace(match[2]),
					Parent: dep,
				}
				if depPath != resolvedPath && err == nil {
					subDep.RealPath = &resolvedPath
				}
				pruneDep(subDep, graph, opts)
				dep.Deps = append(dep.Deps, subDep)
			}
		}
	}

	sort.Sort(ByPath(dep.Deps))

	if opts.Recursive {
		for _, subDep := range dep.Deps {
			if !subDep.Pruned {
				if wg == nil {
					depsRead(subDep, graph, opts, limiter, wg)
				} else {
					wg.Add(1)
					go depsRead(subDep, graph, opts, limiter, wg)
				}
			}
		}
	}
}

// DepsRead calculates the dependency graph for the list of files provided.
// TODO(jtanx): Allow for parsing of directories
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
	var printer func(dep *Dependency, depth int)
	printer = func(dep *Dependency, depth int) {
		if dep == nil || dep.Deps == nil {
			return
		}

		for _, subDep := range dep.Deps {
			fmt.Printf("%s%s => %s\n", strings.Repeat(" ", 4+2*depth), subDep.Name, subDep.Path)
			printer(subDep, depth+1)
		}
	}
	printer(dep, 0)
}
