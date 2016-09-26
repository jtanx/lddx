package main

import (
	"fmt"
	"github.com/fatih/color"
	"github.com/jessevdk/go-flags"
	"github.com/mattn/go-colorable"
	"os"
	"sync"
)

type Options struct {
	NoColor   bool `short:"c" long:"no-color" description:"Colourised output"`
	Quiet     bool `short:"q" long:"quiet" description:"Less verbose output"`
	Version   bool `short:"v" long:"version" description:"Prints the version of lddx"`
	Recursive bool `short:"r" long:"recursive" description:"Recursively find dependencies"`
	Threads   int  `short:"t" long:"threads" default:"10" description:"Number of threads to use (specify 1 for reproducible results"`
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

	graph, err := DepsRead(opts.Recursive, opts.Threads, args...)
	if err != nil {
		LogError("Could not process dependencies: %s", err)
		os.Exit(1)
	}

	for _, dep := range graph.TopDeps {
		if len(graph.TopDeps) > 1 {
			fmt.Printf("%s:\n", dep.Path)
		}
		DepsPrettyPrint(dep)
	}
}
