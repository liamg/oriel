package shell_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry"
)

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

// populate is the errors fixture: two sessions with config in front, a log of each kind of record, a config error
// repeated three times and a gopls restart.
func populate(s *apptest.Scene) {
	for _, ev := range []core.Event{
		core.SessionOpened{ID: "config", Kind: core.KindWork, Status: core.StatusQuiet},
		core.SessionOpened{ID: "pricing-rounding", Kind: core.KindWork, Status: core.StatusWorking},
		core.SessionFocused{ID: "config"},
	} {
		if err := s.Store().Apply(ev); err != nil {
			s.T.Fatal(err)
		}
	}
	rec := func(t time.Time, sess core.SessionID, src, msg string) {
		s.Logs.Router.Write(logs.Record{Time: t, Level: 0, Session: sess, Source: src, Msg: msg})
	}
	rec(apptest.At(14, 2, 11), "pricing-rounding", "session", "worktree ready · rethink/rounding from origin/main @ 4e1c2a9")
	rec(apptest.At(14, 2, 12), "pricing-rounding", "gopls", "started · go 1.26 · 214 packages")
	for i := range 6 {
		s.Host().Notify(core.Notice{ID: "hook.budget", Severity: core.SevWarning, Source: "tickets.lua:8", Text: "4.1ms on CommentSend · over the 2ms budget",
			Time: apptest.At(14, 6, 30+2*i)})
	}
	for i := range 3 {
		s.Host().Notify(core.Notice{ID: "lua.error", Severity: core.SevError, Source: "init.lua", Title: "Config error",
			Text:     "init.lua:7: attempt to index a nil value (field 'args') · Permission hook turned off after 3 errors",
			Detail:   luaTrace,
			Location: "init.lua:7",
			Actions: []core.Action{
				{Title: "Open at line", Command: "editor.open", Args: map[string]any{"path": "init.lua", "line": 7}},
				{Title: "Turn hook back on", Command: "contribution.turn_on", Args: map[string]any{"kind": "hook", "id": "init.lua:6"}},
			},
			Time: apptest.At(14, 7, 1+i)})
	}
	s.Host().Notify(core.Notice{ID: "lsp.restarted", Severity: core.SevWarning, Source: "lsp", Session: "pricing-rounding", Title: "gopls restarted",
		Text: "exited (signal: killed) · restarted in 0.4s", Time: apptest.At(14, 7, 20)})
	rec(apptest.At(14, 7, 58), "pricing-rounding", "claude", "turn 12 done · 3 files changed · go test ./internal/pricing/ passed")
	s.Now = apptest.At(14, 7, 21)
}

// The error toast with its actions, a background warning toast, the ✕/⚠ counts, and the log panel
// in each scope, with the config error expanded to its trace and its highlighted source.
func TestErrorsSnapshots(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	populate(s)
	row := s.Store().State().Notifications[1]
	seq := row.Seq
	s.Run("log.open", registry.Args{"seq": seq})
	s.Golden("errors-all")
	text := s.Text()
	for _, want := range []string{"CONFIG ERROR", "GOPLS RESTARTED", "Open at line", "Turn hook back on", "TRACEBACK", "+4 Go frames",
		"~/.config/oriel/init.lua", "×3", "×6", "in hook Permission"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame lacks %q", want)
		}
	}
	s.Store().Apply(core.Dismissal(row))
	s.Logs.Router.Write(logs.Record{Time: apptest.At(14, 7, 10), Session: "config", Source: "session", Msg: "config session opened"})
	for _, sc := range []struct {
		name  string
		scope int
		has   []string
		lacks []string
	}{
		{"errors-oriel", shell.LogScopeOriel, []string{"tickets.lua:8", "init.lua"}, []string{"worktree ready"}},
		{"errors-session", shell.LogScopeSession, []string{"config session opened"}, []string{"worktree ready", "tickets.lua:8"}},
	} {
		shell.SetLogScope(s.Headless, sc.scope)
		shell.FoldLogRow(s.Headless)
		s.Golden(sc.name)
		text := s.Text()
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

// 38-crash: the report from last time, offered on launch.
func TestCrashSnapshot(t *testing.T) {
	dir := t.TempDir()
	c := shell.Crash{Time: apptest.At(18, 42, 0), Version: "v0.4.2", Panic: "runtime error: index out of range [12] with length 12",
		Report: "oriel v0.4.2 · darwin/arm64\nplugins linear, glowcursor\nsettings editor.clipboard=registers, ui.theme=graphite\n\n" +
			"panic: runtime error: index out of range [12] with length 12\n\ngoroutine 1 [running]:\n" +
			"github.com/liamg/oriel/ui/widget.(*VirtualList).Layout\n\tui/widget/list.go:214\n" +
			"github.com/liamg/oriel/app.(*Explorer).Layout\n\tapp/explorer.go:88\n… 14 more frames"}
	if _, err := shell.WriteCrash(dir, c); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{CrashDir: dir})
	s.Golden("crash")
	if text := s.Text(); !strings.Contains(text, "Oriel quit unexpectedly last time") || !strings.Contains(text, "Delete report") {
		t.Errorf("frame:\n%s", text)
	}
}

// Every interactive node has a help id.
func TestErrorViewsHaveHelp(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	populate(s)
	s.Run("log.open", registry.Args{"seq": s.Store().State().Notifications[1].Seq})
	s.Frame()
	s.Frame()
	if missing := s.UI().WithoutHelp(); len(missing) > 0 {
		t.Errorf("%d interactive nodes have no help id: %v", len(missing), missing)
	}
}

// An engine's edit.Notice becomes a core event, Notified.
func TestEditNoticeMapsOntoCore(t *testing.T) {
	n := kit.FromEdit(edit.Notice{ID: "vim.pattern-not-found", Severity: edit.Error, Text: "Pattern not found: foo"}, "vim")
	if n.ID != "vim.pattern-not-found" || n.Severity != core.SevError || n.Source != "vim" || !n.Key || n.Time.IsZero() {
		t.Errorf("notice = %+v", n)
	}
	q := kit.FromEdit(edit.Notice{ID: "vim.swap-range", Severity: edit.Question}, "vim")
	if q.Severity != core.SevQuestion || shell.ToastTTL(core.Notification{Notice: q}) != 0 {
		t.Errorf("a question should stay until answered: %+v", q)
	}
	info := kit.FromEdit(edit.Notice{ID: "vim.yanked", Severity: edit.Info}, "vim")
	if shell.ToastTTL(core.Notification{Notice: info}) != 3*time.Second {
		t.Error("an info toast should go after 3 s")
	}
}

// :Events is the live core event stream, filtered.
func TestEventsView(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	populate(s)
	s.Run("Events", nil)
	s.Frame()
	s.Frame()
	text := s.Text()
	for _, want := range []string{"SessionOpened", "SessionFocused", "Notified"} {
		if !strings.Contains(text, want) {
			t.Errorf(":Events lacks %s", want)
		}
	}
	s.TypeInto("Filter events", "focused")
	if text := s.Text(); strings.Contains(text, "SessionOpened") || !strings.Contains(text, "SessionFocused") {
		t.Errorf("filtered :Events:\n%s", text)
	}
}

// Clicking a row in the log expands it, and clicking it again folds it.
func TestLogRowClickExpands(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	populate(s)
	s.Run("log.open", nil)
	shell.FoldLogRow(s.Headless)
	s.Frame()
	s.Frame()
	row := s.Node(s.Store().State().Notifications[1].Text)
	s.Click(apptest.Centre(row.Bounds), 0)
	if shell.LogOpenRow(s.Headless) == "" {
		t.Fatal("a click on the row didn't expand it")
	}
	if !strings.Contains(s.Text(), "TRACEBACK") {
		t.Errorf("the expanded row shows no trace:\n%s", s.Text())
	}
	s.Click(apptest.Centre(row.Bounds), 0)
	if shell.LogOpenRow(s.Headless) != "" {
		t.Error("a second click didn't fold it")
	}
}
