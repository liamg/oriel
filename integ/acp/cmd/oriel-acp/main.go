// Command oriel-acp is the ACP bridge's process, as oriel's plugin-process acp runs it: the Oriel agent protocol on
// stdin and stdout, for an Agent Client Protocol agent it runs through Oriel.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/agent/mcptools/mcpstdio"
	"github.com/liamg/oriel/integ/acp"
)

func main() {
	// Standing in for oriel, it also serves the MCP server it hands its agents: `oriel hook mcp`.
	if len(os.Args) == 3 && os.Args[1] == "hook" && os.Args[2] == "mcp" {
		os.Exit(mcpstdio.Main(os.Stdin, os.Stdout, os.Stderr, os.Environ()))
	}
	if err := acp.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
