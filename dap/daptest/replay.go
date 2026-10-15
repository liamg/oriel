package daptest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/dap"
)

// Fake is an adapter that replays a transcript. It sends the adapter's messages in the recorded order, and before
// each message the client sent in the recording it waits for the client to send one like it: the same command and
// arguments (seq aside, with the vars made concrete). Messages the client sends between two of the adapter's may
// come in any order.
type Fake struct {
	t       *Transcript
	vars    Vars
	timeout time.Duration

	mu   sync.Mutex
	errs []error
	done chan struct{}
}

// NewFake replays t with vars. Each wait for the client gives up after timeout.
func NewFake(t *Transcript, vars Vars, timeout time.Duration) *Fake {
	return &Fake{t: t, vars: vars, timeout: timeout, done: make(chan struct{})}
}

// Launcher starts the replay when a client launches its adapter; it can be used once.
func (f *Fake) Launcher() dap.Launcher {
	return func(ctx context.Context, spec dap.AdapterSpec, dir string, stderr io.Writer) (dap.Process, error) {
		client, server := net.Pipe()
		go f.serve(server)
		return &fakeProcess{Conn: client, done: f.done}, nil
	}
}

// Wait blocks until the replay ends, and returns what went wrong.
func (f *Fake) Wait() error {
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	return errors.Join(f.errs...)
}

func (f *Fake) fail(format string, args ...any) {
	f.mu.Lock()
	f.errs = append(f.errs, fmt.Errorf(format, args...))
	f.mu.Unlock()
}

type fakeProcess struct {
	net.Conn
	done chan struct{}
}

func (p *fakeProcess) Wait() error { <-p.done; return nil }
func (p *fakeProcess) Kill() error { return p.Conn.Close() }

type received struct {
	msg  dap.Message
	body []byte
}

// readAll sends each message the client writes on the channel it returns, which closes when the client does.
func readAll(conn net.Conn) chan received {
	in := make(chan received, 256)
	go func() {
		defer close(in)
		r := bufio.NewReader(conn)
		for {
			body, err := dap.ReadFrame(r)
			if err != nil {
				return
			}
			var m dap.Message
			if json.Unmarshal(body, &m) != nil {
				continue
			}
			in <- received{m, body}
		}
	}()
	return in
}

func (f *Fake) serve(conn net.Conn) {
	defer close(f.done)
	defer conn.Close()
	in := readAll(conn)
	var pending []received
	seqs := map[int]int{} // recorded client seq → the live one
	closed := false
	for i, e := range f.t.Entries {
		body := f.vars.concrete(e.Msg)
		var rec dap.Message
		if err := json.Unmarshal(body, &rec); err != nil {
			f.fail("entry %d: %v", i, err)
			return
		}
		if e.Dir == Recv {
			if rec.Type == "response" {
				live, ok := seqs[rec.RequestSeq]
				if !ok {
					f.fail("entry %d: response to request %d the client never sent", i, rec.RequestSeq)
					return
				}
				body = withRequestSeq(body, live)
			}
			if err := dap.WriteFrame(conn, body); err != nil {
				f.fail("entry %d: writing: %v", i, err)
				return
			}
			continue
		}
		deadline := time.After(f.timeout)
		for {
			if j := match(pending, rec); j >= 0 {
				if rec.Type == "request" {
					seqs[rec.Seq] = pending[j].msg.Seq
				}
				pending = append(pending[:j], pending[j+1:]...)
				break
			}
			if closed {
				f.fail("entry %d: the client closed while the transcript waits for %s", i, describe(body))
				return
			}
			select {
			case r, ok := <-in:
				if !ok {
					closed = true
					continue
				}
				pending = append(pending, r)
			case <-deadline:
				var got []string
				for _, p := range pending {
					got = append(got, describe(p.body))
				}
				f.fail("entry %d: waited %v for %s; the client sent %s", i, f.timeout, describe(body), strings.Join(got, ", "))
				return
			}
		}
	}
	for _, p := range pending {
		f.fail("unexpected message from the client: %s", describe(p.body))
	}
}

// withRequestSeq is a response with its request_seq changed.
func withRequestSeq(body []byte, seq int) []byte {
	var m map[string]json.RawMessage
	json.Unmarshal(body, &m)
	m["request_seq"], _ = json.Marshal(seq)
	out, _ := json.Marshal(m)
	return out
}

func describe(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// match finds the received message the recorded one stands for.
func match(pending []received, rec dap.Message) int {
	for j, p := range pending {
		m := p.msg
		if m.Type != rec.Type || m.Command != rec.Command {
			continue
		}
		switch rec.Type {
		case "request":
			if sameJSON(m.Arguments, rec.Arguments) {
				return j
			}
		case "response":
			if m.RequestSeq == rec.RequestSeq {
				return j
			}
		}
	}
	return -1
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if len(a) > 0 && json.Unmarshal(a, &x) != nil {
		return false
	}
	if len(b) > 0 && json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
