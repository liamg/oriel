package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// "Undeclared calls fail with a clear error naming the missing capability: never a
// silent no-op". ui, theme and notify need no declaration ("no: always allowed").
func TestGuardRejectsUndeclared(t *testing.T) {
	work := RealPath(t.TempDir()) // as callers check paths
	g := NewGrants()
	g.Places = func(name string) []string {
		if name == "worktree" {
			return []string{work}
		}
		return nil
	}
	g.Grant("acme/jira", []Grant{
		{Cap: CapNet, Scope: []string{"acme.atlassian.net", "*.linear.app"}},
		{Cap: CapFSRead, Scope: []string{"worktree"}},
		{Cap: CapSecrets, Scope: []string{"JIRA_TOKEN@acme.atlassian.net"}},
		{Cap: CapEditorRead},
	})
	for _, c := range []struct {
		cap    Capability
		target string
		ok     bool
	}{
		{CapUI, "", true},
		{CapTheme, "", true},
		{CapNotify, "", true},
		{CapEditorRead, "", true},
		{CapEditorWrite, "", false},
		{CapGate, "Permission", false},
		{CapNet, "acme.atlassian.net", true},
		{CapNet, "acme.atlassian.net:443", true},
		{CapNet, "ACME.atlassian.net", true},
		{CapNet, "evil.atlassian.net", false},
		{CapNet, "api.linear.app", true},
		{CapNet, "linear.app", false},
		{CapNet, "", true},
		{CapFSRead, filepath.Join(work, "a/b.go"), true},
		{CapFSRead, work, true},
		{CapFSRead, work + "x/y", false},
		{CapFSRead, filepath.Join(work, "../x"), false},
		{CapFSRead, "/etc/passwd", false},
		{CapFSWrite, filepath.Join(work, "a"), false},
		{CapSecrets, "JIRA_TOKEN", true},
		{CapSecrets, "GITHUB_TOKEN", false},
		{CapExec, "/bin/sh", false},
	} {
		err := g.Check("acme/jira", c.cap, c.target)
		if (err == nil) != c.ok {
			t.Errorf("%s %q: %v, want allowed=%v", c.cap, c.target, err, c.ok)
		}
		if err != nil {
			if !errors.Is(err, ErrNotDeclared) || !strings.Contains(err.Error(), string(c.cap)) || !strings.Contains(err.Error(), "acme/jira") {
				t.Errorf("%s: the error doesn't name the plugin and capability: %v", c.cap, err)
			}
		}
	}
	if err := g.Check("someone/else", CapEditorRead, ""); err == nil {
		t.Error("another plugin's grant")
	}
	audit := g.Audit()
	if len(audit) == 0 || audit[len(audit)-1].Plugin != "someone/else" || audit[len(audit)-1].Allowed {
		t.Errorf("the audit's last use: %+v", audit[len(audit)-1])
	}
	g.Revoke("acme/jira")
	if g.Check("acme/jira", CapEditorRead, "") == nil {
		t.Error("revoked, still granted")
	}
}

// A symlink inside a granted directory doesn't reach out of it.
func TestGuardFollowsSymlinks(t *testing.T) {
	work, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(work, "link")); err != nil {
		t.Skip(err)
	}
	ms := HostMethods(nopReporter{})
	g := NewGrants()
	g.Grant("p/q", []Grant{{Cap: CapFSRead, Scope: []string{work}}})
	_, err := ms.Call(t.Context(), Caller{ID: "p/q", Guard: g}, "fs.read", []byte(`{"path":"`+filepath.Join(work, "link", "secret")+`"}`))
	if err == nil || !strings.Contains(err.Error(), "fs.read") {
		t.Errorf("read through a link out of the grant: %v", err)
	}
}

// A bare command name grants the program PATH finds, not another of that name.
func TestGuardCommands(t *testing.T) {
	sh, err := execLook("sh")
	if err != nil {
		t.Skip(err)
	}
	g := NewGrants()
	g.Grant("p/q", []Grant{{Cap: CapExec, Scope: []string{"sh"}}})
	if err := g.Check("p/q", CapExec, sh); err != nil {
		t.Error(err)
	}
	if err := g.Check("p/q", CapExec, "/tmp/evil/sh"); err == nil {
		t.Error("another program named sh")
	}
	// A grant of the path allows the bare name that finds it, as a grant of the name allows the path.
	g.Grant("p/r", []Grant{{Cap: CapExec, Scope: []string{sh}}})
	if err := g.Check("p/r", CapExec, "sh"); err != nil {
		t.Errorf("sh, granted as %s: %v", sh, err)
	}
	if err := g.Check("p/r", CapExec, "ls"); err == nil {
		t.Error("another program, granted sh's path")
	}
}

// A full manifest, with [permissions] given as tables and in the short form (= true).
func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(`
id          = "acme/jira"
version     = "0.4.2"
oriel       = ">=0.9"
description = "Jira tickets in Oriel"
runs        = "both"
lazy        = { commands = ["Jira*"], events = ["SessionStart"] }
binaries    = ["darwin/arm64", "linux/amd64"]

[permissions]
net     = { hosts = ["acme.atlassian.net"], why = "Fetch and update Jira tickets" }
secrets = { JIRA_TOKEN = "acme.atlassian.net", why = "Authenticate to your Jira" }
"agent.tools" = { tools = ["jira_ticket", "jira_comment"], why = "Let the agent read the ticket it's working on" }
fs.read = { scope = "worktree" }
"editor.read" = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "acme/jira" || m.Version != "0.4.2" || m.Runs != "both" || len(m.Lazy.Commands) != 1 || len(m.Binaries) != 2 {
		t.Errorf("manifest: %+v", m)
	}
	got := map[Capability]Grant{}
	for _, g := range m.Permissions {
		got[g.Cap] = g
	}
	for c, want := range map[Capability]string{CapNet: "acme.atlassian.net", CapSecrets: "JIRA_TOKEN@acme.atlassian.net",
		CapAgentTools: "jira_comment,jira_ticket", CapFSRead: "worktree", CapEditorRead: ""} {
		if g, ok := got[c]; !ok || strings.Join(g.Scope, ",") != want {
			t.Errorf("%s: %+v, want scope %q", c, g, want)
		}
	}
	if got[CapNet].Why != "Fetch and update Jira tickets" {
		t.Errorf("net's why: %q", got[CapNet].Why)
	}
	short, err := ParseManifest([]byte(`id = "acme/linear"
permissions = { net = ["api.linear.app"], secrets = ["LINEAR_TOKEN"] }`))
	if err != nil || len(short.Permissions) != 2 || short.Permissions[0].Scope[0] != "api.linear.app" {
		t.Errorf("the short form: %+v, %v", short, err)
	}
	for _, bad := range []string{`id = "nope"`, `id = "a/b"
permissions = { telepathy = true }`, `id = "a/b"
permissions = { exec = true }`, `id = "a/b"
runs = "everywhere"`} {
		if _, err := ParseManifest([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// CheckProgram checks the program that will run: a path the plugin names is its own directory's (or the given
// one's), absolute, so a grant for its bin/server never lets the project's bin/server run; a bare name is checked
// as named.
func TestCheckProgram(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "bin", "server")
	os.MkdirAll(filepath.Dir(server), 0o755)
	os.WriteFile(server, []byte("#!/bin/sh\n"), 0o755)
	g := NewGrants()
	g.Grant("p/q", []Grant{{Cap: CapExec, Scope: []string{RealPath(server), "gopls"}}})
	c := Caller{ID: "p/q", Dir: dir, Guard: g}
	if got, err := c.CheckProgram("bin/server", ""); err != nil || got != RealPath(server) {
		t.Errorf("its own bin/server: %q, %v", got, err)
	}
	if _, err := c.CheckProgram("bin/server", t.TempDir()); err == nil {
		t.Error("another directory's bin/server was allowed")
	}
	if got, err := c.CheckProgram("gopls", ""); err != nil || got != "gopls" {
		t.Errorf("a bare name: %q, %v", got, err)
	}
	if _, err := c.CheckProgram("rm", ""); err == nil {
		t.Error("a program the grant doesn't name was allowed")
	}
}

// Widen grants a plugin more at run time, where the audit shows it and why, and its undo takes exactly that back.
func TestWidenIsAuditedAndUndone(t *testing.T) {
	g := NewGrants()
	g.Grant("p/q", []Grant{{Cap: CapNet, Scope: []string{"api.example.com"}}})
	undo := g.Widen("p/q", "probing git.acme.corp for GitLab", Grant{Cap: CapNet, Scope: []string{"git.acme.corp"}})
	if err := g.Check("p/q", CapNet, "git.acme.corp"); err != nil {
		t.Fatalf("widened: %v", err)
	}
	var widened *Use
	for _, u := range g.Audit() {
		if u.Why != "" {
			widened = &u
		}
	}
	if widened == nil || widened.Cap != CapNet || widened.Target != "git.acme.corp" || widened.Why != "probing git.acme.corp for GitLab" {
		t.Errorf("the audit has no widening: %+v", g.Audit())
	}
	undo()
	if err := g.Check("p/q", CapNet, "git.acme.corp"); err == nil {
		t.Error("undone, it still reaches git.acme.corp")
	}
	if err := g.Check("p/q", CapNet, "api.example.com"); err != nil {
		t.Errorf("undo took the declared grant too: %v", err)
	}
}
