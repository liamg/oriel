// Command oriel is the desktop app, the worker and the CLI.
package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "app"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "oriel", version)
		return 0
	case "app", "worker":
		fmt.Fprintf(stderr, "oriel %s: not implemented yet\n", cmd)
		return 1
	default:
		fmt.Fprintf(stderr, "oriel: unknown command %q\n", cmd)
		return 2
	}
}
