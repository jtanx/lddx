package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/fatih/color"
	"github.com/jessevdk/go-flags"
	"github.com/mattn/go-colorable"
)

type Options struct {
	NoColor         bool     `short:"n" long:"no-color" description:"Colourised output"`
	Quiet           bool     `short:"q" long:"quiet" description:"Less verbose output"`
	Version         bool     `short:"v" long:"version" description:"Prints the version of lddx"`
	Recursive       bool     `short:"r" long:"recursive" description:"Recursively find dependencies"`
	Threads         int      `short:"t" long:"threads" default:"10" description:"Number of threads to use (specify 1 for reproducible results"`
	Json            bool     `short:"j" long:"json" description:"Dump dependencies in JSON format"`
	ExecutablePath  string   `short:"e" long:"executable-path" description:"Executable path to use when resolving @executable_path dependencies"`
	IgnoredPrefixes []string `short:"i" long:"ignore-prefix" description:"Specifies a library prefix to ignore when resolving dependencies"`
	NoDefaultIgnore bool     `short:"d" long:"no-default-ignore" description:"By default, libraries under /System and /usr/lib are ignored from dependency resolution. Specify this flag to not ignore these."`
	Collect         string   `short:"c" long:"collect" description:"Collects dependencies into the specified folder"`
	Overwrite       bool     `short:"w" long:"overwrite" description:"Ignore and overwrite existing libraries in the collection folder"`
}

var logMutex sync.Mutex
var opts Options

func LogError(format string, args ...interface{}) {
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Red(format, args...)
}

func LogInfo(format string, args ...interface{}) {
	if opts.Quiet {
		return
	}
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Green(format, args...)
}

func LogNote(format string, args ...interface{}) {
	if opts.Quiet {
		return
	}
	logMutex.Lock()
	defer logMutex.Unlock()
	color.Yellow(format, args...)
}

func setIgnoredPrefixes(opts *Options) {
	ignoredPrefixes := make(map[string]bool)
	if !opts.NoDefaultIgnore {
		ignoredPrefixes["/System"] = true
		ignoredPrefixes["/usr/lib"] = true
	}

	for _, prefix := range opts.IgnoredPrefixes {
		ignoredPrefixes[prefix] = true
	}
	opts.IgnoredPrefixes = nil

	for prefix, _ := range ignoredPrefixes {
		opts.IgnoredPrefixes = append(opts.IgnoredPrefixes, prefix)
	}
}

func main() {
	args, err := flags.Parse(&opts)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	color.Output = colorable.NewColorableStderr()
	color.NoColor = opts.NoColor
	if opts.Version {
		fmt.Println("lddx version 0.0.1")
		os.Exit(0)
	}

	if _, err := DepsCheckOToolVersion(); err != nil {
		LogError("Could not run otool: %s", err)
		LogError("Ensure you have the Command Line Tools installed.")
		os.Exit(1)
	}

	if opts.ExecutablePath != "" {
		path, err := ResolveAbsPath(opts.ExecutablePath)
		if err != nil {
			LogError("Could not resolve executable path: %s", err)
			os.Exit(1)
		}
		opts.ExecutablePath = path
	}

	setIgnoredPrefixes(&opts)
	graph, err := DepsRead(&opts, args...)
	if err != nil {
		LogError("Could not process dependencies: %s", err)
		os.Exit(1)
	}

	if opts.Json {
		if out, err := json.MarshalIndent(graph, "", "\t"); err != nil {
			LogError("Could not serialise as JSON: %s", err)
		} else {
			fmt.Println(string(out))
		}
	} else {
		for _, dep := range graph.TopDeps {
			if len(graph.TopDeps) > 1 {
				fmt.Printf("%s:\n", dep.Path)
			}
			DepsPrettyPrint(dep)
		}
	}
}
