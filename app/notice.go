package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// now is the clock notices are stamped with.
var now = time.Now

// FromEdit is an engine's notice as a core one. Source is the engine: "vim", "plain".
func FromEdit(n edit.Notice, source string) core.Notice {
	sev := map[edit.Severity]core.Severity{edit.Info: core.SevInfo, edit.Warning: core.SevWarning, edit.Error: core.SevError,
		edit.Question: core.SevQuestion}[n.Severity]
	return core.Notice{ID: n.ID, Severity: sev, Source: source, Text: n.Text, Time: now(), Key: true}
}

func redactNotice(n core.Notice, r *logs.Redactor) core.Notice {
	n.Text, n.Detail = r.Redact(n.Text), r.Redact(n.Detail)
	if n.Time.IsZero() {
		n.Time = now()
	}
	return n
}

// faultNotice is the error notice for a contribution turned off: its stack or trace as the detail, and an action
// to turn it back on. One turned off for its errors in a row is the row its errors were, now saying so.
func faultNotice(f registry.Fault, l *Logs, dir string) core.Notice {
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
	return redactNotice(withTrace(n, f.Err, dir, "Open at line"), l.Redact)
}

// failureNotice is a contribution's error, or a warning about it, as a notice: one row for each contribution,
// counting its errors.
func failureNotice(f registry.Failure, l *Logs, dir string) core.Notice {
	n := core.Notice{ID: "failed." + f.Kind + "." + f.ID, Severity: core.SevError, Source: cmp.Or(f.Source, "core"),
		Title: contribTitle(f.Kind), Text: f.Err.Error()}
	if f.Warn {
		n.ID, n.Severity = "slow."+f.Kind+"."+f.ID, core.SevWarning
	}
	return redactNotice(withTrace(n, f.Err, dir, "Open at line"), l.Redact)
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

// traced is an error that knows where it happened and how it got there: a Lua error.
type traced interface {
	Where() string
	Trace() string
}

// withTrace gives a notice for err its location and trace, and an action titled title that opens the line. A
// relative location is a config file's.
func withTrace(n core.Notice, err error, dir, title string) core.Notice {
	var t traced
	if !errors.As(err, &t) {
		return n
	}
	n.Detail, n.Location = t.Trace(), t.Where()
	if file, line := script.SplitLoc(n.Location); line > 0 {
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		n.Actions = append([]core.Action{{Title: title, Command: "editor.open", Args: map[string]any{"path": file, "line": line}}}, n.Actions...)
	}
	return n
}

// commandNotice is the notice for a command that failed when its key was pressed.
func commandNotice(r registry.Registry, id string, err error, dir string) core.Notice {
	src := "core"
	for _, c := range r.Commands() {
		if c.ID == id && c.Source != "" {
			src = c.Source
		}
	}
	n := core.Notice{ID: "command.failed." + id, Severity: core.SevError, Source: src, Text: err.Error(), Time: now(), Key: true}
	return withTrace(n, err, dir, "Open at line")
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
	if sh.ui != nil && sh.ui.DropFiles(ev.Pos, ev.Paths) {
		return
	}
	for _, p := range ev.Paths {
		if err := sh.cfg.Registry.Run(context.Background(), "editor.open", registry.Args{"path": p}); err != nil {
			sh.cfg.Store.Apply(core.Notified{Notice: core.Notice{ID: "drop.open", Severity: core.SevWarning, Source: "core", Text: err.Error(), Time: now()}})
		}
	}
}

// Desktop is what Oriel asks of the OS: the clipboard and the browser.
type Desktop interface {
	SetClipboard(text string)
	ReadClipboard() (string, bool)
	OpenURL(url string) error
}

// desktopPlugin is the OS's side of the IDE as commands: system.open opens a file or URL in its default app.
type desktopPlugin struct {
	disposer
	sh *shell
}

func (x *desktopPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "desktop"} }

func (x *desktopPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.add(h.Command(registry.Command{ID: "system.open", Title: "Open a file {path} (relative to the work dir) or a {url} in the OS's default app for it",
		Run: func(_ registry.Ctx, a registry.Args) error {
			target := cmpOr(a.String("url"), a.String("path"))
			if target == "" {
				return errors.New("system.open needs a path or a url")
			}
			if a.String("url") == "" && !filepath.IsAbs(target) {
				target = filepath.Join(x.sh.cfg.WorkDir, target)
			}
			if x.sh.desktop == nil {
				return errors.New("there's no desktop to open it on")
			}
			return x.sh.desktop.OpenURL(target)
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
	JSON  string
}

func newEventTail(size int) *eventTail { return &eventTail{size: size} }

func (t *eventTail) add(ev core.Event) {
	b, _ := json.Marshal(ev)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	t.evs = append(t.evs, tailEvent{Seq: t.seq, Type: core.EventName(ev), Scope: ev.Scope(), JSON: string(b)})
	if len(t.evs) > t.size {
		t.evs = slices.Delete(t.evs, 0, len(t.evs)-t.size)
	}
}

func (t *eventTail) list() []tailEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.evs)
}
