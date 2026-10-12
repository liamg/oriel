// Command refcheck fails if a test reference doesn't resolve in spec/ or misquotes it.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/tools/layout"
	"github.com/liamg/oriel/tools/refcheck"
)

func main() {
	wd, _ := os.Getwd()
	root, err := layout.Root(wd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "refcheck:", err)
		os.Exit(2)
	}
	n, problems, err := refcheck.Run(root)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "refcheck:", err)
		os.Exit(2)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Printf("refcheck: %d references ok\n", n)
}
