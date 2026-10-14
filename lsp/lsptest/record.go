package lsptest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/lsp/jsonrpc"
)

// Recorder sits between a client and a real server and keeps the messages that pass, in the order the
// client sent and read them.
type Recorder struct {
	// Drop leaves out notifications by method: log and telemetry noise that can name the machine.
	Drop map[string]bool

	mu      sync.Mutex
	root    string
	entries []Entry
}

// DefaultDrop are the notifications a Recorder leaves out unless told otherwise.
var DefaultDrop = map[string]bool{"window/logMessage": true, "telemetry/event": true, "$/logTrace": true}

// Launcher wraps launch so the processes it starts are recorded.
func (r *Recorder) Launcher(launch lsp.Launcher) lsp.Launcher {
	return func(ctx context.Context, spec lsp.ServerSpec, root string, stderr io.Writer) (lsp.Process, error) {
		p, err := launch(ctx, spec, root, stderr)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.root = root
		r.mu.Unlock()
		return &recorded{Process: p, r: r}, nil
	}
}

// Transcript is what has been recorded, under h.
func (r *Recorder) Transcript(h Header) *Transcript {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Transcript{Header: h, Entries: append([]Entry(nil), r.entries...)}
}

func (r *Recorder) add(dir string, body []byte) {
	drop := r.Drop
	if drop == nil {
		drop = DefaultDrop
	}
	var m jsonrpc.Message
	if json.Unmarshal(body, &m) == nil && m.IsNotification() && drop[m.Method] {
		return
	}
	var buf bytes.Buffer
	if json.Compact(&buf, body) != nil {
		buf.Reset()
		buf.Write(body)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, Entry{Dir: dir, Msg: abstract(buf.Bytes(), r.root)})
}

type recorded struct {
	lsp.Process
	r *Recorder

	wmu  sync.Mutex
	wbuf []byte
	rmu  sync.Mutex
	rbuf []byte
}

func (p *recorded) Write(b []byte) (int, error) {
	p.wmu.Lock()
	p.wbuf = frames(append(p.wbuf, b...), func(body []byte) { p.r.add(Send, body) })
	p.wmu.Unlock()
	return p.Process.Write(b)
}

func (p *recorded) Read(b []byte) (int, error) {
	n, err := p.Process.Read(b)
	if n > 0 {
		p.rmu.Lock()
		p.rbuf = frames(append(p.rbuf, b[:n]...), func(body []byte) { p.r.add(Recv, body) })
		p.rmu.Unlock()
	}
	return n, err
}

// frames calls fn with each whole frame in buf and returns what's left.
func frames(buf []byte, fn func([]byte)) []byte {
	for {
		br := bytes.NewReader(buf)
		r := bufio.NewReader(br)
		body, err := jsonrpc.ReadFrame(r)
		if err != nil {
			return buf
		}
		fn(body)
		buf = buf[len(buf)-br.Len()-r.Buffered():]
	}
}
