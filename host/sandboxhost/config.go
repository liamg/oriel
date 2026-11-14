package sandboxhost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/internal/atomicfile"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/gateway"
)

// configPath is where the host keeps the sandbox settings the app last sent, for sessions that start while no app is
// connected.
func (x *Boxes) configPath() string { return filepath.Join(x.deps.Env.Home, "sandbox.json") }

func (x *Boxes) load() error {
	b, err := os.ReadFile(x.configPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg sandbox.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		x.deps.Env.Log.Warn("sandbox settings", "path", x.configPath(), "err", err)
		return nil
	}
	x.cfgMu.Lock()
	x.cfg = cfg
	x.cfgMu.Unlock()
	return nil
}

// SetConfig is the sandbox's settings, as the app resolved them from its sandbox.* options. Sandboxes opened after
// it use them.
func (x *Boxes) SetConfig(cfg sandbox.Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(x.configPath(), append(b, '\n'), 0o600); err != nil {
		return err
	}
	x.cfgMu.Lock()
	x.cfg = cfg
	x.cfgMu.Unlock()
	return nil
}

// Config is the sandbox settings the app last sent; the defaults if it never has.
func (x *Boxes) Config() sandbox.Config {
	x.cfgMu.Lock()
	defer x.cfgMu.Unlock()
	return x.cfg
}

// MCPServers are the MCP servers a sandbox's gateway carries, as an agent reaches them: each one's name and config.
func (x *Boxes) MCPServers() []agent.MCPServer {
	var providers []agent.Provider
	if x.deps.Providers != nil {
		providers = x.deps.Providers()
	}
	cfg := x.Config()
	var out []agent.MCPServer
	for _, s := range gateway.Found(providers, cfg.Servers, cfg.Hidden) {
		raw, err := json.Marshal(s)
		if err != nil {
			continue
		}
		out = append(out, agent.MCPServer{Name: s.Name, Source: s.Source, Config: raw})
	}
	return out
}
