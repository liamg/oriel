package claudebox

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox"
)

// The sandbox's permission mode is its own, with no dialog to click through,
// whatever mode the host's settings ask for.
func TestSandboxSettingsSetTheirOwnMode(t *testing.T) {
	host := map[string]any{
		"permissions": map[string]any{"defaultMode": "plan", "allow": []any{"Bash(rm:*)"}, "deny": []any{"Read(./.env)"}},
		"model":       "opus",
	}
	out := SandboxSettings(host, nil, nil, "")
	perms, _ := out["permissions"].(map[string]any)
	if perms["defaultMode"] != "bypassPermissions" {
		t.Errorf("defaultMode = %v", perms["defaultMode"])
	}
	if out["skipDangerousModePermissionPrompt"] != true {
		t.Error("the mode's dialog would stop the agent")
	}
	if deny, _ := perms["deny"].([]any); len(deny) != 1 {
		t.Errorf("the host's denies are kept: %v", perms["deny"])
	}
	if allow, _ := perms["allow"].([]any); len(allow) != 2 {
		t.Errorf("the host's allows are not: %v", perms["allow"])
	}
	if out["model"] != "opus" {
		t.Error("other settings are carried over")
	}
}

// The agent's memory directory is the sandbox's to choose, whatever the host's settings name.
func TestSandboxSettingsSetTheMemoryDirectory(t *testing.T) {
	host := map[string]any{"autoMemoryDirectory": "/elsewhere"}
	if got := SandboxSettings(host, nil, nil, "/mem")["autoMemoryDirectory"]; got != "/mem" {
		t.Errorf("autoMemoryDirectory = %v", got)
	}
}

func TestPrepareWritesThePrimarysMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	m := sandbox.Manifest{Memory: []sandbox.ProjectMemory{{Name: "a", Dir: sandbox.MemoryDir("/src/a")}, {Name: "b", Dir: sandbox.MemoryDir("/src/b")}}}
	if err := sandbox.SaveManifest(root, m); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, root, map[string]any{"autoMemoryDirectory": "/elsewhere"}, nil, Seen{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(Dir(root), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["autoMemoryDirectory"] != m.Memory[0].Dir {
		t.Errorf("autoMemoryDirectory = %v, want %s", got["autoMemoryDirectory"], m.Memory[0].Dir)
	}
}

func TestSandboxSettingsPointLocalhostAtTheHost(t *testing.T) {
	host := map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "http://localhost:4000", "FOO": "http://localhost:1"}}
	env := SandboxSettings(host, nil, nil, "")["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "http://"+sandbox.HostGateway+":4000" || env["FOO"] != "http://localhost:1" {
		t.Fatalf("env %v", env)
	}
	if host["env"].(map[string]any)["ANTHROPIC_BASE_URL"] != "http://localhost:4000" {
		t.Fatal("the host's settings were changed")
	}
}

// The defaults carry Claude Code's own settings into the sandbox, but not another program's variables, nor what a
// Claude Code session set for its own processes.
func TestDefaultEnvCarriesClaudesSettings(t *testing.T) {
	n := agent.SandboxNeeds{PassEnv: passEnv, SkipEnv: skipEnv, URLEnv: urlEnv}
	got := sandbox.HostEnv([]string{
		"ANTHROPIC_BASE_URL=http://litellm.tail1234.ts.net:4000",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=32000",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"MAX_THINKING_TOKENS=8000",
		"API_TIMEOUT_MS=600000",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		"CLAUDE_CODE_SESSION_ID=abc",
		"CLAUDE_CODE_OAUTH_TOKEN=secret",
		"GITHUB_TOKEN=secret",
		"AWS_SECRET_ACCESS_KEY=secret",
	}, nil, n)
	want := map[string]string{
		"ANTHROPIC_BASE_URL":            "http://litellm.tail1234.ts.net:4000",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS": "32000",
		"CLAUDE_CODE_USE_BEDROCK":       "1",
		"MAX_THINKING_TOKENS":           "8000",
		"API_TIMEOUT_MS":                "600000",
		"OTEL_EXPORTER_OTLP_ENDPOINT":   "http://localhost:4318",
	}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := sandbox.HostEnv([]string{"CLAUDE_CODE_ENTRYPOINT=cli", "GITHUB_TOKEN=x"}, []string{"CLAUDE_CODE_ENTRYPOINT", "GITHUB_TOKEN"}, n); len(got) != 2 {
		t.Errorf("settings that name variables get them all: %v", got)
	}
	if got := sandbox.FilterEnv([]string{"ANTHROPIC_BASE_URL=http://localhost:4000"}, nil, n); got["ANTHROPIC_BASE_URL"] != "http://"+sandbox.HostGateway+":4000" {
		t.Errorf("a local endpoint isn't pointed at the host: %v", got)
	}
}
