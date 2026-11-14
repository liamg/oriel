package shell

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"

	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// redactNotice is n with secrets taken out of its text, stamped now if it has no time.
func redactNotice(n core.Notice, r *logs.Redactor, now time.Time) core.Notice {
	n.Text, n.Detail = r.Redact(n.Text), r.Redact(n.Detail)
	if n.Time.IsZero() {
		n.Time = now
	}
	return n
}

// faultNotice is the error notice for a contribution turned off: its stack or trace as the detail, and an action
// to turn it back on. One turned off for its errors in a row is the row its errors were, now saying so.
func faultNotice(f registry.Fault, l *Logs, dir string, now time.Time) core.Notice {
	n := core.Notice{ID: "turned-off." + f.Kind + "." + f.ID, Severity: core.SevError, Source: cmp.Or(f.Source, "core"),
		Text: fmt.Sprintf("%s %s is turned off: %v", f.Kind, f.ID, f.Err)}
	if f.Errors > 0 {
		n.ID, n.Title = "failed."+f.Kind+"."+f.ID, contribTitle(f.Kind)
		n.Text = fmt.Sprintf("%v · %s turned off after %d errors", f.Err, contribName(f.Kind, f.ID), f.Errors)
	}
	n.Actions = []core.Action{{Title: "Turn back on", Command: "contribution.turn_on", Args: map[string]any{"kind": f.Kind, "id": f.ID}}}
	if p, ok := f.Err.(*registry.Panic); ok {
		n.Detail = string(p.Stack)
		if fr, ok := failingFrame(script.ParseTrace(n.Detail)); ok {
			n.Location = fr.Loc()
		}
	}
	return redactNotice(script.WithTrace(n, f.Err, dir, "Open at line"), l.Redact, now)
}

// failureNotice is a contribution's error, or a warning about it, as a notice: one row for each contribution,
// counting its errors.
func failureNotice(f registry.Failure, l *Logs, dir string, now time.Time) core.Notice {
	n := core.Notice{ID: "failed." + f.Kind + "." + f.ID, Severity: core.SevError, Source: cmp.Or(f.Source, "core"),
		Title: contribTitle(f.Kind), Text: f.Err.Error()}
	if f.Warn {
		n.ID, n.Severity = "slow."+f.Kind+"."+f.ID, core.SevWarning
	}
	return redactNotice(script.WithTrace(n, f.Err, dir, "Open at line"), l.Redact, now)
}

func contribTitle(kind string) string {
	switch kind {
	case registry.KindHook:
		return "Hook error"
	case registry.KindView:
		return "View error"
	case registry.KindDecoration:
		return "Decoration error"
	}
	return "Error"
}

// contribName is a contribution as a sentence names it: "Permission hook", "app.statusline view".
func contribName(kind, id string) string {
	if kind == registry.KindHook {
		event, _, _ := strings.Cut(id, "#")
		return event + " hook"
	}
	return id + " " + kind
}

// commandNotice is the notice for a command that failed when its key was pressed.
func commandNotice(r registry.Registry, id string, err error, dir string, now time.Time) core.Notice {
	src := "core"
	for _, c := range r.Commands() {
		if c.ID == id && c.Source != "" {
			src = c.Source
		}
	}
	n := core.Notice{ID: "command.failed." + id, Severity: core.SevError, Source: src, Text: err.Error(), Time: now, Key: true}
	return script.WithTrace(n, err, dir, "Open at line")
}

// logNotice writes each notice to its log, Oriel's or its session's, as a record linked to its history row.
func logNotice(s core.Store, log *slog.Logger, ev core.Event) {
	n, ok := ev.(core.Notified)
	if !ok {
		return
	}
	var seq uint64
	for _, row := range slices.Backward(s.State().Notifications) {
		if row.ID == n.ID && row.Source == n.Source && row.Session == n.Session {
			seq = row.Seq
			break
		}
	}
	lv := map[core.Severity]slog.Level{core.SevWarning: slog.LevelWarn, core.SevError: slog.LevelError}[n.Severity]
	args := []any{logs.KeySource, n.Source, logs.KeyNotice, seq, "id", n.ID}
	if n.Session != "" {
		args = append(args, logs.KeySession, string(n.Session))
	}
	if n.Location != "" {
		args = append(args, "at", n.Location)
	}
	if n.Detail != "" {
		args = append(args, "detail", n.Detail)
	}
	log.Log(context.Background(), lv, n.Text, args...)
}

// Frame is one line of a stack trace.
type Frame = script.Frame

// failingFrame is where a Go panic happened: the frame under panic(), past the runtime's own.
func failingFrame(fs []Frame) (Frame, bool) {
	for i, f := range fs {
		if strings.HasPrefix(f.Fn, "panic(") {
			for _, g := range fs[i+1:] {
				if !strings.HasPrefix(g.Fn, "runtime.") {
					return g, true
				}
			}
		}
	}
	for _, f := range fs {
		if !f.Go {
			return f, true
		}
	}
	return Frame{}, false
}

// dropped is files dropped on the window: to the node under them that takes files, else opened in the editor.
func (sh *shell) dropped(ev render.DropEvent) {
	if u := sh.ui.Load(); u != nil && u.DropFiles(ev.Pos, ev.Paths) {
		return
	}
	for _, p := range ev.Paths {
		if err := sh.cfg.Registry.Run(context.Background(), "editor.open", registry.Args{"path": p}); err != nil {
			sh.store.Apply(core.Notified{Notice: core.Notice{ID: "drop.open", Severity: core.SevWarning, Source: "core", Text: err.Error(), Time: sh.now()}})
		}
	}
}

// Desktop is what Oriel asks of the OS: the clipboard, the browser and the file manager.
type Desktop interface {
	SetClipboard(text string)
	ReadClipboard() (string, bool)
	OpenURL(url string) error
	Reveal(path string) error // shows path in the file manager: selected in Finder, its folder elsewhere
}

// desktopPlugin is the OS's side of the IDE as commands: system.open opens a file or URL in its default app, system.copy
// copies text.
type desktopPlugin struct {
	kit.Disposer
	desktop *Desktop // the window's, once it's open
}

func (x *desktopPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "desktop"} }

func (x *desktopPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Command(registry.Command{ID: "system.open", Title: "Open a file {path} (relative to the work dir) or a {url} in the OS's default app for it",
		Run: func(_ registry.Ctx, a registry.Args) error {
			target := cmp.Or(a.String("url"), a.String("path"))
			if target == "" {
				return errors.New("system.open needs a path or a url")
			}
			if a.String("url") == "" && !filepath.IsAbs(target) {
				target = filepath.Join(h.Here().Project, target)
			}
			if *x.desktop == nil {
				return errors.New("there's no desktop to open it on")
			}
			return (*x.desktop).OpenURL(target)
		}}))
	x.Add(h.Command(registry.Command{ID: "system.reveal", Title: "Show a file or folder {path} in the file manager: Finder on macOS, its folder elsewhere",
		Run: func(_ registry.Ctx, a registry.Args) error {
			p := a.String("path")
			if p == "" {
				return errors.New("system.reveal needs a path")
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(h.Here().Project, p)
			}
			if *x.desktop == nil {
				return errors.New("there's no file manager to show it in")
			}
			return (*x.desktop).Reveal(p)
		}}))
	x.Add(h.Command(registry.Command{ID: "system.copy", Title: "Copy {text} to the system clipboard",
		Run: func(_ registry.Ctx, a registry.Args) error {
			if *x.desktop == nil {
				return errors.New("there's no clipboard to copy to")
			}
			(*x.desktop).SetClipboard(a.String("text"))
			return nil
		}}))
	return nil
}

type osDesktop struct{ clip render.Clipboard }

func windowDesktop(w render.Window) Desktop {
	c, _ := w.(render.Clipboard)
	return osDesktop{c}
}

func (d osDesktop) SetClipboard(text string) {
	if d.clip != nil {
		d.clip.SetClipboard(text)
	}
}

func (d osDesktop) ReadClipboard() (string, bool) {
	if d.clip == nil {
		return "", false
	}
	return d.clip.ReadClipboard()
}

func (d osDesktop) ReadClipboardFiles() []string {
	if f, ok := d.clip.(render.ClipboardFiles); ok {
		return f.ReadClipboardFiles()
	}
	return nil
}

func (d osDesktop) ReadClipboardImage() ([]byte, bool) {
	if f, ok := d.clip.(render.ClipboardFiles); ok {
		return f.ReadClipboardImage()
	}
	return nil, false
}

func (d osDesktop) Reveal(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		path = filepath.Dir(path)
	}
	return d.OpenURL(path)
}

func (d osDesktop) OpenURL(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, url).Start()
}

// eventTail keeps the last events applied, for :Events and crash reports.
type eventTail struct {
	mu   sync.Mutex
	size int
	evs  []tailEvent
	seq  uint64
}

type tailEvent struct {
	Seq   uint64
	Type  string
	Scope core.SessionID
	JSON  string // filled in by list: most events kept are never looked at
	At    time.Time
	ev    core.Event
}

func newEventTail(size int) *eventTail { return &eventTail{size: size} }

func (t *eventTail) add(ev core.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	t.evs = append(t.evs, tailEvent{Seq: t.seq, Type: core.EventName(ev), Scope: ev.Scope(), At: time.Now(), ev: ev})
	if len(t.evs) > 2*t.size { // let go of the oldest a batch at a time, not one by one
		t.evs = slices.Clone(t.evs[len(t.evs)-t.size:])
	}
}

func (t *eventTail) list() []tailEvent {
	t.mu.Lock()
	evs := slices.Clone(t.evs[max(0, len(t.evs)-t.size):])
	t.mu.Unlock()
	for i := range evs {
		b, _ := json.Marshal(evs[i].ev)
		evs[i].JSON, evs[i].ev = string(b), nil
	}
	return evs
}

// desktopClipboard is the system clipboard as the editor's registers see it. Platforms without a primary
// selection use the clipboard for "* too.
type desktopClipboard struct{ d *Desktop }

func (c desktopClipboard) Read(bool) (string, bool) {
	if *c.d == nil {
		return "", false
	}
	return (*c.d).ReadClipboard()
}

func (c desktopClipboard) Write(_ bool, s string) {
	if *c.d != nil {
		(*c.d).SetClipboard(s)
	}
}
