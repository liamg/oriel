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
