// Command oriel-sandbox is the helper a sandbox's containers run as
// /usr/local/bin/oriel: the gateway's MCP bridge, the egress proxy, ssh's
// ProxyCommand and git's credential helper.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/sandbox/agentcmd"
)

func main() {
	ok, err := agentcmd.Main(os.Args[1:])
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: oriel mcp | egress-proxy [--listen addr] [--relay dir] | ssh-proxy <host> <port> | git-credential <op>")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		os.Exit(1)
	}
}
