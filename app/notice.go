package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// FromEdit is an engine's notice as a core one. Source is the engine: "vim", "plain".
func FromEdit(n edit.Notice, source string) core.Notice {
	sev := map[edit.Severity]core.Severity{edit.Info: core.SevInfo, edit.Warning: core.SevWarning, edit.Error: core.SevError,
		edit.Question: core.SevQuestion}[n.Severity]
	return core.Notice{ID: n.ID, Severity: sev, Source: source, Text: n.Text, Time: time.Now(), Key: true}
}

func redactNotice(n core.Notice, r *logs.Redactor) core.Notice {
	n.Text, n.Detail = r.Redact(n.Text), r.Redact(n.Detail)
	if n.Time.IsZero() {
		n.Time = time.Now()
	}
	return n
}

// faultNotice is the error notice for a contribution turned off: its stack as the detail, and an action to
// turn it back on.
func faultNotice(f registry.Fault, l *Logs) core.Notice {
	n := core.Notice{ID: "turned-off." + f.Kind + "." + f.ID, Severity: core.SevError, Source: f.Source,
		Text:    fmt.Sprintf("%s %s is turned off: %v", f.Kind, f.ID, f.Err),
		Actions: []core.Action{{Title: "Turn back on", Command: "contribution.turn_on", Args: map[string]any{"kind": f.Kind, "id": f.ID}}}}
	if n.Source == "" {
		n.Source = "core"
	}
	if p, ok := f.Err.(*registry.Panic); ok {
		n.Detail = string(p.Stack)
		if fr, ok := failingFrame(parseTrace(n.Detail)); ok {
			n.Location = fr.Loc()
		}
	}
	return redactNotice(n, l.Redact)
}

// commandNotice is the notice for a command that failed when its key was pressed.
func commandNotice(r registry.Registry, id string, err error) core.Notice {
	src := "core"
	for _, c := range r.Commands() {
		if c.ID == id && c.Source != "" {
			src = c.Source
		}
	}
	return core.Notice{ID: "command.failed." + id, Severity: core.SevError, Source: src, Text: err.Error(), Time: time.Now(), Key: true}
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

// Frame is one line of a stack trace: a Go stack's or a Lua traceback's.
type Frame struct {
	Fn   string
	File string
	Line int
	Go   bool // a Go frame, behind "show Go frames" in a Lua trace
}

func (f Frame) Loc() string {
	if f.Line == 0 {
		return f.File
	}
	return f.File + ":" + strconv.Itoa(f.Line)
}

// parseTrace reads the frames out of a Go stack (debug.Stack) or a Lua traceback.
func parseTrace(s string) []Frame {
	lines := strings.Split(s, "\n")
	var out []Frame
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, "goroutine ") || strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "stack traceback:"):
		case strings.HasPrefix(ln, "\t") && i > 0 && !strings.HasPrefix(lines[i-1], "\t") && isGoFn(lines[i-1]):
			// the file line under a Go function; read with it
		case !strings.HasPrefix(ln, "\t") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\t") && isGoFn(ln):
			file, line := splitLoc(strings.Fields(strings.TrimSpace(lines[i+1]))[0])
			out = append(out, Frame{Fn: ln, File: file, Line: line, Go: true})
		case strings.HasPrefix(ln, "\t"):
			loc, rest, _ := strings.Cut(strings.TrimSpace(ln), ": ")
			file, line := splitLoc(loc)
			out = append(out, Frame{Fn: rest, File: file, Line: line})
		}
	}
	return out
}

func isGoFn(s string) bool { return strings.HasSuffix(s, ")") && !strings.HasPrefix(s, "\t") }

func splitLoc(s string) (string, int) {
	if i := strings.LastIndexByte(s, ':'); i > 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			return s[:i], n
		}
	}
	return s, 0
}

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

// Desktop is what Oriel asks of the OS: the clipboard and the browser.
type Desktop interface {
	SetClipboard(text string)
	ReadClipboard() (string, bool)
	OpenURL(url string) error
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
