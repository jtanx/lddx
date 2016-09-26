package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

type Dependency struct {
	Name   string
	Path   string
	Parent *Dependency
	Pruned bool
	Deps   []*Dependency
}

type ByPath []*Dependency

func (v ByPath) Len() int {
	return len(v)
}
func (v ByPath) Swap(i, j int) {
	v[i], v[j] = v[j], v[i]
}
func (v ByPath) Less(i, j int) bool {
	return v[i].Path < v[j].Path
}

type DependencyGraph struct {
	TopDeps  []*Dependency
	FlatDeps map[string]*Dependency
	fdLock   sync.RWMutex
}

var depRe = regexp.MustCompile(`(.*?)\s+\(compatibility[^)]+\)`)

// depsPrune checks against the dependency graph to see if the current
// dependency meets pruning criteria, and if so, prunes the given dependency.
func depsPrune(dep *Dependency, graph *DependencyGraph) bool {
	// The toplevel dependencies may not be in FlatDeps.
	// Check for a circular dependency here.
	for _, topDep := range graph.TopDeps {
		if topDep.Path == dep.Path {
			dep.Pruned = true
			return true
		}
	}

	if strings.HasPrefix(dep.Path, "/System") || strings.HasPrefix(dep.Path, "/usr/lib") {
		dep.Pruned = true
		return true
	}

	graph.fdLock.Lock()
	defer graph.fdLock.Unlock()

	if _, processed := graph.FlatDeps[dep.Path]; !processed {
		graph.FlatDeps[dep.Path] = dep
		return false
	}
	dep.Pruned = true
	return true
}

func depsRead(dep *Dependency, graph *DependencyGraph, recursive bool, limiter chan int, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	if limiter != nil {
		<-limiter
		defer func() { limiter <- 1 }()
	}

	if strings.HasPrefix(dep.Path, "@") {
		LogError("%s: Special prefix handling not implemented", dep.Path)
		return
	}

	// Run otool to figure out the deps
	cmd := exec.Command("otool", "-L", dep.Path)
	out, err := cmd.Output()
	if err != nil {
		LogError("Could not process dependencies for %s: %s", dep.Path, err)
		return
	}

	for _, val := range strings.Split(string(out), "\n") {
		match := depRe.FindStringSubmatch(val)
		if match != nil {
			depPath := strings.TrimSpace(match[1])
			depPath, err := filepath.EvalSymlinks(depPath)
			if err != nil {
				LogError("Could not evaluate dependency %s for %s", match[1], dep.Path)
				continue
			}

			if depPath != dep.Path {
				subDep := &Dependency{
					Name:   filepath.Base(depPath),
					Path:   depPath,
					Parent: dep,
				}
				depsPrune(subDep, graph)
				dep.Deps = append(dep.Deps, subDep)
			}
		}
	}

	sort.Sort(ByPath(dep.Deps))

	if recursive {
		for _, subDep := range dep.Deps {
			if !subDep.Pruned {
				if wg == nil {
					depsRead(subDep, graph, recursive, limiter, wg)
				} else {
					wg.Add(1)
					go depsRead(subDep, graph, recursive, limiter, wg)
				}
			}
		}
	}
}

func DepsCheckOToolVersion() (string, error) {
	cmd := exec.Command("otool", "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s [%s]", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func DepsRead(recursive bool, threads int, files ...string) (*DependencyGraph, error) {
	var deps []*Dependency
	absFiles := make(map[string]bool)

	// Reduce the file list to make it unique by the absolute path
	for _, file := range files {
		file, err := filepath.EvalSymlinks(file)
		if err != nil {
			return nil, err
		}

		file, err = filepath.Abs(file)
		if err != nil {
			return nil, err
		} else if isfm, err := IsFatMacho(file); err != nil || !isfm {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%s: Not a Mach-O/Universal binary!")
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

	if !recursive || threads <= 1 {
		for _, dep := range graph.TopDeps {
			depsRead(dep, graph, recursive, nil, nil)
		}
	} else {
		var wg sync.WaitGroup
		limiter := make(chan int, threads)
		for i := 0; i < threads; i++ {
			limiter <- 1
		}

		for _, dep := range graph.TopDeps {
			wg.Add(1)
			go depsRead(dep, graph, true, limiter, &wg)
		}
		wg.Wait()
	}

	return graph, nil
}

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
