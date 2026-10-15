package daptest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/liamg/oriel/dap"
)

// Recorder sits between a client and a real adapter and keeps the messages that pass, in the order the client sent
// and read them.
type Recorder struct {
	Vars Vars
	// Drop leaves messages out: telemetry, which names the machine and changes from run to run.
	Drop func(m dap.Message) bool

	mu      sync.Mutex
	entries []Entry
}

// DropTelemetry leaves out telemetry output events.
func DropTelemetry(m dap.Message) bool {
	if m.Type != "event" || m.Event != "output" {
		return false
	}
	var b struct {
		Category string `json:"category"`
	}
	json.Unmarshal(m.Body, &b)
	return b.Category == "telemetry"
}

// Launcher wraps launch so the adapters it starts are recorded.
func (r *Recorder) Launcher(launch dap.Launcher) dap.Launcher {
	return func(ctx context.Context, spec dap.AdapterSpec, dir string, stderr io.Writer) (dap.Process, error) {
		p, err := launch(ctx, spec, dir, stderr)
		if err != nil {
			return nil, err
		}
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
		drop = DropTelemetry
	}
	var m dap.Message
	if json.Unmarshal(body, &m) == nil && drop(m) {
		return
	}
	var buf bytes.Buffer
	if json.Compact(&buf, body) != nil {
		buf.Reset()
		buf.Write(body)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, Entry{Dir: dir, Msg: r.Vars.abstract(buf.Bytes())})
}

type recorded struct {
	dap.Process
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
		body, err := dap.ReadFrame(r)
		if err != nil {
			return buf
		}
		fn(body)
		buf = buf[len(buf)-br.Len()-r.Buffered():]
	}
}
