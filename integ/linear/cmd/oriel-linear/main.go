// Command oriel-linear is the Linear plugin's process: it answers the plugin's Lua over JSON-RPC on stdin and
// stdout, and reaches Linear through the host, which holds the API key.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/integ/linear"
)

func main() {
	if err := linear.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
