package ghostty

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/term"
)

// testBackend is the core the tests use: $ORIEL_VT_BACKEND (native or wasm), else the build's default. CI runs them
// with each; TestConformance and TestParseThroughputBudget run every backend built in.
var testBackend = os.Getenv("ORIEL_VT_BACKEND")

// backends are the cores built in: wasm always, native with the ghostty_native tag.
func backends() []string {
	if Native() {
		return []string{"wasm", "native"}
	}
	return []string{"wasm"}
}

func mustNew(t testing.TB, cols, rows int, opts Options) *Terminal {
	t.Helper()
	if opts.Backend == "" {
		opts.Backend = testBackend
	}
	e, err := New(cols, rows, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// The embedded module is what wasm/build.sh makes: it decompresses to the SHA256SUMS line, which CI checks a rebuild
// from the pinned sources against.
func TestWasmChecksum(t *testing.T) {
	sums, err := os.ReadFile("wasm/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	want, _, _ := strings.Cut(string(sums), "  ")
	b, err := wasmBytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != want {
		t.Errorf("oriel-vt.wasm is %x, SHA256SUMS says %s", got, want)
	}
}

func screen(g term.Grid) []string {
	var out []string
	for _, l := range g.Lines {
		out = append(out, l.Text())
	}
	return out
}

// Grid marks the rows that changed since the last call (damage tracking per row). Marking is
// conservative: the row the cursor left counts as changed too.
func TestDirtyRows(t *testing.T) {
	e := mustNew(t, 10, 4, Options{})
	e.Write([]byte("a\r\nb\r\nc"))
	first := e.Grid()
	for i, l := range first.Lines {
		if !l.Dirty {
			t.Errorf("row %d of the first grid isn't dirty", i)
		}
	}
	if g := e.Grid(); dirtyRows(g) != "" {
		t.Errorf("unchanged grid has dirty rows %q", dirtyRows(g))
	}
	e.Write([]byte("\x1b[2;1HX"))
	g := e.Grid()
	if got := dirtyRows(g); got != "1 2" {
		t.Errorf("dirty rows %q, want the changed row and the one the cursor left: 1 2", got)
	}
	if got := screen(g); got[0] != "a         " || got[1] != "X         " || got[2] != "c         " {
		t.Errorf("clean rows lost their cells: %q", got)
	}
}

func dirtyRows(g term.Grid) string {
	var out []string
	for i, l := range g.Lines {
		if l.Dirty {
			out = append(out, fmt.Sprint(i))
		}
	}
	return strings.Join(out, " ")
}

// shown is everything a pane shows of a terminal, for comparing two.
func shown(e *Terminal) map[string]any {
	cols, rows := e.Size()
	sb := e.Scrollbar()
	var history []string
	for y := range sb.Total - rows {
		history = append(history, e.Text(term.Point{Y: y}, term.Point{X: cols - 1, Y: y}))
	}
	g := e.Grid()
	var cells [][]term.Cell
	for _, l := range g.Lines {
		cells = append(cells, append([]term.Cell(nil), l.Cells...))
	}
	modes := map[uint16]bool{}
	for _, m := range []term.Mode{term.ModeAppCursorKeys, term.ModeBracketedPaste, term.ModeMouseSGR, term.ModeAutoWrap, term.ModeGraphemeCluster} {
		modes[m.N] = e.Mode(m)
	}
	return map[string]any{
		"size": [2]int{cols, rows}, "cells": cells, "cursor": e.CursorState(), "title": e.Title(), "cwd": e.Cwd(),
		"history": history, "commands": e.Commands(), "alt": e.AltScreen(), "modes": modes, "output": term.LastOutput(e),
	}
}

// Snapshot on reconnect: a terminal restored from another's snapshot shows the same thing, and the next bytes
// (which may finish a sequence the snapshot caught half-way) leave both the same.
func TestSnapshotRestore(t *testing.T) {
	var stream bytes.Buffer
	stream.WriteString("\x1b]2;build\x07\x1b]7;file:///home/me/src\x07")
	for i := range 30 {
		fmt.Fprintf(&stream, "\x1b[3%dmline %d\x1b[0m \x1b[1;4:3;58:5:9mstyled\x1b[0m 漢\r\n", i%8, i)
	}
	stream.WriteString("\x1b]133;A\x07$ \x1b]133;B\x07ls\r\n\x1b]133;C\x07a\r\nb\r\n\x1b]133;D;2\x07\x1b]133;A\x07$ ")
	stream.WriteString("\x1b[?1h\x1b[?2004h\x1b[?1006h\x1b[38;2;1;2;3m")
	tail := []byte("\x1b[48;5;100m\x1b]133;B\x07next\r\n")
	cut := 5 // inside the SGR, so the snapshot carries an unfinished sequence

	a := mustNew(t, 20, 5, Options{})
	a.Write(stream.Bytes())
	a.Write(tail[:cut])
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	b := mustNew(t, 20, 5, Options{})
	b.Write([]byte("something else entirely\r\n\x1b]2;old\x07"))
	if err := b.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if sa, sb := shown(a), shown(b); !reflect.DeepEqual(sa, sb) {
		for k := range sa {
			if !reflect.DeepEqual(sa[k], sb[k]) {
				t.Errorf("%s after restore:\n got %v\nwant %v", k, sb[k], sa[k])
			}
		}
	}
	a.Write(tail[cut:])
	b.Write(tail[cut:])
	if sa, sb := shown(a), shown(b); !reflect.DeepEqual(sa, sb) {
		for k := range sa {
			if !reflect.DeepEqual(sa[k], sb[k]) {
				t.Errorf("%s after more output:\n got %v\nwant %v", k, sb[k], sa[k])
			}
		}
	}
	var next *term.Cell
	for _, l := range b.Grid().Lines {
		if strings.HasPrefix(l.Text(), "$ next") {
			next = &l.Cells[2]
		}
	}
	if next == nil || next.BG != term.PaletteColor(100) || next.FG != term.RGBColor(1, 2, 3) {
		t.Errorf("the SGR split by the snapshot didn't apply: %+v", next)
	}
	if cmds := b.Commands(); len(cmds) != 2 || cmds[1].Input.Y < 0 {
		t.Errorf("the mark after the snapshot wasn't recorded: %+v", cmds)
	}
	if err := b.Restore([]byte("nonsense")); err == nil {
		t.Error("restored a bad snapshot")
	}
}

// ref:   xterm/ctlseqs.txt PC-Style Function Keys
// quote: "Cursor Up | CSI A | SS3 A"
// ref:   xterm/ctlseqs.txt PC-Style Function Keys
// quote: "Home | CSI H | SS3 H"
func TestEncodeKey(t *testing.T) {
	e := mustNew(t, 10, 2, Options{})
	press := func(k term.Key, text string) string {
		return string(e.EncodeKey(term.KeyEvent{Key: k, Action: term.KeyPress, Text: text}))
	}
	for _, c := range []struct {
		key      term.Key
		normal   string
		app      string
		text     string
		consumed bool
	}{
		{key: term.KeyArrowUp, normal: "\x1b[A", app: "\x1bOA"},
		{key: term.KeyArrowDown, normal: "\x1b[B", app: "\x1bOB"},
		{key: term.KeyArrowRight, normal: "\x1b[C", app: "\x1bOC"},
		{key: term.KeyArrowLeft, normal: "\x1b[D", app: "\x1bOD"},
		{key: term.KeyHome, normal: "\x1b[H", app: "\x1bOH"},
		{key: term.KeyEnd, normal: "\x1b[F", app: "\x1bOF"},
		{key: term.KeyA, normal: "a", app: "a", text: "a"},
	} {
		e.Write([]byte("\x1b[?1l"))
		if got := press(c.key, c.text); got != c.normal {
			t.Errorf("key %d normal = %q, want %q", c.key, got, c.normal)
		}
		e.Write([]byte("\x1b[?1h"))
		if got := press(c.key, c.text); got != c.app {
			t.Errorf("key %d application = %q, want %q", c.key, got, c.app)
		}
	}
}

// ref:   xterm/ctlseqs.txt Bracketed Paste Mode
// quote: "When bracketed paste mode is set, the program will receive: ESC [ 2 0 0 ~ , followed by the pasted text, followed by ESC [ 2 0 1 ~ ."
func TestPaste(t *testing.T) {
	e := mustNew(t, 10, 2, Options{})
	if got := string(e.Paste("hello")); got != "hello" {
		t.Errorf("paste = %q", got)
	}
	e.Write([]byte("\x1b[?2004h"))
	if got := string(e.Paste("hello")); got != "\x1b[200~hello\x1b[201~" {
		t.Errorf("bracketed paste = %q", got)
	}
}

// ref:   libghostty-vt/stream_terminal.zig
// quote: "The caller must use a timeout so that a program that never"
func TestHoldTimeout(t *testing.T) {
	now := time.Unix(0, 0)
	e := mustNew(t, 10, 1, Options{HoldTimeout: time.Second, Now: func() time.Time { return now }})
	e.Write([]byte("abc"))
	e.Grid()
	e.Write([]byte("\x1b[?2026hXYZ"))
	if got := screen(e.Grid())[0]; got != "abc       " || !e.Held() {
		t.Errorf("held frame = %q, held %v", got, e.Held())
	}
	now = now.Add(999 * time.Millisecond)
	if got := screen(e.Grid())[0]; got != "abc       " {
		t.Errorf("frame before the timeout = %q", got)
	}
	now = now.Add(time.Millisecond)
	if got := screen(e.Grid())[0]; got != "abcXYZ    " {
		t.Errorf("frame after the timeout = %q", got)
	}
}

func TestScrollback(t *testing.T) {
	e := mustNew(t, 10, 2, Options{})
	for i := range 5 {
		fmt.Fprintf(writer{e}, "line %d\r\n", i)
	}
	sb := e.Scrollbar()
	if sb.Total != 6 || sb.Len != 2 || sb.Offset != 4 {
		t.Errorf("scrollbar %+v", sb)
	}
	e.Scroll(term.Scroll{To: term.ScrollTop})
	if got := screen(e.Grid()); got[0] != "line 0    " || got[1] != "line 1    " {
		t.Errorf("top of the scrollback = %q", got)
	}
	e.Scroll(term.Scroll{To: term.ScrollDelta, Delta: 2})
	if got := screen(e.Grid()); got[0] != "line 2    " {
		t.Errorf("scrolled down two = %q", got)
	}
	e.Scroll(term.Scroll{To: term.ScrollBottom})
	if got := screen(e.Grid()); got[0] != "line 4    " {
		t.Errorf("bottom = %q", got)
	}
	if got := e.Text(term.Point{X: 0, Y: 1}, term.Point{X: 9, Y: 2}); got != "line 1\nline 2" {
		t.Errorf("Text = %q", got)
	}
}

type writer struct{ e *Terminal }

func (w writer) Write(p []byte) (int, error) { w.e.Write(p); return len(p), nil }

func TestResize(t *testing.T) {
	e := mustNew(t, 10, 2, Options{})
	e.Write([]byte("abcdefgh"))
	e.Grid()
	e.Resize(4, 3)
	g := e.Grid()
	if c, r := e.Size(); c != 4 || r != 3 || g.Cols != 4 || len(g.Lines) != 3 || len(g.Lines[0].Cells) != 4 {
		t.Fatalf("size %dx%d, grid %dx%d", c, r, g.Cols, len(g.Lines))
	}
}

// A PTY reader writes while a renderer reads; run with -race.
func TestConcurrentUse(t *testing.T) {
	e := mustNew(t, 40, 10, Options{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 2000 {
			fmt.Fprintf(writer{e}, "\x1b[3%dmline %d\x1b[0m\r\n", i%8, i)
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			e.Grid()
			e.CursorState()
			e.Title()
		}
	}()
	wg.Wait()
}

func TestClosed(t *testing.T) {
	e := mustNew(t, 10, 2, Options{})
	e.Close()
	e.Write([]byte("abc"))
	if g := e.Grid(); len(g.Lines) != 0 || e.Title() != "" {
		t.Errorf("a closed terminal shows %+v", g)
	}
	if _, err := e.Snapshot(); err == nil {
		t.Error("snapshot of a closed terminal")
	}
	if err := e.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// The wheel follows the modes the program set through the VT: mouse reports once it asks for them, in SGR when it
// asks for that; arrow keys on the alternate screen with alternate scroll (1007) set; nothing on the main screen.
func TestWheelFollowsTheProgramsModes(t *testing.T) {
	e := mustNew(t, 20, 5, Options{})
	if got := term.Wheel(e, 2, 1, -1); got != nil {
		t.Errorf("main screen, no mouse: %q", got)
	}
	e.Write([]byte("\x1b[?1049h\x1b[?1007h"))
	if got := string(term.Wheel(e, 2, 1, 1)); got != "\x1b[B" {
		t.Errorf("alternate screen, alternate scroll: %q", got)
	}
	e.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	if got := string(term.Wheel(e, 2, 1, -1)); got != "\x1b[<64;3;2M" {
		t.Errorf("mouse reporting in SGR: %q", got)
	}
	e.Write([]byte("\x1b[?1000l\x1b[?1049l"))
	if got := term.Wheel(e, 2, 1, -1); got != nil {
		t.Errorf("back on the main screen, no mouse: %q", got)
	}
}
