package dap_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/dap/daptest"
)

// TestMain lets the test binary stand in for an adapter (ORIEL_DAP_FAKE), so each transport runs a real process.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ORIEL_DAP_FAKE"); mode != "" {
		fakeAdapter(mode, os.Args[len(os.Args)-1])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeAdapter speaks just enough DAP over stdio, or TCP either way, for the transport tests.
func fakeAdapter(mode, addr string) {
	var rw io.ReadWriter
	switch mode {
	case "stdio":
		rw = struct {
			io.Reader
			io.Writer
		}{os.Stdin, os.Stdout}
	case "tcp":
		ln, err := net.Listen("tcp", "127.0.0.1:"+addr)
		if err != nil {
			os.Exit(2)
		}
		c, err := ln.Accept()
		if err != nil {
			os.Exit(2)
		}
		rw = c
	case "reverse":
		c, err := net.Dial("tcp", addr)
		if err != nil {
			os.Exit(2)
		}
		rw = c
	}
	r := bufio.NewReader(rw)
	seq := 0
	send := func(m map[string]any) {
		seq++
		m["seq"] = seq
		b, _ := json.Marshal(m)
		dap.WriteFrame(rw, b)
	}
	reply := func(req dap.Message, ok bool, body any, msg string) {
		send(map[string]any{"type": "response", "request_seq": req.Seq, "command": req.Command, "success": ok, "body": body, "message": msg})
	}
	for {
		b, err := dap.ReadFrame(r)
		if err != nil {
			return
		}
		var m dap.Message
		json.Unmarshal(b, &m)
		switch {
		case m.Type == "response":
			send(map[string]any{"type": "event", "event": "output", "body": map[string]any{"category": "console",
				"output": "runInTerminal success=" + map[bool]string{true: "true", false: "false"}[m.Success] + "\n"}})
		case m.Command == "initialize":
			reply(m, true, map[string]any{"supportsConfigurationDoneRequest": true}, "")
		case m.Command == "launch":
			send(map[string]any{"type": "event", "event": "initialized"})
			reply(m, true, nil, "")
		case m.Command == "configurationDone":
			reply(m, true, nil, "")
			send(map[string]any{"type": "request", "command": "runInTerminal", "arguments": map[string]any{"args": []string{"x"}}})
		case m.Command == "evaluate":
			reply(m, false, map[string]any{"error": map[string]any{"id": 1, "format": "no {name} here", "variables": map[string]string{"name": "x"}}},
				"failed")
		case m.Command == "disconnect":
			reply(m, true, nil, "")
			return
		default:
			reply(m, false, nil, "unknown "+m.Command)
		}
	}
}

func TestTransports(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		transport string
		last      string
	}{{dap.TransportStdio, "-"}, {dap.TransportTCP, "${port}"}, {dap.TransportReverse, "${addr}"}} {
		t.Run(tc.transport, func(t *testing.T) {
			t.Setenv("ORIEL_DAP_FAKE", map[string]string{dap.TransportStdio: "stdio", dap.TransportTCP: "tcp", dap.TransportReverse: "reverse"}[tc.transport])
			spec := dap.AdapterSpec{Name: "fake", Type: "fake", Cmd: []string{self, "-test.run=^$", tc.last}, Transport: tc.transport}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out := make(chan dap.Output, 4)
			c, err := dap.Start(ctx, spec, dap.LaunchConfig{Type: "fake"}, dap.Options{OnEvent: func(e dap.Event) {
				if o, ok := e.(dap.Output); ok {
					out <- o
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			// ref: dap/specification.md#Reverse_Requests_RunInTerminal
			// quote: "This request should only be called if the corresponding client capability `supportsRunInTerminalRequest` is true."
			select {
			case o := <-out:
				if o.Text != "runInTerminal success=false\n" {
					t.Errorf("the adapter saw %q, want the reverse request refused", o.Text)
				}
			case <-ctx.Done():
				t.Fatal("no answer to the reverse request")
			}
			// ref: dap/specification.md#Base_Protocol_ErrorResponse
			// quote: "On error (whenever `success` is false), the body can provide more details."
			_, err = c.Evaluate(ctx, 0, "x", dap.ContextREPL)
			var de *dap.Error
			if !errors.As(err, &de) || de.Message != "no x here" {
				t.Errorf("evaluate error = %v, want the formatted message", err)
			}
			if err := c.Stop(ctx); err != nil {
				t.Errorf("stop: %v", err)
			}
			if _, err := c.Threads(ctx); !errors.Is(err, dap.ErrClosed) {
				t.Errorf("a request after stop = %v, want ErrClosed", err)
			}
		})
	}
}

func TestConfigFromMap(t *testing.T) {
	c, err := dap.ConfigFromMap(map[string]any{"name": "server", "type": "go", "program": "./cmd/server", "args": []any{"--port", int64(8080)},
		"env": map[string]any{"A": "1"}, "buildFlags": "-tags=dev"}, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	want := dap.LaunchConfig{Name: "server", Type: "go", Program: "/repo/cmd/server", Args: []string{"--port", "8080"}, Env: map[string]string{"A": "1"},
		Extra: map[string]any{"buildFlags": "-tags=dev"}}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("config = %+v, want %+v", c, want)
	}
	spec := dap.Builtins()[0]
	args := c.Arguments(spec)
	if args["mode"] != "debug" || args["program"] != "/repo/cmd/server" || args["buildFlags"] != "-tags=dev" {
		t.Errorf("launch arguments = %v", args)
	}
	a, _ := dap.ConfigFromMap(map[string]any{"type": "go", "request": "attach", "pid": int64(42)}, "")
	if args := a.Arguments(spec); args["mode"] != "local" || args["processId"] != 42 {
		t.Errorf("attach arguments = %v", args)
	}
	for _, bad := range []map[string]any{{"name": "x"}, {"type": "go", "request": "run"}, {"type": "go", "args": "x"}, {"type": "go", "pid": "x"}} {
		if _, err := dap.ConfigFromMap(bad, ""); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

func TestInferGo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("// x\npackage main\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package lib\n"), 0o644)
	infer := dap.Builtins()[0].Infer
	for file, mode := range map[string]string{"main.go": "debug", "lib.go": "test", "main_test.go": "test"} {
		c, ok := infer(filepath.Join(dir, file))
		if !ok || c.Extra["mode"] != mode || c.Program != dir {
			t.Errorf("%s: %+v, want mode %s", file, c, mode)
		}
	}
	if _, ok := infer(filepath.Join(dir, "x.py")); ok {
		t.Error("dlv infers a config for a Python file")
	}
}

// TestRequestsMatchSchema checks every request the client sent in the recordings against the protocol's JSON
// schema: a known command, with its required arguments and no unknown ones (launch and attach take the adapter's).
func TestRequestsMatchSchema(t *testing.T) {
	b, err := os.ReadFile("../spec/dap/debugAdapterProtocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	json.Unmarshal(b, &schema)
	type obj struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	def := func(name string) (obj, bool) {
		var o obj
		raw, ok := schema.Definitions[name]
		return o, ok && json.Unmarshal(raw, &o) == nil
	}
	for _, fx := range fixtures {
		tr, err := daptest.Load(filepath.Join("testdata", "transcripts", fx.name+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range tr.Entries {
			var m dap.Message
			json.Unmarshal(e.Msg, &m)
			if e.Dir != daptest.Send || m.Type != "request" {
				continue
			}
			name := strings.ToUpper(m.Command[:1]) + m.Command[1:]
			if _, ok := schema.Definitions[name+"Request"]; !ok {
				t.Errorf("%s: no %sRequest in the schema", fx.name, name)
				continue
			}
			args, ok := def(name + "Arguments")
			if !ok || m.Command == "launch" || m.Command == "attach" {
				continue
			}
			var got map[string]any
			json.Unmarshal(m.Arguments, &got)
			for _, k := range args.Required {
				if _, ok := got[k]; !ok {
					t.Errorf("%s: %s lacks %s", fx.name, m.Command, k)
				}
			}
			for k := range got {
				if _, ok := args.Properties[k]; !ok {
					t.Errorf("%s: %s has %s, which the schema doesn't", fx.name, m.Command, k)
				}
			}
		}
	}
}

// waited is an adapter that answers initialize with a failure, and records whether it was waited for.
type waited struct {
	net.Conn
	waited chan struct{}
}

func (p *waited) Kill() error { return nil }
func (p *waited) Wait() error { close(p.waited); return nil }

// A Start that fails reaps the adapter it launched.
func TestFailedStartWaitsForTheAdapter(t *testing.T) {
	a, b := net.Pipe()
	p := &waited{Conn: a, waited: make(chan struct{})}
	go func() {
		r := bufio.NewReader(b)
		body, err := dap.ReadFrame(r)
		if err != nil {
			return
		}
		var m dap.Message
		json.Unmarshal(body, &m)
		res, _ := json.Marshal(map[string]any{"seq": 1, "type": "response", "request_seq": m.Seq, "command": m.Command, "success": false, "message": "no"})
		dap.WriteFrame(b, res)
		io.Copy(io.Discard, b)
	}()
	launch := func(context.Context, dap.AdapterSpec, string, io.Writer) (dap.Process, error) { return p, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := dap.Start(ctx, dap.AdapterSpec{Name: "fake", Type: "fake"}, dap.LaunchConfig{}, dap.Options{Launch: launch}); err == nil {
		t.Fatal("Start succeeded")
	}
	select {
	case <-p.waited:
	default:
		t.Error("the adapter was never waited for")
	}
}
