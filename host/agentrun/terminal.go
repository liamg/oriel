package agentrun

import (
	"context"
	"errors"

	"github.com/liamg/oriel/core"
)

// Terminal is the agent's raw terminal: what it has shown, then what it shows, until ctx ends or the agent
// stops.
func (x *Runner) Terminal(ctx context.Context, id core.SessionID) (<-chan []byte, error) {
	h, _ := x.sessionFor(id, false)
	if h == nil {
		return nil, errors.New("the agent isn't running")
	}
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil || sess.Terminal() == nil {
		return nil, errors.New("this agent has no terminal")
	}
	backlog, live, detach := sess.Terminal().Attach()
	out := make(chan []byte, 64)
	x.env.Go("Terminal", func() {
		defer close(out)
		defer detach()
		send := func(b []byte) bool {
			select {
			case out <- b:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if len(backlog) > 0 && !send(backlog) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-x.env.Closed:
				return
			case b, ok := <-live:
				if !ok || !send(b) {
					return
				}
			}
		}
	})
	return out, nil
}

// TermInput types into the agent's terminal, and resizes it.
func (x *Runner) TermInput(r TermReq) error {
	h, _ := x.sessionFor(r.Session, false)
	if h == nil {
		return errors.New("the agent isn't running")
	}
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil || sess.Terminal() == nil {
		return errors.New("this agent has no terminal")
	}
	t := sess.Terminal()
	if r.Cols > 0 && r.Rows > 0 {
		if err := t.Resize(r.Cols, r.Rows); err != nil {
			return err
		}
	}
	if len(r.Data) > 0 {
		_, err := t.Write(r.Data)
		return err
	}
	return nil
}
