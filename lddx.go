package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"

	"github.com/jessevdk/go-flags"
	. "github.com/jtanx/lddx/lddx"
)

type options struct {
	NoColor         bool     `short:"n" long:"no-color" description:"Colourised output"`
	Quiet           bool     `short:"q" long:"quiet" description:"Less verbose output"`
	Version         bool     `short:"v" long:"version" description:"Prints the version of lddx"`
	Recursive       bool     `short:"r" long:"recursive" description:"Recursively find dependencies"`
	Jobs            int      `short:"j" long:"jobs" default:"10" description:"Number of files to process concurrently."`
	JSON            bool     `short:"s" long:"json" description:"Dump dependencies in JSON format"`
	IgnoredPrefixes []string `short:"i" long:"ignore-prefix" description:"Specifies a library prefix to ignore when resolving dependencies"`
	IgnoredFiles    []string `short:"x" long:"ignore-file" description:"Specifies a file (e.g. libz.dylib) to ignore when resolving dependencies (case sensitive)"`
	NoDefaultIgnore bool     `short:"d" long:"no-default-ignore" description:"By default, libraries under /System and /usr/lib are ignored from dependency resolution. Specify this flag to not ignore these"`
	ExecutablePath  string   `short:"e" long:"executable-path" description:"Executable path to use when resolving @executable_path dependencies"`
	SkipWeakLibs    bool     `long:"skip-weak" description:"Skip handling weakly loaded libs"`

	Collect            string   `short:"c" long:"collect" description:"Collects dependencies into the specified folder"`
	CollectOrder       []string `short:"l" long:"collect-order" description:"Specifies a prefix to prefer when resolving conflicts in library collection"`
	Overwrite          bool     `short:"w" long:"overwrite" description:"Ignore and overwrite existing libraries in the collection folder"`
	ModifySpecialPaths bool     `short:"m" long:"modify-special-paths" description:"Collect and modify special paths (e.g. @executable_path/@loader_path) when collecting dependencies"`
	CollectFrameworks  bool     `short:"f" long:"collect-frameworks" descrption:"Include Framework libraries in the collection"`

	CpuProfile string `long:"cpu-profile" description:"Run CPU profiling (e.g. --cpu-profile=cpuprofile.pprof)"`
	MemProfile string `long:"mem-profile" description:"Run memory profiling (e.g. --mem-profile=memprofile.pprof)"`
}

func setIgnoredPrefixes(opts *options, depOpts *DependencyOptions) {
	ignoredPrefixes := make(map[string]bool)
	if !opts.NoDefaultIgnore {
		ignoredPrefixes["/System"] = true
		ignoredPrefixes["/usr/lib"] = true
	}

	for _, prefix := range opts.IgnoredPrefixes {
		ignoredPrefixes[prefix] = true
	}

	for prefix := range ignoredPrefixes {
		depOpts.IgnoredPrefixes = append(depOpts.IgnoredPrefixes, prefix)
	}
}

func expandFileList(files []string) []string {
	var ret []string

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			LogError("Cannot process %s: %s", file, err)
			continue
		}

		if info.IsDir() {
			sublist, err := FindFatMachOFiles(file)
			if err != nil {
				LogError("Cannot process %s: %s", file, err)
				continue
			}
			ret = append(ret, sublist...)
		} else {
			ret = append(ret, file)
		}
	}
	return ret
}

func main() {
	var opts options
	parser := flags.NewParser(&opts, flags.HelpFlag|flags.PassDoubleDash)
	args, err := parser.Parse()
	if err != nil {
		switch er := err.(type) {
		case *flags.Error:
			if er.Type == flags.ErrHelp {
				fmt.Println(err)
				os.Exit(0)
			}
		}
		LogError("%s", err)
		os.Exit(1)
	}

	if opts.Version {
		fmt.Println("lddx version 0.1.0")
		os.Exit(0)
	}

	if opts.CpuProfile != "" {
		if fp, err := os.Create(opts.CpuProfile); err != nil {
			LogError("Could not create CPU profile: %s", err)
			os.Exit(1)
		} else {
			pprof.StartCPUProfile(fp)
			defer pprof.StopCPUProfile()
		}
	}

	LogInit(opts.NoColor, opts.Quiet)

	depOpts := DependencyOptions{
		Recursive:      opts.Recursive,
		Jobs:           opts.Jobs,
		IgnoredFiles:   opts.IgnoredFiles,
		SkipWeakLibs:   opts.SkipWeakLibs,
		ExecutablePath: opts.ExecutablePath,
		// Ignored prefixes set below.
	}
	setIgnoredPrefixes(&opts, &depOpts)

	graph, err := DepsRead(depOpts, expandFileList(args)...)
	if err != nil {
		LogError("Could not process dependencies: %s", err)
		os.Exit(1)
	}

	if opts.JSON {
		if out, err := json.MarshalIndent(DepsGetJSONSerialisableVersion(graph), "", "\t"); err != nil {
			LogError("Could not serialise as JSON: %s", err)
		} else {
			fmt.Println(string(out))
		}
	} else if opts.Collect == "" || !opts.Quiet {
		for _, dep := range graph.TopDeps {
			if len(graph.TopDeps) > 1 {
				fmt.Printf("%s:\n", dep.Path)
			}
			DepsPrettyPrint(dep)
		}
	}

	if opts.Collect != "" {
		collectorOpts := CollectorOptions{
			Folder:             opts.Collect,
			PreferredOrder:     opts.CollectOrder,
			Overwrite:          opts.Overwrite,
			Jobs:               opts.Jobs,
			ModifySpecialPaths: opts.ModifySpecialPaths,
			CollectFrameworks:  opts.CollectFrameworks,
		}

		if err := CollectDeps(graph, &collectorOpts); err != nil {
			LogError("Could not collect dependencies: %s", err)
			os.Exit(1)
		} else if err := FixupToplevels(graph, &collectorOpts); err != nil {
			LogError("Could not fixup toplevels: %s", err)
			os.Exit(1)
		}
	}

	if opts.MemProfile != "" {
		if fp, err := os.Create(opts.MemProfile); err != nil {
			LogError("Could not create memory profile: %s", err)
			os.Exit(1)
		} else {
			defer fp.Close()
			runtime.GC()
			if err := pprof.WriteHeapProfile(fp); err != nil {
				LogError("Could not make memory profile: %s", err)
				os.Exit(1)
			}
		}
	}
}
