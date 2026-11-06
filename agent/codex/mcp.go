package codex

import (
	"io"
	"os"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/mcptools"
	"github.com/liamg/oriel/agent/mcptools/mcpstdio"
)

// SockEnv names the unix socket `oriel hook mcp` dials to reach the session's tools.
const SockEnv = agent.HookSocketEnv

// MCPMain is `oriel hook mcp` (mcpstdio.Main), its errors on stderr.
func MCPMain(stdin io.Reader, stdout io.Writer, env []string) int {
	return mcpstdio.Main(stdin, stdout, os.Stderr, env)
}

// toolServer is the session's tools served for `oriel hook mcp`, at path.
type toolServer struct {
	*mcptools.Server
	path string
}

func serveTools(tools func() []agent.Tool) (*toolServer, error) {
	s, err := mcptools.Serve(tools)
	if err != nil {
		return nil, err
	}
	return &toolServer{Server: s, path: s.Path()}, nil
}
