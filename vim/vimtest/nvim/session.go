package nvim

import (
	"fmt"

	"github.com/liamg/oriel/vim/vimtest"
)

// Session is a long-lived nvim that runs Vimscript, for tools that replay vim's own tests.
type Session struct{ e *engine }

// Start starts nvim (mappings cleared, as for the oracle) with dir as its working directory.
func Start(dir string) (*Session, error) {
	e, err := startIn(dir)
	if err != nil {
		return nil, err
	}
	return &Session{e}, nil
}

// Exec runs Vimscript source, as a sourced script would.
func (s *Session) Exec(src string) error {
	_, err := s.e.c.call("nvim_exec2", src, map[string]any{})
	return err
}

// Eval evaluates a Vimscript expression.
func (s *Session) Eval(expr string) (any, error) {
	return s.e.c.call("nvim_eval", expr)
}

// State is the current buffer, cursor and mode.
func (s *Session) State() (vimtest.Buffer, string, error) {
	r, err := s.e.Snapshot(vimtest.Query{})
	if err != nil {
		return vimtest.Buffer{}, "", fmt.Errorf("snapshot: %w", err)
	}
	return vimtest.Buffer{Lines: r.Lines, Cursor: r.Cursor}, r.Mode, nil
}

func (s *Session) Close() error { return s.e.Close() }

// Input types keys in vim notation, as nvim_input, and waits for nvim to take them.
func (s *Session) Input(keys string) error { return s.e.feed(keys) }
