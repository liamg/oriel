package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/toml"
)

// In [plugins], a string is a semver range resolved through the registries; a table with git or path is a source
// outside them.
func TestParseEntries(t *testing.T) {
	doc, err := toml.Parse([]byte(`[plugins]
"acme/jira"      = "^0.4"
"liamg/surround" = "*"
"someone/thing"  = { git = "github.com/someone/thing" }
"me/wip"         = { path = "~/src/wip" }
"acme/linear"    = { version = "^1", enabled = false }
"user/pickers"   = { version = "*", lazy = { keys = "<leader>f" } }
"bad"            = "1"
"two/sources"    = { git = "x", path = "y" }
"git/version"    = { git = "x", version = "^1" }
"bad/range"      = "^abc"
`))
	if err != nil {
		t.Fatal(err)
	}
	es, errs := ParseEntries(doc.Value()["plugins"].(map[string]any))
	var got []string
	for _, e := range es {
		s := e.ID + " " + e.Source() + " " + e.Range
		if !e.Enabled {
			s += " off"
		}
		if e.Lazy != nil {
			s += " lazy:" + strings.Join(e.Lazy.Keys, ",")
		}
		got = append(got, s)
	}
	want := []string{"acme/jira registry ^0.4", "acme/linear registry ^1 off", "liamg/surround registry *",
		"me/wip path:~/src/wip ", "someone/thing git:github.com/someone/thing ", "user/pickers registry * lazy:<leader>f"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(errs) != 4 {
		t.Errorf("errors: %v, want 4", errs)
	}
	if u := (Entry{Git: "github.com/someone/thing"}).GitURL(); u != "https://github.com/someone/thing" {
		t.Errorf("GitURL = %s", u)
	}
}

// The lockfile records source, version, commit, badge, capabilities and archives, by id. A write changes only
// the plugins that changed.
func TestLockRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFile)
	l, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	jira := Locked{Source: "registry:liamg/oriel-plugins", Version: "0.4.2", Commit: "9c1e07a", Badge: BadgeReviewed,
		Capabilities: []string{"net:acme.atlassian.net", "agent.tools"}, Archives: map[string]string{"linux/amd64": "sha256:ab"}}
	if err := l.Set("acme/jira", jira); err != nil {
		t.Fatal(err)
	}
	if err := l.Set("me/wip", Locked{Source: "path:~/src/wip", Badge: BadgeUnverified}); err != nil {
		t.Fatal(err)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := again.Get("acme/jira")
	if !ok || !equalLocked(got, jira) {
		t.Errorf("read back %+v, want %+v", got, jira)
	}
	if ids := strings.Join(again.IDs(), " "); ids != "acme/jira me/wip" {
		t.Errorf("ids %s", ids)
	}
	again.Delete("acme/jira")
	again.Delete("me/wip")
	if err := again.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLock(path); err != nil {
		t.Fatal(err)
	}
}

// Saving a lock nothing changed leaves the file as it is now, whoever wrote it since it was read.
func TestLockUnchangedLeavesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFile)
	for _, before := range []string{"", "[\"me/wip\"]\nsource = \"path:~/src/wip\"\n"} {
		if before != "" {
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		l, err := LoadLock(path)
		if err != nil {
			t.Fatal(err)
		}
		since := "[\"acme/hi\"]\nsource = \"path:~/src/hi\"\n"
		if err := os.WriteFile(path, []byte(since), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := l.Save(); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != since {
			t.Errorf("read as %q, saved unchanged: the file is %q, want %q", before, b, since)
		}
	}
}
