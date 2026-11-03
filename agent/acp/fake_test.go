package acp

import (
	"os"
	"testing"

	"github.com/liamg/oriel/agent/acp/acptest"
	"github.com/liamg/oriel/agent/mcptools/mcpstdio"
)

// The test binary is also `oriel hook mcp` (the MCP server the bridge hands the agent) and, with acptest.Env set,
// the fake ACP agent.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "hook" && os.Args[2] == "mcp" {
		os.Exit(mcpstdio.Main(os.Stdin, os.Stdout, os.Stderr, os.Environ()))
	}
	if os.Getenv(acptest.Env) == "1" {
		os.Exit(acptest.Main())
	}
	os.Exit(m.Run())
}
