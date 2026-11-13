package claudebox

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
)

// MCPServers are the MCP servers the user installed for Claude Code: ~/.claude.json's user scope, then enabled
// plugins'. A repository's .mcp.json is never read: a cloned repo must not get to run a server here.
func MCPServers(home string) []agent.MCPServer {
	var out []agent.MCPServer
	var state struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	claudeJSON := filepath.Join(home, ".claude.json")
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		claudeJSON = filepath.Join(dir, ".claude.json")
	}
	if b, err := os.ReadFile(claudeJSON); err == nil {
		_ = json.Unmarshal(b, &state)
	}
	for _, name := range slices.Sorted(maps.Keys(state.MCPServers)) {
		out = append(out, agent.MCPServer{Name: name, Source: "user", Config: state.MCPServers[name]})
	}
	for _, p := range enabledPlugins(filepath.Join(home, ".claude")) {
		servers := pluginServers(p.path)
		for _, name := range slices.Sorted(maps.Keys(servers)) {
			out = append(out, agent.MCPServer{Name: name, Source: "plugin:" + p.name, Config: servers[name],
				Vars: map[string]string{"CLAUDE_PLUGIN_ROOT": p.path}})
		}
	}
	return out
}

type plugin struct{ name, path string }

// enabledPlugins are installed plugins the user's settings turn on.
func enabledPlugins(claudeDir string) []plugin {
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if b, err := os.ReadFile(filepath.Join(claudeDir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &settings)
	}
	var installed struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if b, err := os.ReadFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json")); err == nil {
		_ = json.Unmarshal(b, &installed)
	}
	var out []plugin
	for _, id := range slices.Sorted(maps.Keys(installed.Plugins)) {
		if !settings.EnabledPlugins[id] {
			continue
		}
		for _, p := range installPaths(installed.Plugins[id]) {
			name, _, _ := strings.Cut(id, "@")
			out = append(out, plugin{name: name, path: p})
		}
	}
	return out
}

// installPaths reads a plugin's install record, which is one install or a
// list of them depending on the Claude Code version that wrote it.
func installPaths(raw json.RawMessage) []string {
	type install struct {
		InstallPath string `json:"installPath"`
	}
	var one install
	if json.Unmarshal(raw, &one) == nil && one.InstallPath != "" {
		return []string{one.InstallPath}
	}
	var many []install
	_ = json.Unmarshal(raw, &many)
	var out []string
	for _, i := range many {
		if i.InstallPath != "" {
			out = append(out, i.InstallPath)
		}
	}
	return out[:min(len(out), 1)]
}

func pluginServers(dir string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if b, err := os.ReadFile(filepath.Join(dir, ".mcp.json")); err == nil {
		var wrapped struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(b, &wrapped) == nil && len(wrapped.MCPServers) > 0 {
			return wrapped.MCPServers
		}
		_ = json.Unmarshal(b, &out)
	}
	return out
}
