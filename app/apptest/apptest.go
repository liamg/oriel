// Package apptest drives the app in a test from outside package app: its built-ins with no window, drawn a frame at
// a time on a clock the test sets. A feature package's tests use it, driving the app through commands, keys and
// clicks, and reading what it drew.
package apptest

import (
	"cmp"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/agents"

	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/voice"
)

// MockupSize is the mockups' window, in points.
var MockupSize = render.Point{X: 1600, Y: 1000}

// Timeout is how long Run, Press and Until wait.
var Timeout = 15 * time.Second

// HookBudget is the hooks.budget a test's app starts with: looser than the real one, so a hook slowed by the tests
// running alongside isn't a warning in the frame. TestSlowHookWarns holds the real one.
const HookBudget = 10 * time.Millisecond

// testRegistry is the registry a test's app starts with, timing its work on c: a hook's time is the scene's clock's,
// not how busy the machine running the test is.
func testRegistry(c clock.Clock) uireg.Registry {
	r := uireg.NewWith(registry.Config{Clock: c})
	r.Option(registry.OptionSpec{Name: "hooks.budget", Type: registry.Duration, Default: HookBudget, Scopes: registry.ScopeUser,
		Source: "builtin:apptest", Help: "How long a hook may take, on two calls running, before it's a warning: hooks run on the hot path."})
	return r
}

// InitLua is the init.lua a scene's config starts with: a keymap, a mode's key and a Permission hook.
const InitLua = `local L = oriel
L.keymap.set({ "n", "x" }, "ga", function() L.thread.new() end)
L.keymap.set("requests", "x", "request.resolve")

-- auto-allow test runs in this workspace
L.on("Permission", function(ev)
  if ev.tool == "Bash" and ev.args:match("^go test ") then
    return "allow"
  end
end)
`

// Backends are the sandbox backends a scene's machine offers, whatever this one has.
func Backends() []string { return []string{"docker", "seatbelt"} }

// OfflineWhisper is dictation's engine with its models in a directory of the test's, downloaded offline.
func OfflineWhisper(t testing.TB) *voice.Whisper {
	w := voice.New(nil)
	w.Dir, w.Net = t.TempDir(), isolate.Net(t)
	return w
}

// Options are the app a scene starts.
type Options struct {
	CrashDir string
	WorkDir  string            // files are relative to it
	Size     render.Point      // MockupSize if zero
	Config   func(*app.Config) // changes the config before the app starts: the session host, say
	ImageNet *http.Client      // what remote images are fetched with; offline if nil
	FontDirs []string          // where its installed fonts are (ui.font); none if nil
	// ScriptedFiles gives the app file watchers whose events are only what the test says changed (FilesChanged,
	// WriteFiles), not the OS's: a count of reads isn't moved by the OS's late events for the test's own setup.
	ScriptedFiles bool
}

// Scene is the app's built-ins over a store, drawn the way Run draws the window, on a fixed clock.
type Scene struct {
	*app.Headless
	T         testing.TB
	Now       time.Time // the window's clock
	Desktop   *Desktop
	ConfigDir string    // the config directory: init.lua, settings.toml, themes/
	Logs      *app.Logs // the window's logs, its ring of records
	// OverviewOff hides the project overview, whose temporary paths would change every run; overview tests dispose
	// it.
	OverviewOff registry.Disposable
	midway      bool             // looks don't settle the app first (Midway)
	clock       *sceneClock      // the registry's: Now, and what's timed on it
	files       *fswatch.Scripts // the app's file watchers, with ScriptedFiles
}

// New starts a scene with a home and config of the test's, and stops it when the test ends.
func New(t testing.TB, o Options) *Scene {
	t.Helper()
	isolate.Test(t)
	if o.Size == (render.Point{}) {
		o.Size = MockupSize
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	conf := filepath.Join(home, ".config", "oriel")
	os.MkdirAll(conf, 0o755)
	os.WriteFile(filepath.Join(conf, "init.lua"), []byte(InitLua), 0o644)
	s := &Scene{T: t, Desktop: &Desktop{}, ConfigDir: conf}
	s.clock = &sceneClock{now: func() time.Time { return s.Now }}
	cfg := app.Config{
		Window: app.Window{Store: eventstore.New(), Registry: testRegistry(s.clock), Logs: app.NewLogs(app.LogOpts{}),
			ConfigDir: conf, CrashDir: o.CrashDir, Version: "v0.4.2", WorkDir: o.WorkDir, Desktop: s.Desktop, Net: isolate.Net(t),
			ImageNet: cmp.Or(o.ImageNet, isolate.Net(t))},
		LanguageServers: TestServers, SandboxBackends: Backends, Voice: app.VoiceConfig{Whisper: OfflineWhisper(t)},
	}
	if o.ScriptedFiles {
		s.files = &fswatch.Scripts{}
		cfg.FSWatch = s.files.New
	}
	if o.Config != nil {
		o.Config(&cfg)
	}
	s.Logs = cfg.Logs
	h, err := app.NewHeadless(cfg, app.HeadlessOpts{Size: o.Size, Clock: s.Clock(), FontDirs: o.FontDirs})
	if err != nil {
		t.Fatal(err)
	}
	s.Headless = h
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("closing the scene: %v", err)
		}
	})
	s.OverviewOff = h.Registry().View(app.OverviewView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.ColumnNode() }))
	return s
}

// PluginOf is the plugin started as id, as a T.
func PluginOf[T plugin.Plugin](s *Scene, id string) T {
	s.T.Helper()
	p, ok := s.Plugin(id)
	x, isT := p.(T)
	if !ok || !isT {
		s.T.Fatalf("no plugin %s of type %T", id, x)
	}
	return x
}

// Config is the app's config, as Options.Config changes it.
type Config = app.Config

// VoiceConfig swaps dictation's parts (app.VoiceConfig).
type VoiceConfig = app.VoiceConfig

// Connect is a config change that gives the app a session host, and its store; builtin are as for Builtin.
func Connect(w *host.Host, builtin ...agent.Provider) func(*Config) {
	return func(cfg *Config) {
		cfg.Host, cfg.Store = w, w.Store()
		Builtin(builtin...)(cfg)
	}
}

// Builtin is a config change that has the built-in providers' plugins (app/agents) register fakes in their place, by
// ID.
func Builtin(fakes ...agent.Provider) func(*Config) {
	return func(cfg *Config) {
		for _, p := range fakes {
			if pl, ok := agents.For(p); ok {
				cfg.Agents = append(cfg.Agents, pl)
			}
		}
	}
}

// HostScene is a scene with a session host of the fake provider and a fixture repo, its state in a directory of
// the test's; change changes the config after.
func HostScene(t *testing.T, change ...func(*Config)) (*Scene, *rig.Host) {
	t.Helper()
	h := rig.NewHost(t)
	state := t.TempDir()
	s := New(t, Options{Config: func(cfg *Config) {
		Connect(h.W)(cfg)
		cfg.StateDir = state
		for _, f := range change {
			f(cfg)
		}
	}})
	return s, h
}

// Day is the mockups' day, in UTC.
var Day = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
