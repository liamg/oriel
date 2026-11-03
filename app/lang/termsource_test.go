package lang_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// pipeSource is a terminal source a test writes into, and reads what was typed from.
type pipeSource struct {
	out    *io.PipeReader
	in     *io.PipeWriter
	typed  chan []byte
	closed chan struct{} // closed on Close, when not nil
}

func (p *pipeSource) Read(b []byte) (int, error) { return p.out.Read(b) }
func (p *pipeSource) Write(b []byte) (int, error) {
	p.typed <- append([]byte(nil), b...)
	return len(b), nil
}
func (p *pipeSource) Resize(int, int) error { return nil }
func (p *pipeSource) Close() error {
	if p.closed != nil {
		close(p.closed)
	}
	return p.out.Close()
}

// A terminal view shows any source of bytes, not only a program it starts: the agent's raw terminal is one.
func TestTerminalShowsASource(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Run("editor.open", registry.Args{"path": "a.txt"})
	pr, pw := io.Pipe()
	src := &pipeSource{out: pr, in: pw, typed: make(chan []byte, 16)}
	t.Cleanup(func() { pw.Close() })
	s.Run("term.open", registry.Args{"source": lang.TermSource(src), "title": "agent · w"})
	go pw.Write([]byte("hello from elsewhere\r\n"))
	s.Until("the source's text", func() bool { return s.Has("hello from elsewhere") })
	s.Press("x")
	select {
	case b := <-src.typed:
		if string(b) != "x" {
			t.Errorf("typed %q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("what was typed never reached the source")
	}
}

// Opening a source on an instance that's already open (the agent's raw terminal, opened again) stops the one
// before, so the window can still open again on another directory, which waits for every terminal's reader.
func TestTerminalOpenedAgainStopsTheOneBefore(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	open := func() *pipeSource {
		pr, pw := io.Pipe()
		src := &pipeSource{out: pr, in: pw, typed: make(chan []byte, 16), closed: make(chan struct{})}
		t.Cleanup(func() { pw.Close() })
		s.Run("term.open", registry.Args{"source": lang.TermSource(src), "title": "agent · w", "instance": "agent-w"})
		return src
	}
	first := open()
	open()
	select {
	case <-first.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal opened before was never stopped")
	}
	done := make(chan struct{})
	go func() { s.Reopen(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the window never opened again")
	}
}

// The wheel over a terminal goes to its program once the program asks for mouse events, as a real terminal sends it
// (term.Wheel); with Shift held, or before the program asks, it scrolls the terminal's own scrollback.
func TestTerminalWheelGoesToAProgramThatAsks(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	pr, pw := io.Pipe()
	src := &pipeSource{out: pr, in: pw, typed: make(chan []byte, 64)}
	t.Cleanup(func() { pw.Close() })
	s.Run("term.open", registry.Args{"source": lang.TermSource(src), "title": "agent · w"})
	var lines strings.Builder
	for i := range 200 {
		fmt.Fprintf(&lines, "line %03d\r\n", i)
	}
	go pw.Write([]byte(lines.String()))
	s.Until("the last line", func() bool { return s.Has("line 199") })
	at := s.TextPoint("line 199")
	wheel := func(dy float32, mods render.Mods) {
		s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
		s.UI().Mods(mods)
		s.Frame()
		s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: dy}, Mods: mods})
		s.Frame()
		s.Frame()
		s.UI().Mods(0)
	}
	typed := func() string {
		var out []byte
		for {
			select {
			case b := <-src.typed:
				out = append(out, b...)
			case <-time.After(200 * time.Millisecond):
				return string(out)
			}
		}
	}

	wheel(-2000, 0)
	if s.Has("line 199") || typed() != "" {
		t.Fatalf("without mouse reporting, the wheel didn't scroll the scrollback:\n%s", s.Text())
	}
	wheel(4000, 0)
	go pw.Write([]byte("\x1b[?1000h\x1b[?1006hready\r\n"))
	s.Until("the program's next line", func() bool { return s.Has("ready") })
	wheel(-60, 0)
	if got := typed(); !strings.HasPrefix(got, "\x1b[<64;") || !strings.HasSuffix(got, "M") {
		t.Errorf("with mouse reporting, the program got %q", got)
	}
	wheel(-2000, render.ModShift)
	if s.Has("ready") || typed() != "" {
		t.Errorf("Shift and the wheel didn't scroll the scrollback:\n%s", s.Text())
	}
}
