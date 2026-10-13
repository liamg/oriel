package app

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var mockupSize = render.Point{X: 1600, Y: 1000}

type clipboard struct{ text string }

func (c *clipboard) SetClipboard(s string)         { c.text = s }
func (c *clipboard) ReadClipboard() (string, bool) { return c.text, c.text != "" }
func (c *clipboard) OpenURL(u string) error        { return nil }

// errScene is the app's built-ins over a store, drawn the way Run draws the window, on a fixed clock.
type errScene struct {
	t     testing.TB
	sh    *shell
	store core.Store
	reg   registry.Registry
	host  ext.Host
	ui    *ui.UI
	now   time.Time
	list  *render.List
	clip  *clipboard
	exts  []ext.Extension
	size  render.Point
	// overviewOff hides the project overview, whose temporary paths would change every run; overview tests dispose it.
	overviewOff registry.Disposable
}

func newErrScene(t *testing.T, crashDir string) *errScene {
	return newScene(t, crashDir, "", mockupSize)
}

// newScene is the built-ins drawn at size, with files relative to workDir.
func newScene(t testing.TB, crashDir, workDir string, size render.Point) *errScene {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	conf := filepath.Join(home, ".config", "oriel")
	os.MkdirAll(conf, 0o755)
	os.WriteFile(filepath.Join(conf, "init.lua"), []byte(initLua), 0o644)

	s := &errScene{t: t, store: eventstore.New(), reg: registry.New(), clip: &clipboard{}, size: size}
	cfg := Config{Store: s.store, Registry: s.reg, Logs: NewLogs(LogOpts{}), ConfigDir: conf, CrashDir: crashDir, Version: "v0.4.2",
		WorkDir: workDir}
	s.sh = &shell{cfg: cfg, events: newEventTail(500), desktop: s.clip, poke: func() {}}
	s.store.Subscribe(core.Filter{}, s.sh.events.add)
	s.store.Subscribe(core.Filter{}, func(ev core.Event) { logNotice(s.store, cfg.Logs.Logger("core"), ev) })
	s.exts = builtins(s.sh)
	for _, e := range s.exts {
		h := s.sh.host("builtin:" + e.Manifest().ID)
		if err := e.Activate(context.Background(), h); err != nil {
			t.Fatal(err)
		}
		s.host = h
	}
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	s.ui = ui.New(ts, textStyle)
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	r, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		t.Fatal(err)
	}
	s.ui.SetTheme(r)
	o := s.ui.Options()
	o.ReducedMotion = true // scenes are of the settled UI on a stopped clock
	s.ui.SetOptions(o)
	s.ui.SetClock(func() time.Time { return s.now })
	s.overviewOff = s.reg.View(OverviewView, registry.ViewFunc(func(registry.ViewCtx) ui.Node { return ui.ColumnNode() }))
	s.sh.ui = s.ui
	s.sh.theme = s.ui.Theme
	s.sh.shot = func(th *theme.Resolved) *image.RGBA {
		if th == nil {
			th = s.ui.Theme()
		}
		return shotOf(s.ui, s.reg, s.size, th)
	}
	return s
}

func (s *errScene) frame() {
	s.list = new(render.List)
	s.ui.Frame(s.list.Record(1), s.size, func(c *ui.Ctx) { buildWindow(c, s.reg, s.size) })
}

func (s *errScene) run(id string, args registry.Args) {
	s.t.Helper()
	if err := s.reg.Run(context.Background(), id, args); err != nil {
		s.t.Fatal(err)
	}
}

func (s *errScene) golden(name string) {
	s.t.Helper()
	s.frame()
	s.frame()
	snapshot.Golden(s.t, "testdata/"+name+".list", []byte(snapshot.List(s.list)))
	img := raster.Render(s.list, s.size, 1, s.ui.Theme().C(theme.BgBase))
	snapshot.GoldenPNG(s.t, "testdata/"+name+".png", img, 0.02, 0.001)
}

const initLua = `local L = oriel
L.keymap.set({ "n", "x" }, "ga", function() L.thread.new() end)
L.keymap.set("requests", "x", "thread.resolve")

-- auto-allow test runs in this workspace
L.on("Permission", function(ev)
  if ev.tool == "Bash" and ev.args:match("^go test ") then
    return "allow"
  end
end)
`

const luaTrace = `stack traceback:
	init.lua:7: in hook Permission
	oriel.on: dispatch · Permission (Bash)
	init.lua:6: in main chunk
github.com/liamg/oriel/script.(*VM).call(...)
	/src/oriel/script/vm.go:88 +0x1c
github.com/liamg/oriel/script.(*VM).Hook(...)
	/src/oriel/script/hook.go:41 +0x9a
github.com/liamg/oriel/registry.Call(...)
	/src/oriel/registry/guard.go:35 +0x51
github.com/liamg/oriel/app.(*loop).run(...)
	/src/oriel/app/app.go:297 +0x2d2
`

var day = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

func at(h, m, s int) time.Time {
	return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second)
}

// the 37-errors fixture: two sessions with config in front, the log mockup 37 shows, a config error repeated
// three times and a gopls restart.
func (s *errScene) populate() {
	for _, ev := range []core.Event{
		core.SessionOpened{ID: "config", Kind: core.KindWork, Status: core.StatusQuiet},
		core.SessionOpened{ID: "pricing-rounding", Kind: core.KindWork, Status: core.StatusWorking},
		core.SessionFocused{ID: "config"},
	} {
		if err := s.store.Apply(ev); err != nil {
			s.t.Fatal(err)
		}
	}
	rec := func(t time.Time, sess core.SessionID, src, msg string) {
		s.sh.cfg.Logs.Router.Write(logs.Record{Time: t, Level: 0, Session: sess, Source: src, Msg: msg})
	}
	rec(at(14, 2, 11), "pricing-rounding", "session", "worktree ready · rethink/rounding from origin/main @ 4e1c2a9")
	rec(at(14, 2, 12), "pricing-rounding", "gopls", "started · go 1.26 · 214 packages")
	for i := range 6 {
		s.host.Notify(core.Notice{ID: "hook.budget", Severity: core.SevWarning, Source: "tickets.lua:8", Text: "4.1ms on CommentSend · over the 2ms budget",
			Time: at(14, 6, 30+2*i)})
	}
	for i := range 3 {
		s.host.Notify(core.Notice{ID: "lua.error", Severity: core.SevError, Source: "init.lua", Title: "Config error",
			Text:     "init.lua:7: attempt to index a nil value (field 'args') · Permission hook turned off after 3 errors",
			Detail:   luaTrace,
			Location: "init.lua:7",
			Actions: []core.Action{
				{Title: "Open at line", Command: "editor.open", Args: map[string]any{"path": "init.lua", "line": 7}},
				{Title: "Turn hook back on", Command: "contribution.turn_on", Args: map[string]any{"kind": "hook", "id": "init.lua:6"}},
			},
			Time: at(14, 7, 1+i)})
	}
	s.host.Notify(core.Notice{ID: "lsp.restarted", Severity: core.SevWarning, Source: "lsp", Session: "pricing-rounding", Title: "gopls restarted",
		Text: "exited (signal: killed) · restarted in 0.4s", Time: at(14, 7, 20)})
	rec(at(14, 7, 58), "pricing-rounding", "claude", "turn 12 done · 3 files changed · go test ./internal/pricing/ passed")
	s.now = at(14, 7, 21)
}

// 37-errors: the error toast with its actions, a background warning toast, the ✕/⚠ counts, and the log panel
// in each scope, with the config error expanded to its trace and its highlighted source.
func TestErrorsSnapshots(t *testing.T) {
	s := newErrScene(t, "")
	s.populate()
	seq := s.store.State().Notifications[1].Seq
	s.run("log.open", registry.Args{"seq": seq})
	s.golden("errors-all")
	text := s.frameText()
	for _, want := range []string{"CONFIG ERROR", "GOPLS RESTARTED", "Open at line", "Turn hook back on", "TRACEBACK", "+4 Go frames",
		"~/.config/oriel/init.lua", "×3", "×6", "in hook Permission"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame lacks %q", want)
		}
	}
	s.sh.cfg.Store.Apply(core.NoticeDismissed{Seq: seq})
	s.sh.cfg.Logs.Router.Write(logs.Record{Time: at(14, 7, 10), Session: "config", Source: "session", Msg: "config session opened"})
	for _, sc := range []struct {
		name  string
		scope int
		has   []string
		lacks []string
	}{
		{"errors-oriel", scopeOriel, []string{"tickets.lua:8", "init.lua"}, []string{"worktree ready"}},
		{"errors-session", scopeSession, []string{"config session opened"}, []string{"worktree ready", "tickets.lua:8"}},
	} {
		panel(s).scope = sc.scope
		panel(s).open = ""
		s.golden(sc.name)
		text := s.frameText()
		for _, w := range sc.has {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q", sc.name, w)
			}
		}
		for _, w := range sc.lacks {
			if strings.Contains(text, w) {
				t.Errorf("%s shows %q", sc.name, w)
			}
		}
	}
}

func panel(s *errScene) *logPanel {
	for _, e := range s.exts {
		if n, ok := e.(*notices); ok {
			return n.panel
		}
	}
	return nil
}

func (s *errScene) frameText() string {
	var lines []string
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs {
			lines = append(lines, op.Run.Text)
		}
	}
	return strings.Join(lines, "\n")
}

// 38-crash: the report from last time, offered on launch.
func TestCrashSnapshot(t *testing.T) {
	dir := t.TempDir()
	c := Crash{Time: at(18, 42, 0), Version: "v0.4.2", Panic: "runtime error: index out of range [12] with length 12",
		Report: "oriel v0.4.2 · darwin/arm64\nplugins linear, glowcursor\nsettings editor.clipboard=registers, ui.theme=graphite\n\n" +
			"panic: runtime error: index out of range [12] with length 12\n\ngoroutine 1 [running]:\n" +
			"github.com/liamg/oriel/ui/widget.(*VirtualList).Layout\n\tui/widget/list.go:214\n" +
			"github.com/liamg/oriel/app.(*Explorer).Layout\n\tapp/explorer.go:88\n… 14 more frames"}
	if _, err := WriteCrash(dir, c); err != nil {
		t.Fatal(err)
	}
	s := newErrScene(t, dir)
	s.golden("crash")
	if text := s.frameText(); !strings.Contains(text, "Oriel quit unexpectedly last time") || !strings.Contains(text, "Delete report") {
		t.Errorf("frame:\n%s", text)
	}
}

// help.md: every interactive node has a help id.
func TestErrorViewsHaveHelp(t *testing.T) {
	s := newErrScene(t, "")
	s.populate()
	s.run("log.open", registry.Args{"seq": s.store.State().Notifications[1].Seq})
	s.frame()
	s.frame()
	if missing := s.ui.WithoutHelp(); len(missing) > 0 {
		t.Errorf("%d interactive nodes have no help id: %v", len(missing), missing)
	}
}

// errors.md: "an engine's edit.Notice … Each becomes a core event, Notified".
func TestEditNoticeMapsOntoCore(t *testing.T) {
	n := FromEdit(edit.Notice{ID: "vim.pattern-not-found", Severity: edit.Error, Text: "Pattern not found: foo"}, "vim")
	if n.ID != "vim.pattern-not-found" || n.Severity != core.SevError || n.Source != "vim" || !n.Key || n.Time.IsZero() {
		t.Errorf("notice = %+v", n)
	}
	q := FromEdit(edit.Notice{ID: "vim.swap-range", Severity: edit.Question}, "vim")
	if q.Severity != core.SevQuestion || toastTTL(core.Notification{Notice: q}) != 0 {
		t.Errorf("a question should stay until answered: %+v", q)
	}
	info := FromEdit(edit.Notice{ID: "vim.yanked", Severity: edit.Info}, "vim")
	if toastTTL(core.Notification{Notice: info}) != 3*time.Second {
		t.Error("an info toast should go after 3 s")
	}
}

// :Events is the live core event stream, filtered.
func TestEventsView(t *testing.T) {
	s := newErrScene(t, "")
	s.populate()
	s.run("Events", nil)
	s.frame()
	s.frame()
	text := s.frameText()
	for _, want := range []string{"SessionOpened", "SessionFocused", "Notified"} {
		if !strings.Contains(text, want) {
			t.Errorf(":Events lacks %s", want)
		}
	}
	for _, e := range s.exts {
		if n, ok := e.(*notices); ok {
			n.events = "focused"
		}
	}
	s.frame()
	if text := s.frameText(); strings.Contains(text, "SessionOpened") || !strings.Contains(text, "SessionFocused") {
		t.Errorf("filtered :Events:\n%s", text)
	}
}
