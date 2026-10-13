package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui"
)

func bootWith(t *testing.T, cfg app.Config) *booted {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	be := headless.New()
	if cfg.Store == nil {
		cfg.Store = eventstore.New()
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = t.TempDir()
	}
	b := &booted{store: cfg.Store, done: make(chan error, 1)}
	go func() { b.done <- app.Run(ctx, be, cfg) }()
	select {
	case b.win = <-be.Windows():
	case err := <-b.done:
		t.Fatalf("app exited before opening a window: %v", err)
	case <-ctx.Done():
		t.Fatal("no window opened")
	}
	return b
}

func (b *booted) waitState(t *testing.T, what string, ok func(core.State) bool) core.State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := b.store.State(); ok(st) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state never had %s: %+v", what, b.store.State())
	return core.State{}
}

func findNotice(st core.State, prefix string) (core.Notification, bool) {
	for _, n := range st.Notifications {
		if strings.HasPrefix(n.ID, prefix) {
			return n, true
		}
	}
	return core.Notification{}, false
}

// buggy is a plugin whose command and view panic.
type buggy struct{ counter }

func (b *buggy) Manifest() ext.Manifest { return ext.Manifest{ID: "test.buggy"} }

func (b *buggy) Activate(ctx context.Context, h ext.Host) error {
	if err := b.counter.Activate(ctx, h); err != nil {
		return err
	}
	h.Command(registry.Command{ID: "buggy.boom", Run: func(registry.Ctx, registry.Args) error {
		var xs []int
		_ = xs[3]
		return nil
	}})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "b", Command: "buggy.boom"})
	h.View("buggy.view", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Fn: func(c *ui.Ctx) { panic("buggy view") }}
	}))
	return nil
}

// errors.md: "Every call into a plugin, hook, view, provider or command runs under recover. A panic becomes an
// error notice with the Go stack as its detail, the offending contribution is turned off as above, and the app
// keeps running."
func TestPanickingCommandAndViewTurnOff(t *testing.T) {
	b, _ := bootGrab(t, app.Config{Extensions: []ext.Extension{&buggy{counter{tab: true}}}},
		registry.Keymap{Keys: "V", Command: "view.open", Args: registry.Args{"id": "buggy.view"}},
		registry.Keymap{Keys: "C", Command: "view.open", Args: registry.Args{"id": "counter"}})
	b.waitText(t, "count 0")

	b.win.Send(render.KeyEvent{Key: "b"})
	st := b.waitState(t, "a notice for buggy.boom", func(st core.State) bool { _, ok := findNotice(st, "turned-off.command.buggy.boom"); return ok })
	n, _ := findNotice(st, "turned-off.command.buggy.boom")
	if n.Severity != core.SevError || n.Source != "test.buggy" || !strings.Contains(n.Detail, "errors_test.go") || !strings.Contains(n.Location, "errors_test.go:") {
		t.Errorf("notice = %+v", n)
	}
	if len(n.Actions) != 1 || n.Actions[0].Command != "contribution.turn_on" {
		t.Errorf("actions = %+v", n.Actions)
	}
	b.waitText(t, "is turned off")
	b.win.Send(render.KeyEvent{Key: "x"})
	b.waitText(t, "count 1")

	b.win.Send(render.KeyEvent{Key: "b"}) // off now: an error that it's off, and no second panic
	b.waitState(t, "a failed command", func(st core.State) bool { _, ok := findNotice(st, "command.failed.buggy.boom"); return ok })

	b.win.Send(render.KeyEvent{Key: "<C-w>"}) // nothing mapped; keys still flow
	b.win.Send(render.KeyEvent{Key: "V"})
	b.waitState(t, "a notice for buggy.view", func(st core.State) bool { _, ok := findNotice(st, "turned-off.view.buggy.view"); return ok })
	b.waitText(t, "view buggy.view is turned off")
	b.win.Send(render.KeyEvent{Key: "x"})
	b.win.Send(render.KeyEvent{Key: "C"})
	b.waitText(t, "count 2")
	b.close(t)
}

// grab hands the test its host, and maps keys to commands so the test runs them on the app's loop.
type grab struct {
	h    ext.Host
	keys map[string]registry.Keymap
}

func (g *grab) Manifest() ext.Manifest { return ext.Manifest{ID: "test.grab"} }
func (g *grab) Activate(_ context.Context, h ext.Host) error {
	g.h = h
	for k, km := range g.keys {
		km.Mode, km.Keys = "global", k
		h.Keymap(km)
	}
	return nil
}
func (g *grab) Deactivate(context.Context) error { return nil }

func bootGrab(t *testing.T, cfg app.Config, keys ...registry.Keymap) (*booted, ext.Host) {
	t.Helper()
	g := &grab{keys: map[string]registry.Keymap{}}
	for _, k := range keys {
		g.keys[k.Keys] = k
	}
	cfg.Extensions = append(cfg.Extensions, g)
	return bootWith(t, cfg), g.h
}

// errors.md: "The same ID repeating collapses into one entry with a count".
func TestRepeatedNoticeIsOneRowAndOneToast(t *testing.T) {
	b, h := bootGrab(t, app.Config{Extensions: []ext.Extension{&counter{}}})
	b.waitText(t, "count 0")
	for range 100 {
		h.Notify(core.Notice{ID: "lua.error", Severity: core.SevError, Title: "Config error", Text: "init.lua:15: attempt to index a nil value"})
	}
	st := b.store.State()
	if len(st.Notifications) != 1 || st.Notifications[0].Count != 100 {
		t.Fatalf("history = %+v", st.Notifications)
	}
	f := b.lastFrame(t, "×100")
	if n := strings.Count(f.Text(), "attempt to index a nil value"); n != 1 {
		t.Errorf("%d toasts, want 1:\n%s", n, f.Text())
	}
	b.close(t)
}

func (b *booted) lastFrame(t *testing.T, want string) headless.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return strings.Contains(f.Text(), want) })
	if err != nil {
		t.Fatalf("no frame shows %q; last frame:\n%s", want, f.Text())
	}
	return f
}

// errors.md: "Errors from a session you're not looking at don't toast: they put an error mark on that session's
// tab and wait in its log."
func TestErrorInUnfocusedSessionDoesntToast(t *testing.T) {
	b, h := bootGrab(t, app.Config{Extensions: []ext.Extension{&counter{}}})
	b.waitText(t, "count 0")
	for _, ev := range []core.Event{
		core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet},
		core.SessionOpened{ID: "s2", Kind: core.KindWork, Status: core.StatusQuiet},
		core.SessionFocused{ID: "s1"},
	} {
		if err := b.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	h.Notify(core.Notice{ID: "gopls.crashed", Severity: core.SevError, Session: "s2", Text: "gopls exited in s2"})
	h.Notify(core.Notice{ID: "here", Severity: core.SevError, Session: "s1", Text: "an error in s1"})
	b.lastFrame(t, "an error in s1")
	for _, f := range b.win.Frames() {
		if strings.Contains(f.Text(), "gopls exited in s2") {
			t.Fatal("an unfocused session's error toasted")
		}
	}
	if s, _ := b.store.Session("s2"); s.Unseen() != 1 {
		t.Errorf("s2 unseen = %d, want 1", s.Unseen())
	}
	b.close(t)
}

// A panic outside every contribution is Oriel's own bug: it writes a crash report and the next launch offers it.
func TestCrashReportOfferedOnNextLaunch(t *testing.T) {
	crashes := t.TempDir()
	cfg := app.Config{CrashDir: crashes, Version: "v0.4.2", Extensions: []ext.Extension{&counter{}}}
	cfg.Logs = app.NewLogs(app.LogOpts{})
	cfg.Logs.Redact.Add("lin_api_secret_value")
	b, h := bootGrab(t, cfg)
	b.waitText(t, "count 0")
	h.Log().Info("token lin_api_secret_value in use")
	h.View(app.StatusLineView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Paint{W: ui.Px(10), H: ui.Px(10), Fn: func(render.Frame, render.Rect) { panic("index out of range [12] with length 12") }}
	}))
	b.win.Send(render.KeyEvent{Key: "x"})
	var err error
	select {
	case err = <-b.done:
	case <-time.After(5 * time.Second):
		t.Fatal("app didn't stop after its own panic")
	}
	if err == nil || !strings.Contains(err.Error(), "oriel crashed") {
		t.Fatalf("Run = %v", err)
	}
	reports := app.PendingCrashes(crashes)
	if len(reports) != 1 {
		t.Fatalf("%d crash reports", len(reports))
	}
	r := reports[0].Report
	for _, want := range []string{"oriel v0.4.2", "panic: index out of range [12] with length 12", "errors_test.go", "token " + logs.Mask + " in use"} {
		if !strings.Contains(r, want) {
			t.Errorf("report lacks %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "lin_api_secret_value") {
		t.Error("the report holds the secret")
	}

	b2 := bootWith(t, app.Config{CrashDir: crashes})
	b2.waitText(t, "Oriel quit unexpectedly last time")
	b2.waitText(t, "Copy report")
	b2.close(t)
}

// errors.md: "Secrets never reach a log". Redacted in the panel, the file and the report.
func TestSecretsAreRedacted(t *testing.T) {
	dir := t.TempDir()
	cfg := app.Config{Extensions: []ext.Extension{&counter{tab: true}}}
	cfg.Logs = app.NewLogs(app.LogOpts{File: filepath.Join(dir, "oriel.log")})
	cfg.Logs.Redact.Add("ghp_supersecrettoken")
	b, h := bootGrab(t, cfg, registry.Keymap{Keys: "L", Command: "Log"}, registry.Keymap{Keys: "R", Command: "log.copy_report"})
	b.waitText(t, "count 0")
	h.Notify(core.Notice{ID: "push.failed", Severity: core.SevError, Text: "push to https://x:ghp_supersecrettoken@github.com failed",
		Detail: "GET https://api.github.com/user?access_token=abc123"})
	h.Log().Warn("fetched https://hooks.slack.com/services/x?token=xyz789")
	b.win.Send(render.KeyEvent{Key: "L"})
	f := b.lastFrame(t, "fetched https://hooks.slack.com")
	for _, secret := range []string{"ghp_supersecrettoken", "abc123", "xyz789"} {
		if strings.Contains(f.Text(), secret) {
			t.Errorf("the panel shows %q", secret)
		}
	}
	b.win.Send(render.KeyEvent{Key: "R"})
	report := b.waitClipboard(t)
	if !strings.Contains(report, "fetched https://hooks.slack.com/services/x?token="+logs.Mask) {
		t.Errorf("report:\n%s", report)
	}
	b.close(t)
	cfg.Logs.Close()
	file, err := os.ReadFile(filepath.Join(dir, "oriel.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{report, string(file)} {
		for _, secret := range []string{"ghp_supersecrettoken", "abc123", "xyz789"} {
			if strings.Contains(s, secret) {
				t.Errorf("%q leaked:\n%s", secret, s)
			}
		}
	}
	if !strings.Contains(string(file), "push to https://x:"+logs.Mask+"@github.com failed") {
		t.Errorf("log file:\n%s", file)
	}
}

func TestLogLevelCommand(t *testing.T) {
	cfg := app.Config{}
	cfg.Logs = app.NewLogs(app.LogOpts{})
	b, h := bootGrab(t, cfg)
	h.Log().Debug("hidden")
	if err := h.Run(context.Background(), "Log", registry.Args{"args": "level debug"}); err != nil {
		t.Fatal(err)
	}
	h.Log().Debug("shown")
	if err := h.Run(context.Background(), "Log", registry.Args{"level": "loud"}); err == nil {
		t.Error("set a level that doesn't exist")
	}
	var msgs []string
	for _, r := range cfg.Logs.Ring.Records() {
		msgs = append(msgs, r.Msg)
	}
	if strings.Join(msgs, ",") != "shown" {
		t.Errorf("records = %v", msgs)
	}
	b.close(t)
}

func (b *booted) waitClipboard(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c := b.win.Clipboard(); c != "" {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("nothing went on the clipboard")
	return ""
}
