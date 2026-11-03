// Command oriel-sandbox is the helper a sandbox's containers run as
// /usr/local/bin/oriel: the gateway's MCP bridge, the egress proxy, ssh's
// ProxyCommand, git's credential helper, and the Claude mod's `oriel hook`, and
// `oriel hook mcp`, the agents' MCP server of Oriel's tools.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/agent/mcptools/mcpstdio"
	"github.com/liamg/oriel/sandbox/agentcmd"
	"github.com/liamg/oriel/sandbox/hookcall"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "hook" && os.Args[2] == "mcp" {
		os.Exit(mcpstdio.Main(os.Stdin, os.Stdout, os.Stderr, os.Environ()))
	}
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(hookcall.Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	ok, err := agentcmd.Main(os.Args[1:])
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: oriel mcp | egress-proxy [--listen addr] [--relay dir] | ssh-proxy <host> <port> | git-credential <op> | hook <op>")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		os.Exit(1)
	}
}
