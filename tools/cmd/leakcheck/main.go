// Command leakcheck fails if a tracked file holds a private name: the built-in terms plus ORIEL_LEAK_DENYLIST
// (one per line), which CI sets from a repository variable.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/tools/layout"
	"github.com/liamg/oriel/tools/leakcheck"
)

func main() {
	wd, _ := os.Getwd()
	root, err := layout.Root(wd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "leakcheck:", err)
		os.Exit(2)
	}
	terms := append(leakcheck.Terms(os.Getenv("ORIEL_LEAK_DENYLIST")), leakcheck.Builtin...)
	files, err := leakcheck.Files(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "leakcheck:", err)
		os.Exit(2)
	}
	hits, err := leakcheck.Scan(root, files, terms)
	if err != nil {
		fmt.Fprintln(os.Stderr, "leakcheck:", err)
		os.Exit(2)
	}
	for _, h := range hits {
		fmt.Fprintln(os.Stderr, h)
	}
	if len(hits) > 0 {
		os.Exit(1)
	}
	fmt.Printf("leakcheck: %d files, %d terms, clean\n", len(files), len(terms))
}
