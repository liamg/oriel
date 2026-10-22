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
	out   *io.PipeReader
	in    *io.PipeWriter
	typed chan []byte
}

func (p *pipeSource) Read(b []byte) (int, error) { return p.out.Read(b) }
func (p *pipeSource) Write(b []byte) (int, error) {
	p.typed <- append([]byte(nil), b...)
	return len(b), nil
}
func (p *pipeSource) Resize(int, int) error { return nil }
func (p *pipeSource) Close() error          { return p.out.Close() }

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
