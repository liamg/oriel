package logs

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
)

// Record is one log line, flattened and already redacted.
type Record struct {
	Time    time.Time      `json:"time"`
	Level   slog.Level     `json:"level"`
	Source  string         `json:"source"` // the "source" attr: "core", "lsp:gopls", "plugin:linear"
	Session core.SessionID `json:"session,omitempty"`
	Msg     string         `json:"msg"`
	Attrs   []Attr         `json:"attrs,omitempty"`
	Notice  uint64         `json:"notice,omitempty"` // the notification it records, if any
}

type Attr struct {
	Key   string `json:"k"`
	Value string `json:"v"`
}

// Text is the record as one plain line.
func (r Record) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %-5s", r.Time.Format(time.RFC3339), r.Level)
	if r.Session != "" {
		fmt.Fprintf(&b, " %s ›", r.Session)
	}
	if r.Source != "" {
		fmt.Fprintf(&b, " %s", r.Source)
	}
	fmt.Fprintf(&b, " %s", r.Msg)
	for _, a := range r.Attrs {
		fmt.Fprintf(&b, " %s=%s", a.Key, quote(a.Value))
	}
	return b.String()
}

func quote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n\"=") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// Sink takes records.
type Sink interface {
	Write(Record)
}

// SinkFunc is a func as a Sink.
type SinkFunc func(Record)

func (f SinkFunc) Write(r Record) { f(r) }

// Keys of attrs a Handler lifts into a Record's own fields.
const (
	KeySource  = "source"
	KeySession = "session"
	KeyNotice  = "notice"
)

// Handler is a slog.Handler that redacts each record and hands it to a sink.
type Handler struct {
	sink   Sink
	level  slog.Leveler
	red    *Redactor
	attrs  []Attr
	group  string
	source string
	sess   core.SessionID
	notice uint64
}

// NewHandler makes a handler writing to sink records at level and above, redacted by red.
func NewHandler(sink Sink, level slog.Leveler, red *Redactor) *Handler {
	if level == nil {
		level = slog.LevelInfo
	}
	return &Handler{sink: sink, level: level, red: red}
}

func (h *Handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	c := *h
	c.attrs = slices.Clone(h.attrs)
	r.Attrs(func(a slog.Attr) bool { c.add(h.group, a); return true })
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	h.sink.Write(Record{Time: t, Level: r.Level, Source: c.source, Session: c.sess, Msg: h.red.Redact(r.Message),
		Attrs: c.attrs, Notice: c.notice})
	return nil
}

func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = slices.Clone(h.attrs)
	for _, a := range as {
		c.add(h.group, a)
	}
	return &c
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.group = h.group + name + "."
	return &c
}

func (h *Handler) add(group string, a slog.Attr) {
	v := a.Value.Resolve()
	if a.Key == "" && v.Kind() != slog.KindGroup {
		return
	}
	if v.Kind() == slog.KindGroup {
		g := group
		if a.Key != "" {
			g += a.Key + "."
		}
		for _, ga := range v.Group() {
			h.add(g, ga)
		}
		return
	}
	if group == "" {
		switch a.Key {
		case KeySource:
			h.source = v.String()
			return
		case KeySession:
			h.sess = core.SessionID(v.String())
			return
		case KeyNotice:
			if v.Kind() == slog.KindUint64 {
				h.notice = v.Uint64()
				return
			}
		}
	}
	h.attrs = append(h.attrs, Attr{Key: group + a.Key, Value: h.red.Redact(v.String())})
}

// Ring keeps the last records in memory, for the log panel and for reports.
type Ring struct {
	mu   sync.Mutex
	buf  []Record
	next int
	full bool
	subs []func()
}

func NewRing(size int) *Ring { return &Ring{buf: make([]Record, size)} }

func (r *Ring) Write(rec Record) {
	r.mu.Lock()
	r.buf[r.next] = rec
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	subs := slices.Clone(r.subs)
	r.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

// Records returns what the ring holds, oldest first.
func (r *Ring) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return slices.Clone(r.buf[:r.next])
	}
	return append(slices.Clone(r.buf[r.next:]), r.buf[:r.next]...)
}

// Tail is the last n records, oldest first.
func (r *Ring) Tail(n int) []Record {
	all := r.Records()
	return all[max(0, len(all)-n):]
}

// OnWrite calls fn after every write. It must not block.
func (r *Ring) OnWrite(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subs = append(r.subs, fn)
}

// Tee writes every record to each sink.
type Tee []Sink

func (t Tee) Write(r Record) {
	for _, s := range t {
		s.Write(r)
	}
}

// Lines is an io.Writer that logs each line written to it, for a child process's stderr (an LSP server, a DAP
// adapter, a process plugin). A line without its newline yet waits for the rest, or Close.
type Lines struct {
	Log   *slog.Logger
	Level slog.Level

	mu  sync.Mutex
	buf []byte
}

func (l *Lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.emit(string(bytes.TrimRight(l.buf[:i], "\r")))
		l.buf = l.buf[i+1:]
	}
	return len(p), nil
}

// Close logs whatever is left without a newline.
func (l *Lines) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) > 0 {
		l.emit(string(l.buf))
		l.buf = nil
	}
	return nil
}

func (l *Lines) emit(s string) {
	if strings.TrimSpace(s) != "" {
		l.Log.Log(context.Background(), l.Level, s)
	}
}
