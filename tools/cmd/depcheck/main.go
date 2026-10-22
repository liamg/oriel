// Command depcheck fails if the workspace requires a module that tools/depcheck/allowed.txt doesn't approve.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/tools/depcheck"
	"github.com/liamg/oriel/tools/layout"
)

func main() {
	wd, _ := os.Getwd()
	root, err := layout.Root(wd)
	if err == nil {
		var problems []string
		problems, err = depcheck.Run(root)
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		if err == nil && len(problems) > 0 {
			os.Exit(1)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "depcheck:", err)
		os.Exit(2)
	}
}
