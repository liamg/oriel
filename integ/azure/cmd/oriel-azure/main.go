// Command oriel-azure is the Azure DevOps plugin's process: it answers the plugin's Lua over JSON-RPC on stdin and
// stdout, and reaches Azure DevOps through the host, which holds the token.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/integ/azure"
)

func main() {
	if err := azure.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
