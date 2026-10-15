// Command oriel-registry is `oriel plugin registry` alone, built with no cgo: what the plugin registry's CI runs.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/catalog/registrycmd"
)

func main() {
	if err := registrycmd.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "oriel-registry:", err)
		os.Exit(1)
	}
}
