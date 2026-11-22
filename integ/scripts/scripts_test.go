package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/plugintest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

func TestMain(m *testing.M) { plugintest.Main(m) }

// scene is a work session in front on a workspace of two projects, with the plugin loaded and scripts set.
func scene(t *testing.T, name string, scripts map[string]string) (*plugintest.App, string) {
	t.Helper()
	ws := t.TempDir()
	for _, p := range []string{"api", "web"} {
		if err := os.MkdirAll(filepath.Join(ws, "repos", p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a := plugintest.New(t, plugintest.Options{})
	a.Apply(core.SessionSynced{Session: core.SessionState{ID: "s1", Name: name, Kind: core.KindWork, Dir: ws, Projects: []core.Project{
		{Name: "api", Dir: filepath.Join(ws, "repos", "api"), Primary: true}, {Name: "web", Dir: filepath.Join(ws, "repos", "web")}}}},
		core.SessionFocused{ID: "s1"})
	a.Load("plugin", nil)
	a.SetOption("plugin.scripts.list", scripts)
	return a, ws
}

// noticeWith waits for a notice holding text, and returns it.
func noticeWith(a *plugintest.App, text string) core.Notification {
	var got core.Notification
	a.Until("a notice with "+text, func() bool {
		for _, n := range a.Store().State().Notifications {
			if strings.Contains(n.Text, text) {
				got = n
				return true
			}
		}
		return false
	})
	return got
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A script for the whole session runs in the workspace with its slots filled in, each a single shell word however
// it's spelled; ${x} is the shell's. The notice shows the last lines it printed, stderr too.
func TestScriptForTheSession(t *testing.T) {
	a, ws := scene(t, "it's tilt", map[string]string{
		"Deploy": `printf '%s|%s|%s' {session} {session_name} "${HOME:+home}" > slots.txt
for i in 1 2 3 4 5 6; do echo line$i; done
echo warned >&2`,
	})
	a.Run("scripts.run", registry.Args{"name": "Deploy"})
	n := noticeWith(a, "Deploy")
	if got, want := read(t, filepath.Join(ws, "slots.txt")), ws+"|it's tilt|home"; got != want {
		t.Errorf("slots = %q, want %q", got, want)
	}
	if n.Severity != core.SevInfo || strings.Contains(n.Text, "line2") || !strings.Contains(n.Text, "line3") || !strings.Contains(n.Text, "warned") {
		t.Errorf("notice = %v %q", n.Severity, n.Text)
	}
}

// A script naming {repo} or {repo_name} runs in the project it's run for, and needs one.
func TestScriptForAProject(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Up": `printf '%s|%s' {repo_name} "$(pwd)" > {repo}/up.txt`})
	a.Run("scripts.run", registry.Args{"name": "Up", "project": "web"})
	noticeWith(a, "Up")
	dir := filepath.Join(ws, "repos", "web")
	real, _ := filepath.EvalSymlinks(dir)
	if got := read(t, filepath.Join(dir, "up.txt")); got != "web|"+dir && got != "web|"+real {
		t.Errorf("up.txt = %q", got)
	}

	a.Run("scripts.run", registry.Args{"name": "Up"})
	if n := noticeWith(a, "say which project"); n.Severity != core.SevError {
		t.Errorf("without a project: %+v", n)
	}
}

// A script starting with #! runs under the interpreter it names.
func TestScriptWithAShebang(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Who": "#!/bin/sh\nprintf '%s' \"$0\" > shebang.txt"})
	a.Run("scripts.run", registry.Args{"name": "Who"})
	noticeWith(a, "Who")
	if got := read(t, filepath.Join(ws, "shebang.txt")); got == "sh" || got == "bash" || got == "" {
		t.Errorf("$0 = %q: it ran as -c code, not as a file", got)
	}
}

// A script that fails is an error notice with why and its last lines.
func TestScriptThatFails(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Boom": "echo boom; exit 3"})
	a.Run("scripts.run", registry.Args{"name": "Boom"})
	n := noticeWith(a, "boom")
	if n.Severity != core.SevError || !strings.Contains(n.Text, "Boom failed") {
		t.Errorf("notice = %v %q", n.Severity, n.Text)
	}
}

// A slot the plugin doesn't know is an error, and nothing runs.
func TestUnknownSlot(t *testing.T) {
	a, ws := scene(t, "tilt", map[string]string{"Odd": "touch ran.txt {nope}"})
	a.Run("scripts.run", registry.Args{"name": "Odd"})
	if n := noticeWith(a, "{nope}"); n.Severity != core.SevError {
		t.Errorf("notice = %+v", n)
	}
	if _, err := os.Stat(filepath.Join(ws, "ran.txt")); err == nil {
		t.Error("it ran")
	}
}

// The picker lists each script once, and a project's script once for each project.
func TestScriptsPicker(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Deploy": "true", "Up": "cd {repo}"})
	a.Run("scripts.pick", nil)
	a.UntilText("Deploy", "Up · api", "Up · web")
}

// $ in the omnibox narrows it to the scripts.
func TestScriptsPrefix(t *testing.T) {
	a, _ := scene(t, "tilt", map[string]string{"Deploy": "true", "Up": "cd {repo}"})
	a.Run("omnibox.open", registry.Args{"query": "$"})
	a.UntilText("Deploy", "Up · api", "Up · web")
	if a.Has("Commands") {
		t.Errorf("$ isn't narrowed to scripts:\n%s", a.Text())
	}
}
