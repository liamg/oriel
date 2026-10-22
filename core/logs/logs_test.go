package logs

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
)

// Secrets never reach a log: values from the secrets store and tokens in URLs are redacted before a record is
// written.
func TestRedact(t *testing.T) {
	r := NewRedactor()
	r.Add("lin_api_abcdef123456")
	r.Add("abc") // too short to mask
	for in, want := range map[string]string{
		"token lin_api_abcdef123456 used":                       "token " + Mask + " used",
		"GET https://api.example.com/v1?access_token=xyz&q=a":   "GET https://api.example.com/v1?access_token=" + Mask + "&q=a",
		"https://hooks.slack.com/x?TOKEN=s3cr3t#frag":           "https://hooks.slack.com/x?TOKEN=" + Mask + "#frag",
		"clone https://liam:ghp_123456@github.com/liamg/oriel":  "clone https://liam:" + Mask + "@github.com/liamg/oriel",
		"https://github.com/liamg/oriel?tab=readme and abc def": "https://github.com/liamg/oriel?tab=readme and abc def",
		"plain text": "plain text",
	} {
		if got := r.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	var nilR *Redactor
	if got := nilR.Redact("x?token=1"); got != "x?token="+Mask {
		t.Errorf("nil redactor: %q", got)
	}
}

func TestHandlerFlattensAndRedacts(t *testing.T) {
	red := NewRedactor()
	red.Add("hunter2hunter2")
	ring := NewRing(10)
	lv := new(slog.LevelVar)
	log := slog.New(NewHandler(ring, lv, red)).With("source", "lsp:gopls", "session", "s1")
	log.Debug("dropped")
	log.WithGroup("req").Info("sent hunter2hunter2", "url", "https://x.io/?key=k1", slog.Group("pos", "line", 3))
	lv.Set(slog.LevelDebug)
	log.Debug("kept", "notice", uint64(7))

	recs := ring.Records()
	if len(recs) != 2 {
		t.Fatalf("%d records, want 2", len(recs))
	}
	r := recs[0]
	if r.Source != "lsp:gopls" || r.Session != "s1" || r.Msg != "sent "+Mask {
		t.Errorf("record = %+v", r)
	}
	want := []Attr{{"req.url", "https://x.io/?key=" + Mask}, {"req.pos.line", "3"}}
	if len(r.Attrs) != 2 || r.Attrs[0] != want[0] || r.Attrs[1] != want[1] {
		t.Errorf("attrs = %v, want %v", r.Attrs, want)
	}
	if recs[1].Notice != 7 || recs[1].Level != slog.LevelDebug {
		t.Errorf("record = %+v", recs[1])
	}
}

func TestRingWraps(t *testing.T) {
	r := NewRing(3)
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		r.Write(Record{Msg: m})
	}
	var got []string
	for _, rec := range r.Records() {
		got = append(got, rec.Msg)
	}
	if strings.Join(got, "") != "cde" {
		t.Errorf("records = %v", got)
	}
	if tail := r.Tail(2); len(tail) != 2 || tail[0].Msg != "d" {
		t.Errorf("tail = %v", tail)
	}
}

func TestFileRotates(t *testing.T) {
	dir := t.TempDir()
	f := &File{Path: filepath.Join(dir, "sub", "oriel.log"), MaxSize: 200, Keep: 2}
	for range 20 {
		f.Write(Record{Time: time.Unix(0, 0).UTC(), Msg: strings.Repeat("x", 40)})
	}
	if err := f.Err(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, name := range []string{"oriel.log", "oriel.log.1", "oriel.log.2"} {
		st, err := os.Stat(filepath.Join(dir, "sub", name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > 200 {
			t.Errorf("%s is %d bytes", name, st.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "oriel.log.3")); err == nil {
		t.Error("kept a third backup")
	}
}

// A session log lives with the session, survives a restart, and is archived with the session. A notice goes to exactly one log, by its source.
func TestSessionLogSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	var oriel []Record
	r1 := &Router{Ring: NewRing(10), Oriel: SinkFunc(func(r Record) { oriel = append(oriel, r) }), Sessions: Dirs{Root: root}}
	r1.Write(Record{Session: "s1", Msg: "worktree ready"})
	r1.Write(Record{Msg: "reloaded init.lua"})
	r1.Write(Record{Session: "s1", Msg: "gopls started"})
	if err := r1.Err(); err != nil {
		t.Fatal(err)
	}
	r1.Close()
	if len(oriel) != 1 || oriel[0].Msg != "reloaded init.lua" {
		t.Errorf("oriel log = %v", oriel)
	}

	r2 := &Router{Ring: NewRing(10), Sessions: Dirs{Root: root}}
	if err := r2.Load("s1"); err != nil {
		t.Fatal(err)
	}
	r2.Write(Record{Session: "s1", Msg: "after"})
	var got []string
	for _, rec := range r2.Ring.Records() {
		got = append(got, rec.Msg)
	}
	if strings.Join(got, "|") != "worktree ready|gopls started|after" {
		t.Errorf("ring after restart = %v", got)
	}
	r2.Close()
	if _, err := (Dirs{Root: root}).Open("../escape"); err == nil {
		t.Error("opened a session log outside the root")
	}
}

func TestSessionLogSkipsTornLine(t *testing.T) {
	root := t.TempDir()
	l, err := Dirs{Root: root}.Open(core.SessionID("s1"))
	if err != nil {
		t.Fatal(err)
	}
	l.Append(Record{Msg: "one"})
	l.Close()
	f, _ := os.OpenFile(filepath.Join(root, "s1", "session.log"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"msg":"tor`)
	f.Close()
	l, _ = Dirs{Root: root}.Open("s1")
	defer l.Close()
	recs, err := l.Records()
	if err != nil || len(recs) != 1 || recs[0].Msg != "one" {
		t.Errorf("records = %v, %v", recs, err)
	}
}

// LSP servers', DAP adapters' and process plugins' stderr are captured as records with their source.
func TestLinesCapturesStderr(t *testing.T) {
	ring := NewRing(10)
	w := &Lines{Log: slog.New(NewHandler(ring, nil, nil)).With("source", "lsp:gopls"), Level: slog.LevelWarn}
	w.Write([]byte("gopls: loading packa"))
	w.Write([]byte("ges\r\n\nsecond line\npartial"))
	w.Close()
	var got []string
	for _, r := range ring.Records() {
		if r.Source != "lsp:gopls" || r.Level != slog.LevelWarn {
			t.Errorf("record = %+v", r)
		}
		got = append(got, r.Msg)
	}
	if strings.Join(got, "|") != "gopls: loading packages|second line|partial" {
		t.Errorf("lines = %q", got)
	}
}
