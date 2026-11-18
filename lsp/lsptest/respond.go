package lsptest

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"

	"github.com/liamg/oriel/internal/transcript"
	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/lsp"
)

// Responder is a server that answers from a transcript in whatever order it's asked, as an editor asks: each request
// gets the recorded answer to the same method with the same params, or failing that the first recorded answer to
// the method. A document's recorded diagnostics (the last non-empty set the server published for it, or the last)
// are sent when the client opens it. It doesn't check what the client sends: Fake does that.
type Responder struct {
	t *Transcript

	mu       sync.Mutex
	asked    []string // methods asked, in order
	launches int
	results  map[string]json.RawMessage // method → the result every request of it gets, over the transcript's
	holds    map[string]chan struct{}   // method → closed when its answers may go
}

func NewResponder(t *Transcript) *Responder { return &Responder{t: t} }

// Launcher starts an answering server each time a client launches one.
func (r *Responder) Launcher() lsp.Launcher {
	return func(ctx context.Context, spec lsp.ServerSpec, root string, stderr io.Writer) (lsp.Process, error) {
		client, server := net.Pipe()
		done := make(chan struct{})
		r.mu.Lock()
		r.launches++
		r.mu.Unlock()
		go r.serve(server, root, done)
		return &fakeProcess{Conn: client, done: done}, nil
	}
}

// Answer has every request of method answered with result from now on, in place of what the transcript recorded:
// a server answering something no recording has.
func (r *Responder) Answer(method string, result any) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.results == nil {
		r.results = map[string]json.RawMessage{}
	}
	r.results[method] = b
	return nil
}

// Hold keeps the answers to method back until release, as a server still starting keeps back its initialize: the
// server reads nothing more meanwhile. release may be called more than once.
func (r *Responder) Hold(method string) (release func()) {
	ch := make(chan struct{})
	r.mu.Lock()
	if r.holds == nil {
		r.holds = map[string]chan struct{}{}
	}
	r.holds[method] = ch
	r.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

// Asked are the methods clients asked for, in order.
func (r *Responder) Asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
}

type answer struct {
	params json.RawMessage
	reply  json.RawMessage // the whole recorded response
}

func (r *Responder) serve(conn net.Conn, root string, done chan struct{}) {
	vars := transcript.MachineVars(root)
	defer close(done)
	defer func() { _ = conn.Close() }()
	answers := map[string][]answer{}
	byID := map[string]json.RawMessage{}
	var methods = map[string]string{} // recorded request id → method
	var params = map[string]json.RawMessage{}
	diags := map[string]json.RawMessage{} // uri → the last publishDiagnostics
	for _, e := range r.t.Entries {
		msg := vars.Concrete(e.Msg)
		var m jsonrpc.Message
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		switch {
		case e.Dir == Send && m.IsRequest():
			methods[jsonrpc.IDKey(m.ID)], params[jsonrpc.IDKey(m.ID)] = m.Method, m.Params
		case e.Dir == Recv && m.IsResponse():
			byID[jsonrpc.IDKey(m.ID)] = msg
		case e.Dir == Recv && m.Method == "textDocument/publishDiagnostics":
			var p struct {
				URI         string            `json:"uri"`
				Diagnostics []json.RawMessage `json:"diagnostics"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if _, seen := diags[p.URI]; !seen || len(p.Diagnostics) > 0 {
				diags[p.URI] = msg
			}
		}
	}
	for id, method := range methods {
		if reply, ok := byID[id]; ok {
			answers[method] = append(answers[method], answer{params[id], reply})
		}
	}
	var wmu sync.Mutex
	write := func(b []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = jsonrpc.WriteFrame(conn, b)
	}
	rd := bufio.NewReader(conn)
	for {
		body, err := jsonrpc.ReadFrame(rd)
		if err != nil {
			return
		}
		var m jsonrpc.Message
		if json.Unmarshal(body, &m) != nil || m.IsResponse() {
			continue
		}
		r.mu.Lock()
		r.asked = append(r.asked, m.Method)
		r.mu.Unlock()
		switch m.Method {
		case "exit":
			return
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if d, ok := diags[p.TextDocument.URI]; ok {
				write(d)
			}
		}
		if !m.IsRequest() {
			continue
		}
		reply := json.RawMessage(`{"jsonrpc":"2.0","id":0,"result":null}`)
		as := answers[m.Method]
		for i, a := range as {
			if sameJSON(a.params, m.Params) || i == len(as)-1 {
				reply = as[0].reply
				if sameJSON(a.params, m.Params) {
					reply = a.reply
				}
				break
			}
		}
		var out map[string]json.RawMessage
		_ = json.Unmarshal(reply, &out)
		r.mu.Lock()
		if res, ok := r.results[m.Method]; ok {
			out = map[string]json.RawMessage{"jsonrpc": json.RawMessage(`"2.0"`), "result": res}
		}
		held := r.holds[m.Method]
		r.mu.Unlock()
		if held != nil {
			<-held
		}
		out["id"], _ = json.Marshal(m.ID)
		b, _ := json.Marshal(out)
		write(b)
	}
}
