package lsptest

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

	"github.com/liamg/oriel/internal/transcript"
	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/lsp"
)

// Fake is a server that replays a transcript. It sends the server's messages in the recorded order, and before
// each message the client sent in the recording it waits for the client to send one like it: the same method and
// params (ids aside, with the root abstracted), or for a response, the same id. Messages the client sends between
// two of the server's may come in any order.
type Fake struct {
	t       *Transcript
	timeout time.Duration

	mu   sync.Mutex
	errs []error
	done chan struct{}
}

// Ignore are fields left out when comparing params: they differ from run to run.
var Ignore = []string{"processId"}

// NewFake replays t. Each wait for the client gives up after timeout.
func NewFake(t *Transcript, timeout time.Duration) *Fake {
	return &Fake{t: t, timeout: timeout, done: make(chan struct{})}
}

// Launcher starts the replay when a client launches its server; it can be used once.
func (f *Fake) Launcher() lsp.Launcher {
	return func(ctx context.Context, spec lsp.ServerSpec, root string, stderr io.Writer) (lsp.Process, error) {
		client, server := net.Pipe()
		go f.serve(server, root)
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
	msg  jsonrpc.Message
	body []byte
}

func (f *Fake) serve(conn net.Conn, root string) {
	vars := transcript.MachineVars(root)
	defer close(f.done)
	defer conn.Close()
	in := make(chan received, 256)
	go func() {
		defer close(in)
		r := bufio.NewReader(conn)
		for {
			body, err := jsonrpc.ReadFrame(r)
			if err != nil {
				return
			}
			var m jsonrpc.Message
			if json.Unmarshal(body, &m) != nil {
				continue
			}
			in <- received{m, body}
		}
	}()
	var pending []received
	ids := map[string]json.RawMessage{} // recorded request id → the live one
	closed := false
	for i, e := range f.t.Entries {
		var rec jsonrpc.Message
		if err := json.Unmarshal(e.Msg, &rec); err != nil {
			f.fail("entry %d: %v", i, err)
			return
		}
		if e.Dir == Recv {
			body := vars.Concrete(e.Msg)
			if rec.IsResponse() {
				live, ok := ids[jsonrpc.IDKey(rec.ID)]
				if !ok {
					f.fail("entry %d: response to request %s the client never sent", i, rec.ID)
					return
				}
				var m map[string]json.RawMessage
				json.Unmarshal(body, &m)
				m["id"], _ = json.Marshal(live)
				body, _ = json.Marshal(m)
			}
			if err := jsonrpc.WriteFrame(conn, body); err != nil {
				f.fail("entry %d: writing: %v", i, err)
				return
			}
			continue
		}
		want := vars.Concrete(e.Msg)
		deadline := time.After(f.timeout)
		for {
			if j := match(pending, rec, want); j >= 0 {
				if rec.IsRequest() {
					ids[jsonrpc.IDKey(rec.ID)] = pending[j].msg.ID
				}
				pending = append(pending[:j], pending[j+1:]...)
				break
			}
			if closed {
				f.fail("entry %d: the client closed while the transcript waits for %s", i, describe(want))
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
				f.fail("entry %d: waited %v for %s; the client sent %s", i, f.timeout, describe(want), strings.Join(got, ", "))
				return
			}
		}
	}
	for _, p := range pending {
		f.fail("unexpected message from the client: %s", describe(p.body))
	}
}

func describe(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// match finds the received message the recorded one stands for.
func match(pending []received, rec jsonrpc.Message, want []byte) int {
	var w jsonrpc.Message
	json.Unmarshal(want, &w)
	for j, p := range pending {
		m := p.msg
		switch {
		case rec.IsResponse():
			if m.IsResponse() && jsonrpc.IDKey(m.ID) == jsonrpc.IDKey(rec.ID) {
				return j
			}
		case m.Method == rec.Method && m.IsRequest() == rec.IsRequest() && sameJSON(m.Params, w.Params):
			return j
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
	return reflect.DeepEqual(strip(x), strip(y))
}

func strip(v any) any {
	if m, ok := v.(map[string]any); ok {
		for _, k := range Ignore {
			delete(m, k)
		}
		for k, e := range m {
			m[k] = strip(e)
		}
	}
	if a, ok := v.([]any); ok {
		for i, e := range a {
			a[i] = strip(e)
		}
	}
	return v
}
