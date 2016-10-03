package lddx

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	for i := 0; (i < len(order)) && (nice1 < 0 || nice2 < 0); i++ {
		if nice1 < 0 && strings.HasPrefix(ent1, order[i]) {
			nice1 = i
		}

		if nice2 < 0 && strings.HasPrefix(ent2, order[i]) {
			nice2 = i
		}
	}

	return nice1, nice2
}

// isFrameworkLib determines if the file is likely a framework dylib.
// They have no extensions. I think.
func isFrameworkLib(file string) bool {
	return filepath.Ext(file) == ""
}

// Copies a file and ensures it's writeable
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

func collectorWorker(jobs <-chan *Dependency, results chan<- []string, opts *CollectorOptions) {
	var errList []string

	for dep := range jobs {
		// LogInfo("PROCESSING %s", dep.Path)
		destination := filepath.Join(opts.Folder, dep.Name)
		if err := copyFile(dep.RealPath, destination); err != nil {
			errList = append(errList, fmt.Sprintf("Could not copy file %s [%s]: %s", dep.Path, dep.RealPath, err))
		} else {
			out, err := exec.Command("install_name_tool", "-id", "@loader_path/"+dep.Name, destination).CombinedOutput()
			if err != nil {
				errList = append(errList, fmt.Sprintf("Could not update identity for %s [%s]: %s [%s]", dep.Path, dep.RealPath, err, out))
			} else {
				for _, subDep := range *dep.Deps {
					if subDep.NotResolved || (subDep.Pruned && !subDep.PrunedByFlatDeps) {
						continue
					} else if !opts.CollectFrameworks && isFrameworkLib(subDep.Name) {
						continue
					} else if !opts.ModifySpecialPaths && IsSpecialPath(subDep.Path) {
						continue
					}

					out, err := exec.Command("install_name_tool", "-change", subDep.Path, "@loader_path/"+subDep.Name, destination).CombinedOutput()
					if err != nil {
						errList = append(errList, fmt.Sprintf("Could not rewrite dep path for %s [%s]: %s [%s]", dep.Path, dep.RealPath, err, out))
					}
				}
			}
		}
		results <- errList
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

	jobs := make(chan *Dependency, opts.Jobs)
	results := make(chan []string, len(toCollect))
	for i := 0; i < opts.Jobs; i++ {
		go collectorWorker(jobs, results, opts)
	}

	for _, dep := range toCollect {
		jobs <- dep
	}
	close(jobs)

	var errors []string
	for range toCollect {
		errors = append(errors, <-results...)
	}

	if errors != nil {
		return fmt.Errorf(strings.Join(errors, "\n"))
	}

	return nil
}

func FixupToplevels(graph *DependencyGraph, opts *CollectorOptions) error {
	for _, ent := range graph.TopDeps {
		if ent.NotResolved {
			LogWarn("Not fixing unresolved toplevel %s", ent.Path)
			continue
		} else if info, err := os.Stat(ent.RealPath); err != nil {
			LogWarn("Cannot stat %s, skipping", ent.Path)
			continue
		} else if err := os.Chmod(ent.RealPath, info.Mode()|0700); err != nil {
			LogWarn("Cannot make %s writeable, skipping", ent.Path)
			continue
		}

		for _, subDep := range *ent.Deps {
			if subDep.NotResolved || (subDep.Pruned && !subDep.PrunedByFlatDeps) {
				continue
			} else if !opts.CollectFrameworks && isFrameworkLib(subDep.Name) {
				continue
			} else if !opts.ModifySpecialPaths && IsSpecialPath(subDep.Path) {
				continue
			}

			rel, err := filepath.Rel(filepath.Dir(ent.RealPath), filepath.Join(opts.Folder, subDep.Name))
			if err != nil {
				LogWarn("Could not determine relative path to dep %s: %s", ent.RealPath, err)
				continue
			}
			out, err := exec.Command("install_name_tool", "-change", subDep.Path, "@loader_path/"+rel, ent.RealPath).CombinedOutput()
			if err != nil {
				LogError("Could not rewrite dep path: %s [%s]", err, out)
			}
		}
	}
	return nil
}
