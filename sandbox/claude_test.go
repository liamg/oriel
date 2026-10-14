package sandbox

import "testing"

// The sandbox's permission mode is its own, with no dialog to click through,
// whatever mode the host's settings ask for.
func TestSandboxSettingsSetTheirOwnMode(t *testing.T) {
	host := map[string]any{
		"permissions": map[string]any{"defaultMode": "plan", "allow": []any{"Bash(rm:*)"}, "deny": []any{"Read(./.env)"}},
		"model":       "opus",
	}
	out := SandboxSettings(host, nil, nil)
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

func TestSandboxSettingsPointLocalhostAtTheHost(t *testing.T) {
	host := map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "http://localhost:4000", "FOO": "http://localhost:1"}}
	env := SandboxSettings(host, nil, nil)["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "http://"+HostGateway+":4000" || env["FOO"] != "http://localhost:1" {
		t.Fatalf("env %v", env)
	}
	if host["env"].(map[string]any)["ANTHROPIC_BASE_URL"] != "http://localhost:4000" {
		t.Fatal("the host's settings were changed")
	}
	got := EndpointEnv(nil, nil, host)
	if got["ANTHROPIC_BASE_URL"] != "http://"+HostGateway+":4000" || got["FOO"] != "" {
		t.Fatalf("endpoint env %v", got)
	}
}
