package lddx

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// CollectorOptions specifies the options for the collector
type CollectorOptions struct {
	Folder             string   // The folder to dump libraries into
	PreferredOrder     []string // If there are library conflicts, this specifies an order to choose from
	Overwrite          bool     // Whether or not to overwrite existing deps
	ModifySpecialPaths bool     // Whether or not to modify paths beginnig with @, e.g. @executable_path
	CollectFrameworks  bool     // Whether or not to also collect frameworks
	Jobs               int      // Number of concurrent jobs
}

// getNiceness determines how preferred a string is (less is better,
// unless negative - unknown)
func getNiceness(ent1, ent2 string, order []string) (int, int) {
	nice1, nice2 := -1, -1

	for i, ent := range order {
		if nice1 < 0 && strings.HasPrefix(ent1, ent) {
			nice1 = i
		}

		if nice2 < 0 && strings.HasPrefix(ent2, ent) {
			nice2 = i
		}
	}
	return nice1, nice2
}

func isFrameworkLib(file string) bool {
	return !strings.HasSuffix(file, ".dylib") && !strings.HasSuffix(file, ".so")
}

// Copies a file and makes it writeable
func copyFile(from, to string) error {
	if info, err := os.Stat(from); err != nil {
		return err
	} else if buf, err := ioutil.ReadFile(from); err != nil {
		return err
	} else if err := ioutil.WriteFile(to, buf, info.Mode()|0700); err != nil {
		return err
	}
	return nil
}

func collectorWorker(jobs <-chan *Dependency, wg *sync.WaitGroup, opts *CollectorOptions) {
	for dep := range jobs {
		LogInfo("PROCESSING %s", dep.Path)
		destination := filepath.Join(opts.Folder, dep.Name)
		if err := copyFile(dep.Path, destination); err != nil {
			LogError("Could not copy file %s: %s", dep.Path, err)
		} else {
			out, err := exec.Command("install_name_tool", "-id", "@loader_path/"+dep.Name, destination).CombinedOutput()
			if err != nil {
				LogError("Could not update identity: %s [%s]", err, out)
			}
			for _, subDep := range dep.Deps {
				if subDep.NotResolved || (subDep.Pruned && !subDep.PrunedByFlatDeps) {
					continue
				} else if !opts.CollectFrameworks && isFrameworkLib(subDep.Name) {
					continue
				} else if !opts.ModifySpecialPaths && IsSpecialPath(subDep.Path) {
					continue
				}

				out, err := exec.Command("install_name_tool", "-change", subDep.Path, "@loader_path/"+subDep.Name, destination).CombinedOutput()
				if err != nil {
					LogError("Could not rewrite dep path: %s [%s]", err, out)
				}
			}
		}
		wg.Done()
	}
}

func CollectDeps(graph *DependencyGraph, opts *CollectorOptions) error {
	// Create the output directory if it doesn't exist
	if folder, err := filepath.Abs(opts.Folder); err != nil {
		return err
	} else if err := os.MkdirAll(folder, 0755); err != nil {
		return err
	} else {
		opts.Folder = folder
	}

	// 1: Handling Framework libs
	// 2: Handling @ paths
	// 3: Handling deps that are part of the toplevel tree

	// Determine which libraries to collect/fix
	toCollect := make(map[string]*Dependency)
	for _, dep := range graph.FlatDeps {
		if !opts.Overwrite {
			if _, err := os.Stat(filepath.Join(opts.Folder, dep.Name)); err != nil {
				if !os.IsNotExist(err) {
					LogWarn("Could not stat file [skipping]: %s", err)
					continue
				}
			} else {
				continue
			}
		}

		if dep.NotResolved {
			LogWarn("Not collecting unresolved dependency %s (%s)", dep.Name, dep.Path)
			continue
		} else if !opts.CollectFrameworks && isFrameworkLib(dep.Name) {
			LogWarn("Not collecting framework dependency %s (%s)", dep.Name, dep.Path)
			continue
		} else if !opts.ModifySpecialPaths && IsSpecialPath(dep.Path) {
			LogWarn("Not collecting/modifying @dependency %s (%s)", dep.Name, dep.Path)
			continue
		}

		// Check for conflicts and resolve, if possible
		existing, ok := toCollect[dep.Name]
		if ok {
			LogWarn("Library conflict: %s -- %s, attempting resolve", existing.Path, dep.Path)
			n1, n2 := getNiceness(existing.Path, dep.Path, opts.PreferredOrder)
			if n2 >= 0 && (n1 < 0 || n2 < n1) {
				// We have a better entry, use this one instead
				LogNote("Preferred %s over %s", dep.Path, existing.Path)
				toCollect[dep.Name] = dep
			}
		} else {
			toCollect[dep.Name] = dep
		}
	}

	// Run the jobs
	if opts.Jobs <= 0 {
		opts.Jobs = 1
	}

	var wg sync.WaitGroup
	jobs := make(chan *Dependency, opts.Jobs)
	for i := 0; i < opts.Jobs; i++ {
		go collectorWorker(jobs, &wg, opts)
	}

	for _, dep := range toCollect {
		wg.Add(1)
		jobs <- dep
	}
	close(jobs)
	wg.Wait()

	return nil
}

func FixupToplevels(graph *DependencyGraph, opts *CollectorOptions) error {
	for _, ent := range graph.TopDeps {
		if ent.NotResolved {
			LogWarn("Not fixing unresolved toplevel %s", ent.Path)
			continue
		} else if info, err := os.Stat(GetRealPath(ent)); err != nil {
			LogWarn("Cannot stat %s, skipping", ent.Path)
			continue
		} else if err := os.Chmod(GetRealPath(ent), info.Mode()|0700); err != nil {
			LogWarn("Cannot make %s writeable, skipping", ent.Path)
			continue
		}

		for _, subDep := range ent.Deps {
			if subDep.NotResolved || (subDep.Pruned && !subDep.PrunedByFlatDeps) {
				continue
			} else if !opts.CollectFrameworks && isFrameworkLib(subDep.Name) {
				continue
			} else if !opts.ModifySpecialPaths && IsSpecialPath(subDep.Path) {
				continue
			}

			rel, err := filepath.Rel(filepath.Dir(GetRealPath(ent)), filepath.Join(opts.Folder, subDep.Name))
			if err != nil {
				LogWarn("Could not determine relative path to dep %s: %s", GetRealPath(ent), err)
				continue
			}
			out, err := exec.Command("install_name_tool", "-change", subDep.Path, "@loader_path/"+rel, GetRealPath(ent)).CombinedOutput()
			if err != nil {
				LogError("Could not rewrite dep path: %s [%s]", err, out)
			}
		}
	}
	return nil
}
