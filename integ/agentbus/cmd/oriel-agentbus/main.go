// Command oriel-agentbus is the agentbus plugin's process: it holds the connection to agentbus for the plugin's Lua,
// over JSON-RPC on stdin and stdout, in the sandbox the plugin's grants open. It's a process of its own so agentbus
// stays out of Oriel's own binary.
package main

import (
	"os"

	"github.com/liamg/oriel/integ/agentbus"
)

func main() {
	agentbus.Serve(os.Stdin, os.Stdout, &agentbus.Server{})
}
