package shell

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"

	"github.com/liamg/oriel/core/logs"
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
	n := core.Notice{ID: "command.failed." + id, Severity: core.SevError, Source: src, Text: err.Error(), Time: now, AutoClear: true}
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
