package daptest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/liamg/oriel/dap"
)

// Responder is an adapter that answers from a transcript in whatever order it's asked, as a debugger UI asks. A
// request gets the recorded answer to the first request of the same command and arguments not yet answered (once
// all are, the last), and is followed by what the adapter sent after it in the recording up to the client's next
// message: its events, and responses to requests already asked (so launch's comes with configurationDone's, as
// recorded); then its own response if that wasn't among them. It doesn't check the order: Fake does that.
type Responder struct {
	t    *Transcript
	vars Vars

	mu    sync.Mutex
	asked []string
	errs  []error
}

func NewResponder(t *Transcript, vars Vars) *Responder { return &Responder{t: t, vars: vars} }

// Launcher starts an answering adapter each time a client launches one.
func (r *Responder) Launcher() dap.Launcher {
	return func(ctx context.Context, spec dap.AdapterSpec, dir string, stderr io.Writer) (dap.Process, error) {
		client, server := net.Pipe()
		done := make(chan struct{})
		go r.serve(server, done)
		return &fakeProcess{Conn: client, done: done}, nil
	}
}

// Asked are the commands clients asked for, in order.
func (r *Responder) Asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
}

// Err is the requests it had no answer for.
func (r *Responder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(r.errs...)
}

func (r *Responder) serve(conn net.Conn, done chan struct{}) {
	defer close(done)
	defer conn.Close()
	type entry struct {
		dir  string
		msg  dap.Message
		body []byte
	}
	var entries []entry
	for _, e := range r.t.Entries {
		body := r.vars.Concrete(e.Msg)
		var m dap.Message
		if json.Unmarshal(body, &m) == nil {
			entries = append(entries, entry{e.Dir, m, body})
		}
	}
	used := map[int]bool{} // entries answered
	live := map[int]int{}  // recorded request seq → the live one
	sent := map[int]bool{} // recv entries sent
	for got := range readAll(conn) {
		m := got.msg
		if m.Type != "request" {
			continue
		}
		r.mu.Lock()
		r.asked = append(r.asked, m.Command)
		r.mu.Unlock()
		at, last := -1, -1
		for i, e := range entries {
			if e.dir == Send && e.msg.Type == "request" && e.msg.Command == m.Command && sameJSON(e.msg.Arguments, m.Arguments) {
				last = i
				if !used[i] {
					at = i
					break
				}
			}
		}
		if at < 0 && last >= 0 {
			// Asked again: the same answer, without the events that followed it.
			if body, ok := r.response(entries[last].msg.Seq); ok {
				dap.WriteFrame(conn, withRequestSeq(body, m.Seq))
			}
			continue
		}
		if at < 0 {
			r.mu.Lock()
			r.errs = append(r.errs, fmt.Errorf("no recorded answer to %s %s", m.Command, m.Arguments))
			r.mu.Unlock()
			b, _ := json.Marshal(map[string]any{"seq": 0, "type": "response", "request_seq": m.Seq, "success": false, "command": m.Command,
				"message": "no recorded answer"})
			dap.WriteFrame(conn, b)
			continue
		}
		used[at] = true
		live[entries[at].msg.Seq] = m.Seq
		send := func(i int) {
			body := entries[i].body
			if entries[i].msg.Type == "response" {
				body = withRequestSeq(body, live[entries[i].msg.RequestSeq])
			}
			sent[i] = true
			dap.WriteFrame(conn, body)
		}
		for i := at + 1; i < len(entries) && entries[i].dir == Recv; i++ {
			e := entries[i]
			if _, asked := live[e.msg.RequestSeq]; !sent[i] && (e.msg.Type != "response" || asked) {
				send(i)
			}
		}
		// Its own response, wherever the recording has it: a request asked in another order than recorded is
		// still answered.
		for i := at + 1; i < len(entries); i++ {
			if e := entries[i]; e.dir == Recv && e.msg.Type == "response" && e.msg.RequestSeq == entries[at].msg.Seq {
				if !sent[i] {
					send(i)
				}
				break
			}
		}
	}
}

// response is the recorded response to the request with seq.
func (r *Responder) response(seq int) ([]byte, bool) {
	for _, e := range r.t.Entries {
		if e.Dir != Recv {
			continue
		}
		body := r.vars.Concrete(e.Msg)
		var m dap.Message
		if json.Unmarshal(body, &m) == nil && m.Type == "response" && m.RequestSeq == seq {
			return body, true
		}
	}
	return nil, false
}
