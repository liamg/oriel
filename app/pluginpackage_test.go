package app_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin/ossandbox"
	"github.com/liamg/oriel/registry"
)

// writePlugin lays out a plugin's files in a directory of the test's.
func writePlugin(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func loadFailed(s *apptest.Scene, id string) string {
	for _, n := range s.Store().State().Notifications {
		if n.ID == "plugin.load" && n.Title == "Plugin "+id {
			return n.Text
		}
	}
	return ""
}

func themeNamesOf(r registry.Registry) []string {
	ts, _ := r.Themes()
	var out []string
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

// A data-only theme plugin, a Lua plugin, and a Lua + process plugin each load from a directory and register their
// contributions. A data-only plugin (a theme, a grammar pack) is oriel.toml + assets/, with no init.lua, and
// [contributes] registers themes, prompts and help by declaration alone.
func TestDataOnlyPlugin(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	dir := writePlugin(t, map[string]string{
		"oriel.toml": `id = "acme/dusk"
version = "1.0.0"
[contributes]
themes  = ["assets/themes/*.toml"]
prompts = ["assets/prompts/*.md"]
help    = ["assets/help/*.md"]
`,
		"assets/themes/dusk.toml":  "name = \"dusk\"\nextends = \"oriel\"\n\n[dark.palette]\nbg0 = \"#10101c\"\n",
		"assets/prompts/review.md": "Review like a hawk.\n",
		"assets/help/dusk.md":      "# Dusk {#dusk}\n\nA darker theme.\n",
	})
	p := app.DirPlugin(dir, app.PluginOpts{})
	if err := s.Activate(p); err != nil {
		t.Fatal(err)
	}
	if msg := loadFailed(s, "acme/dusk"); msg != "" {
		t.Fatalf("loading: %s", msg)
	}
	if !slices.Contains(themeNamesOf(s.Registry()), "dusk") {
		t.Errorf("themes: %v, want dusk", themeNamesOf(s.Registry()))
	}
	var packs []string
	for _, c := range s.Registry().Contributions("agent.pack") {
		if pk := c.(registry.PromptPack); pk.Source == "acme/dusk" {
			packs = append(packs, pk.Name+": "+strings.TrimSpace(pk.Prompt))
		}
	}
	if !slices.Equal(packs, []string{"review: Review like a hawk."}) {
		t.Errorf("prompt packs: %q", packs)
	}
	hs, _ := s.Registry().HelpSources()
	found := false
	for _, h := range hs {
		if h.Source == "acme/dusk" {
			b, err := fs.ReadFile(h.FS, "dusk.md")
			found = err == nil && strings.Contains(string(b), "A darker theme.")
		}
	}
	if !found {
		t.Error("its help page isn't registered")
	}
	if err := p.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(themeNamesOf(s.Registry()), "dusk") {
		t.Error("the theme stays after the plugin stops")
	}
}

// A [contributes] pattern that matches nothing is a load error, not an empty plugin.
func TestContributesMatchingNothing(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	dir := writePlugin(t, map[string]string{"oriel.toml": "id = \"acme/typo\"\nversion = \"1.0.0\"\n[contributes]\nthemes = [\"asset/themes/*.toml\"]\n"})
	s.Activate(app.DirPlugin(dir, app.PluginOpts{}))
	if msg := loadFailed(s, "acme/typo"); !strings.Contains(msg, "matches nothing") {
		t.Errorf("load notice %q, want one saying the pattern matches nothing", msg)
	}
}

// A Lua plugin: its init.lua registers a command, and reads its own asset with oriel.asset, which needs no
// capability: it's the plugin's own directory.
func TestLuaPluginWithAssets(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	dir := writePlugin(t, map[string]string{
		"oriel.toml": "id = \"acme/greet\"\nversion = \"0.2.0\"\n",
		"init.lua": `local L = oriel
local hello = L.asset("greeting.txt")
L.command("Greet", function() L.notify(hello) end)
local ok, err = pcall(L.asset, "../oriel.toml")
L.command("Escape", function() L.notify(tostring(ok) .. " " .. tostring(err)) end)
`,
		"assets/greeting.txt": "Hello from an asset",
	})
	if err := s.Activate(app.DirPlugin(dir, app.PluginOpts{})); err != nil {
		t.Fatal(err)
	}
	if msg := loadFailed(s, "acme/greet"); msg != "" {
		t.Fatalf("loading: %s", msg)
	}
	runCommand(s, t, "Greet")
	runCommand(s, t, "Escape")
	var texts []string
	for _, n := range s.Store().State().Notifications {
		texts = append(texts, n.Title+n.Text)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Hello from an asset") {
		t.Errorf("notices %q, want the asset's text", texts)
	}
	if !strings.Contains(joined, "false") || !strings.Contains(joined, "isn't a path under assets/") {
		t.Errorf("notices %q, want reading outside assets/ refused", texts)
	}
}

func runCommand(s *apptest.Scene, t *testing.T, id string) {
	t.Helper()
	if err := s.Registry().Run(context.Background(), id, nil); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	s.Frame()
}

// A Lua + process plugin as oriel plugin init --process writes it: init.lua spawns its own binary, which needs no
// exec grant, and answers over JSON-RPC.
func TestLuaProcessPlugin(t *testing.T) {
	if err := ossandbox.Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	if _, err := catalog.Scaffold(dir, "acme/echo", true); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, "bin", "echo"), "./cmd/echo")
	build.Dir, build.Env = dir, append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the process: %v\n%s", err, out)
	}
	s := apptest.New(t, apptest.Options{})
	if err := s.Activate(app.DirPlugin(dir, app.PluginOpts{Data: t.TempDir()})); err != nil {
		t.Fatal(err)
	}
	if msg := loadFailed(s, "acme/echo"); msg != "" {
		t.Fatalf("loading: %s", msg)
	}
	runCommand(s, t, "Echo")
	s.UntilState("the process's answer", func(st core.State) bool {
		return slices.ContainsFunc(st.Notifications, func(n core.Notification) bool { return strings.Contains(n.Title+n.Text, "Hello, Oriel") })
	})
}

func noticeTexts(ns []core.Notification) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Title+": "+n.Text)
	}
	return out
}
