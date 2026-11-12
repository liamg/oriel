package guard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
)

func TestCheck(t *testing.T) {
	spec := agent.StartSpec{Cwd: "/w/ws", Protect: []string{"/src/repo", "/w"}}
	sandboxed := spec
	sandboxed.Sandbox = &agent.Sandbox{Name: "box"}
	cases := []struct {
		name        string
		spec        agent.StartSpec
		tool, input string
		allow, deny bool
		reason      string
	}{
		{"Oriel's tools go ahead", spec, "mcp__oriel__reply", `{}`, true, false, ""},
		{"the gateway gates its own calls", sandboxed, "mcp__oriel-gw__oriel__push", `{}`, true, false, ""},
		{"an edit of the checkout", spec, "Edit", `{"file_path":"/src/repo/a.go"}`, false, true, "original checkout"},
		{"a notebook in the checkout", spec, "NotebookEdit", `{"notebook_path":"/src/repo/n.ipynb"}`, false, true, "original checkout"},
		{"the session's own copy, inside a protected root", spec, "Write", `{"file_path":"/w/ws/a.go"}`, false, false, ""},
		{"a relative path stays in cwd", spec, "Edit", `{"file_path":"a.go"}`, false, false, ""},
		{"a neighbour that only shares a prefix", spec, "Edit", `{"file_path":"/src/repository/a.go"}`, false, false, ""},
		{"a read of the checkout", spec, "Read", `{"file_path":"/src/repo/a.go"}`, false, false, ""},
		{"git push in a sandbox", sandboxed, "Bash", `{"command":"cd x && git -C y push origin main"}`, false, true, Gateway + "push"},
		{"gh in a sandbox", sandboxed, "Bash", `{"command":"gh pr create"}`, false, true, Gateway + "pr_create"},
		{"git push on the host", spec, "Bash", `{"command":"git push"}`, false, false, ""},
		{"a quoted git push is an argument", sandboxed, "Bash", `{"command":"echo 'git push'"}`, false, false, ""},
		{"a review by gh", spec, "Bash", `{"command":"gh pr review 7 --approve"}`, false, true, "never post"},
		{"a GitHub MCP review reply", spec, "mcp__github__add_reply_to_pull_request_comment", `{}`, false, true, "never post"},
	}
	for _, c := range cases {
		got := Check(c.spec, c.tool, json.RawMessage(c.input))
		if got.Allow != c.allow || got.Deny != c.deny || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// Every other MCP server's call is Oriel's to decide, whatever the agent's own gate would: a connector's, the user's
// own server's, and one named like the gateway outside a sandbox. An agent nobody can ask (PolicyRefuse) gets an
// answer at once: its Allow goes ahead, the rest is refused.
func TestMCPCallsAreOrielsToDecide(t *testing.T) {
	spec := agent.StartSpec{Cwd: "/w/ws"}
	sandboxed := spec
	sandboxed.Sandbox = &agent.Sandbox{Name: "box"}
	refuse := agent.StartSpec{Cwd: "/w/ws", Policy: agent.PolicyRefuse, Unattended: true, Allow: []string{"WebSearch", "mcp__linear__list_issues"}}
	cases := []struct {
		name             string
		spec             agent.StartSpec
		tool             string
		allow, deny, ask bool
	}{
		{"a claude.ai connector", spec, "mcp__claude_ai_Claude_Docs__guide", false, false, true},
		{"the user's own server", spec, "mcp__linear__list_issues", false, false, true},
		{"a connector in a sandbox", sandboxed, "mcp__claude_ai_Claude_Docs__guide", false, false, true},
		{"a server named like the gateway, on the host", spec, "mcp__oriel-gw__oriel__push", false, false, true},
		{"Oriel's own", spec, "mcp__oriel__reply", true, false, false},
		{"not an MCP tool", spec, "Bash", false, false, false},
		{"a utility agent's own tools", refuse, "mcp__oriel__sessions", true, false, false},
		{"a utility agent's allowed server", refuse, "mcp__linear__list_issues", true, false, false},
		{"a utility agent's connector", refuse, "mcp__claude_ai_Claude_Docs__guide", false, true, false},
		{"a utility agent's other tools are its gate's", refuse, "Bash", false, false, false},
	}
	for _, c := range cases {
		got := Check(c.spec, c.tool, json.RawMessage(`{}`))
		if got.Allow != c.allow || got.Deny != c.deny || got.Ask != c.ask {
			t.Errorf("%s: %+v", c.name, got)
		}
		if got.Deny && !strings.Contains(got.Reason, c.tool) {
			t.Errorf("%s: the refusal doesn't name the tool: %q", c.name, got.Reason)
		}
	}
}

func TestProtected(t *testing.T) {
	spec := agent.StartSpec{Cwd: "/w/ws", Protect: []string{"/src/repo"}}
	if d := Protected(spec, "/src/repo/x/y.go"); !d.Deny || !strings.Contains(d.Reason, "/w/ws") {
		t.Errorf("a file in the checkout: %+v", d)
	}
	for _, f := range []string{"", "y.go", "/w/ws/y.go", "/tmp/y.go"} {
		if d := Protected(spec, f); d.Deny {
			t.Errorf("%q: %+v", f, d)
		}
	}
}
