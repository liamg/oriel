package lang_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
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
