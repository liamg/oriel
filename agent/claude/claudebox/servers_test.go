package claudebox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The user's servers: ~/.claude.json's user scope, then the enabled plugins', each plugin's with its root to
// expand. A project's own and a disabled plugin's aren't.
func TestMCPServersAreTheUsersAndTheirPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plug := filepath.Join(home, ".claude", "plugins", "cache", "linear")
	write(filepath.Join(home, ".claude.json"), `{"mcpServers":{"aws":{"command":"aws-mcp"}},"projects":{"/x":{"mcpServers":{"repo":{"command":"evil"}}}}}`)
	write(filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"linear@official":true,"off@official":false}}`)
	write(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"plugins":{"linear@official":[{"installPath":"`+plug+`"}],"off@official":[{"installPath":"/nope"}]}}`)
	write(filepath.Join(plug, ".mcp.json"), `{"linear":{"type":"http","url":"https://mcp.linear.app/mcp"}}`)
	got := MCPServers(home)
	var names []string
	for _, s := range got {
		names = append(names, s.Name+"/"+s.Source)
	}
	if strings.Join(names, " ") != "aws/user linear/plugin:linear" {
		t.Fatalf("servers = %v", names)
	}
	if got[1].Vars["CLAUDE_PLUGIN_ROOT"] != plug {
		t.Errorf("the plugin's root = %v", got[1].Vars)
	}
}
