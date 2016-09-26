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
	Deps   map[string]*Dependency
}

type ToplevelDependency struct {
	Dependency
	FlatDeps map[string]*Dependency
	fdLock   sync.RWMutex
}

var depRe = regexp.MustCompile(`(.*?)\s+\(compatibility[^)]+\)`)

// depsCheckAndUpdateIfProcessed checks the toplevel dependency to
// see if this dependency has already been processed
func depsPrune(dep *Dependency, topdep *ToplevelDependency) bool {
	// The toplevel dependency won't be in FlatDeps.
	// Check for a circular dependency here.
	if topdep.Path == dep.Path {
		dep.Pruned = true
		return true
	}

	if strings.HasPrefix(dep.Path, "/System") || strings.HasPrefix(dep.Path, "/usr/lib") {
		dep.Pruned = true
		return true
	}

	topdep.fdLock.Lock()
	defer topdep.fdLock.Unlock()

	if _, processed := topdep.FlatDeps[dep.Path]; !processed {
		topdep.FlatDeps[dep.Path] = dep
		return false
	}
	dep.Pruned = true
	return true
}

func depsRead(dep *Dependency, topdep *ToplevelDependency, recursive bool, limiter chan int, wg *sync.WaitGroup) {
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

	var subDeps []*Dependency
	for _, val := range strings.Split(string(out), "\n") {
		match := depRe.FindStringSubmatch(val)
		if match != nil {
			depPath := strings.TrimSpace(match[1])
			if depPath != dep.Path {
				subDep := Dependency{
					Name:   filepath.Base(depPath),
					Path:   depPath,
					Parent: dep,
					Deps:   nil,
				}
				if dep.Deps == nil {
					dep.Deps = make(map[string]*Dependency)
				}
				dep.Deps[subDep.Path] = &subDep
				depsPrune(&subDep, topdep)
				subDeps = append(subDeps, &subDep)
			}
		}
	}

	if recursive && subDeps != nil {
		for _, subDep := range subDeps {
			if !subDep.Pruned {
				if wg == nil {
					depsRead(subDep, topdep, recursive, limiter, wg)
				} else {
					wg.Add(1)
					go depsRead(subDep, topdep, recursive, limiter, wg)
				}
			}
		}
	}
}

func depsPrettyPrint(dep *Dependency, depth int) {
	if dep == nil || dep.Deps == nil {
		return
	}

	keys := make([]string, 0, len(dep.Deps))
	for key := range dep.Deps {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		subDep := dep.Deps[key]
		fmt.Printf("%s%s => %s\n", strings.Repeat(" ", 4+2*depth), subDep.Name, subDep.Path)
		depsPrettyPrint(subDep, depth+1)
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

func DepsRead(file string, recursive bool, threads int) (*ToplevelDependency, error) {
	file, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}

	baseDep := &ToplevelDependency{
		Dependency: Dependency{
			Name: filepath.Base(file),
			Path: file,
		},
		FlatDeps: make(map[string]*Dependency),
	}

	if recursive {
		if threads <= 1 {
			depsRead(&baseDep.Dependency, baseDep, true, nil, nil)
		} else {
			var wg sync.WaitGroup
			limiter := make(chan int, threads)
			for i := 0; i < threads; i++ {
				limiter <- 1
			}
			wg.Add(1)
			go depsRead(&baseDep.Dependency, baseDep, true, limiter, &wg)
			wg.Wait()
		}
	} else {
		depsRead(&baseDep.Dependency, baseDep, false, nil, nil)
	}

	return baseDep, nil
}

func DepsPrettyPrint(dep *Dependency) {
	depsPrettyPrint(dep, 0)
}
