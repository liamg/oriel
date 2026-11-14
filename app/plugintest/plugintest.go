// Package plugintest drives a plugin through the real app in a test: Oriel's built-ins with no window, drawn a frame
// at a time at a size, on a clock the test sets, with the plugin loaded from its directory under the grants it's
// given.
package plugintest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/plugin/ossandbox"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Timeout is how long Until, ClickText and CallTool wait.
var Timeout = 15 * time.Second

// Options is the app a test starts.
type Options struct {
	Size   render.Point      // the window, in points; 1600×1000 if zero
	Now    time.Time         // the clock's start; now, to the second, if zero
	Config func(*app.Config) // changes the app's config before it starts: Net, Secrets, Host…
}

// App is a running app, driven from the test's goroutine.
type App struct {
	*app.Headless
	t     testing.TB
	clock *clocktest.Fake
}

// New starts the app with a home, config and state directory of the test's, and stops it when the test ends.
func New(t testing.TB, o Options) *App {
	t.Helper()
	isolate.Test(t)
	if o.Size == (render.Point{}) {
		o.Size = render.Point{X: 1600, Y: 1000}
	}
	if o.Now.IsZero() {
		o.Now = time.Now().Truncate(time.Second)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := app.Config{Window: app.Window{ConfigDir: filepath.Join(home, ".config", "oriel"), StateDir: filepath.Join(home, "state"), Version: "dev"}}
	os.MkdirAll(cfg.ConfigDir, 0o755)
	if o.Config != nil {
		o.Config(&cfg)
	}
	a := &App{t: t, clock: clocktest.New(o.Now)}
	h, err := app.NewHeadless(cfg, app.HeadlessOpts{Size: o.Size, Clock: a.clock})
	if err != nil {
		t.Fatal(err)
	}
	a.Headless = h
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Log("stopping the app:", err)
		}
	})
	h.Frame()
	return a
}

// Now is the app's clock.
func (a *App) Now() time.Time { return a.clock.Now() }

// SetNow sets the app's clock.
func (a *App) SetNow(t time.Time) { a.clock.Set(t) }

// Main runs the package's tests: call it from TestMain. A plugin's process starts in the OS sandbox through the
// test binary, which Main lets it do.
func Main(m *testing.M) {
	ossandbox.Main()
	os.Exit(m.Run())
}

// Install lays out the plugin in src as an install does, in a directory of the test's, with each Go command in cmds
// (a package directory, relative to the test's working directory) built into its bin/. It skips the test where the
// OS sandbox a plugin's processes run in isn't there.
func Install(t testing.TB, src string, cmds ...string) string {
	t.Helper()
	return hosttest.Install(t, src, cmds...)
}

// Manifest is the oriel.toml in dir.
func Manifest(t testing.TB, dir string) plugin.Manifest {
	t.Helper()
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Plugin is a plugin a test loaded.
type Plugin struct {
	ID   string
	Dir  string
	Data string // its data directory
}

// Load starts the Lua plugin in dir granted grants (what its manifest declares if nil), with a data directory of
// the test's.
func (a *App) Load(dir string, grants []plugin.Grant) Plugin {
	a.t.Helper()
	m := Manifest(a.t, dir)
	p := Plugin{ID: m.ID, Dir: dir, Data: filepath.Join(a.t.TempDir(), "data")}
	if err := a.Activate(app.DirPlugin(dir, app.PluginOpts{Grants: grants, Data: p.Data})); err != nil {
		a.t.Fatal(err)
	}
	for _, n := range a.Store().State().Notifications {
		if n.ID == "plugin.load" && n.Title == "Plugin "+m.ID {
			a.t.Fatalf("loading %s: %s", m.ID, n.Text)
		}
	}
	a.Frame()
	return p
}

// Apply applies core events to the app's state, as the session host's would: sessions, focus.
func (a *App) Apply(evs ...core.Event) {
	a.t.Helper()
	for _, ev := range evs {
		if err := a.Store().Apply(ev); err != nil {
			a.t.Fatal(err)
		}
	}
	a.Frame()
}

// SetOption sets an option as settings.toml would.
func (a *App) SetOption(name string, v any) {
	a.t.Helper()
	if err := a.Registry().Options().SetFrom(name, registry.LayerUser, registry.At{}, v, "settings.toml"); err != nil {
		a.t.Fatal(err)
	}
}

// Run runs a command and waits until it has finished, its slow part off the loop (plugin.RunSlow) too.
func (a *App) Run(id string, args registry.Args) {
	a.t.Helper()
	var err error
	done := false
	a.Registry().Run(registry.OnDone(context.Background(), func(e error) { err, done = e, true }), id, args)
	if !done {
		a.Until(id+" to finish", func() bool { return done })
	}
	if err != nil {
		a.t.Fatal(err)
	}
	a.Frame()
}

// Until draws frames, running the work handed to the loop, until ok; it fails the test with the frame's text after
// Timeout.
func (a *App) Until(what string, ok func() bool) {
	a.t.Helper()
	if !a.Headless.Until(Timeout, ok) {
		a.t.Fatalf("waiting for %s:\n%s", what, a.Text())
	}
}

// UntilText waits for every one of texts in the frame.
func (a *App) UntilText(texts ...string) {
	a.t.Helper()
	a.Until(strings.Join(texts, ", "), func() bool { return a.Has(texts...) })
}

// Pump draws frames and runs the work handed to the loop for d: for a test that something doesn't happen.
func (a *App) Pump(d time.Duration) {
	a.Headless.Until(d, func() bool { return false })
}

// Has says whether the frame has every one of texts.
func (a *App) Has(texts ...string) bool {
	frame := a.Text()
	for _, s := range texts {
		if !strings.Contains(frame, s) {
			return false
		}
	}
	return true
}

// WantText fails the test for each of texts the frame lacks.
func (a *App) WantText(texts ...string) {
	a.t.Helper()
	frame := a.Text()
	for _, s := range texts {
		if !strings.Contains(frame, s) {
			a.t.Errorf("the frame lacks %q:\n%s", s, frame)
		}
	}
}

// Find is the first node in the accessibility tree named name, or nil.
func (a *App) Find(name string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n == nil {
			return nil
		}
		if n.Name == name {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(a.Access())
}

// ClickText clicks the middle of the run of text reading text painted last, on top of any other.
func (a *App) ClickText(text string) {
	a.t.Helper()
	p, ok := a.TextAt(text)
	if !ok {
		a.t.Fatalf("no %q in the frame:\n%s", text, a.Text())
	}
	a.Click(p, 0)
	a.Frame()
}

// Type presses each key (a character, or a name such as "CR", "Esc", "Tab") on what has focus.
func (a *App) Type(keys ...string) {
	a.t.Helper()
	for _, k := range keys {
		if !a.Key(k) {
			a.t.Fatalf("nothing focused takes %q", k)
		}
	}
}

// TypeIn clicks the field showing placeholder and types text into it, a character at a time.
func (a *App) TypeIn(text, placeholder string) {
	a.t.Helper()
	a.ClickText(placeholder)
	for _, r := range text {
		a.Type(string(r))
	}
}

// Tool is the agent tool named name offered to agent, with the parts other tools add to it.
func (a *App) Tool(agent, name string) registry.AgentTool {
	a.t.Helper()
	for _, tl := range registry.AgentToolsFor(a.Registry(), agent, true) {
		if tl.Name == name {
			return tl
		}
	}
	a.t.Fatalf("no tool %s for %s", name, agent)
	return registry.AgentTool{}
}

// CallTool calls tool as agent's MCP server does, off the loop, drawing frames until it answers. It answers the
// tool's JSON.
func (a *App) CallTool(agent string, tool registry.AgentTool, args map[string]any) (string, error) {
	a.t.Helper()
	return a.Headless.CallTool(registry.WithAgent(context.Background(), agent), Timeout, tool, args)
}
