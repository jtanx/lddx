package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/jtanx/lddx/lddx"
)

func main() {
	var graph *lddx.DependencyGraph
	if len(os.Args) < 2 {
		fmt.Printf("Usage %s lddxdata.json\n", os.Args[0])
		os.Exit(1)
	}

	for i := 1; i < len(os.Args); i++ {
		if fd, err := ioutil.ReadFile(os.Args[i]); err != nil {
			fmt.Printf("Cannot read: %s\n", err)
			os.Exit(1)
		} else if err := json.Unmarshal(fd, &graph); err != nil {
			fmt.Printf("Cannot unmarshal: %s\n", err)
			os.Exit(1)
		} else {
			for _, dep := range graph.TopDeps {
				if len(graph.TopDeps) > 1 {
					fmt.Printf("%s:\n", dep.Path)
				}
				lddx.DepsPrettyPrint(dep)
			}
		}
	}
}
