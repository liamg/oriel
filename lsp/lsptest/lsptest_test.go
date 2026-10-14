package lsptest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/lsp/jsonrpc"
)

const sample = `{"server":"fake","version":"1.0"}
{"dir":"send","msg":{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":123,"rootUri":"file://${root}"}}}
{"dir":"recv","msg":{"jsonrpc":"2.0","method":"window/showMessage","params":{"type":3,"message":"hi"}}}
{"dir":"recv","msg":{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"hoverProvider":true}}}}
{"dir":"send","msg":{"jsonrpc":"2.0","method":"initialized","params":{}}}
{"dir":"recv","msg":{"jsonrpc":"2.0","id":"s1","method":"workspace/configuration","params":{"items":[{"section":"x"}]}}}
{"dir":"send","msg":{"jsonrpc":"2.0","id":"s1","result":[null]}}
{"dir":"send","msg":{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"file://${root}/a.go"},"position":{"line":0,"character":1}}}}
{"dir":"recv","msg":{"jsonrpc":"2.0","id":2,"result":{"contents":{"kind":"markdown","value":"in ${root}"}}}}
`

func TestParseWrite(t *testing.T) {
	tr, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Header.Server != "fake" || len(tr.Entries) != 8 || tr.Entries[0].Dir != Send || tr.Entries[1].Dir != Recv {
		t.Fatalf("transcript = %+v", tr)
	}
	var b bytes.Buffer
	if err := tr.Write(&b); err != nil {
		t.Fatal(err)
	}
	again, err := Parse(b.Bytes())
	if err != nil || len(again.Entries) != len(tr.Entries) || string(again.Entries[7].Msg) != string(tr.Entries[7].Msg) {
		t.Errorf("round trip = %+v, %v", again, err)
	}
	if _, err := Parse([]byte("{}\n{\"dir\":\"sideways\",\"msg\":{}}\n")); err == nil {
		t.Error("a bad direction parsed")
	}
}

// client drives a replay by hand, as a real client would.
func client(t *testing.T, f *Fake, root string, h jsonrpc.Handlers) *jsonrpc.Conn {
	p, err := f.Launcher()(context.Background(), lsp.ServerSpec{}, root, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return jsonrpc.NewConn(p, h)
}

func TestReplay(t *testing.T) {
	tr, _ := Parse([]byte(sample))
	f := NewFake(tr, 5*time.Second)
	shown := make(chan string, 1)
	c := client(t, f, "/live/root", jsonrpc.Handlers{
		Request: func(ctx context.Context, method string, params json.RawMessage) (any, error) { return []any{nil}, nil },
		Notify:  func(method string, params json.RawMessage) { shown <- method },
	})
	ctx := context.Background()
	// The process id differs from the recording's and is ignored; the root is the live one.
	if err := c.Call(ctx, "initialize", map[string]any{"processId": 999, "rootUri": "file:///live/root"}, nil); err != nil {
		t.Fatal(err)
	}
	if m := <-shown; m != "window/showMessage" {
		t.Errorf("notification = %s", m)
	}
	c.Notify("initialized", map[string]any{})
	var hover struct {
		Contents struct{ Value string }
	}
	if err := c.Call(ctx, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": "file:///live/root/a.go"},
		"position": map[string]any{"line": 0, "character": 1}}, &hover); err != nil {
		t.Fatal(err)
	}
	if hover.Contents.Value != "in /live/root" {
		t.Errorf("hover = %q", hover.Contents.Value)
	}
	if err := f.Wait(); err != nil {
		t.Errorf("replay: %v", err)
	}
}

func TestReplayMismatch(t *testing.T) {
	tr, _ := Parse([]byte(sample))
	f := NewFake(tr, 200*time.Millisecond)
	c := client(t, f, "/r", jsonrpc.Handlers{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c.Call(ctx, "initialize", map[string]any{"rootUri": "file:///elsewhere"}, nil)
	err := f.Wait()
	if err == nil || !strings.Contains(err.Error(), "initialize") {
		t.Errorf("replay of a different request = %v", err)
	}
}

// pipe is a Process over two buffers, for the recorder.
type pipe struct {
	in  io.Reader
	out bytes.Buffer
}

func (p *pipe) Read(b []byte) (int, error) {
	if len(b) > 7 {
		b = b[:7] // frames arrive in pieces
	}
	return p.in.Read(b)
}
func (p *pipe) Write(b []byte) (int, error) { return p.out.Write(b) }
func (p *pipe) Close() error                { return nil }
func (p *pipe) Wait() error                 { return nil }
func (p *pipe) Kill() error                 { return nil }

func TestRecorder(t *testing.T) {
	var server bytes.Buffer
	jsonrpc.WriteFrame(&server, []byte(`{"jsonrpc":"2.0","method":"window/logMessage","params":{"message":"/root/secret"}}`))
	jsonrpc.WriteFrame(&server, []byte(`{"jsonrpc":"2.0", "id":1, "result":{"uri":"file:///root/a.go"}}`))
	p := &pipe{in: &server}
	rec := &Recorder{}
	launch := rec.Launcher(func(context.Context, lsp.ServerSpec, string, io.Writer) (lsp.Process, error) { return p, nil })
	proc, err := launch(context.Background(), lsp.ServerSpec{}, "/root", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var req bytes.Buffer
	jsonrpc.WriteFrame(&req, []byte(`{"jsonrpc":"2.0","id":1,"method":"x","params":{"path":"/root/b"}}`))
	b := req.Bytes()
	proc.Write(b[:10])
	proc.Write(b[10:])
	io.ReadAll(proc)
	tr := rec.Transcript(Header{Server: "s"})
	var got []string
	for _, e := range tr.Entries {
		got = append(got, e.Dir+" "+string(e.Msg))
	}
	want := []string{
		`send {"jsonrpc":"2.0","id":1,"method":"x","params":{"path":"${root}/b"}}`,
		`recv {"jsonrpc":"2.0","id":1,"result":{"uri":"file://${root}/a.go"}}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("recorded:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if p.out.String() != req.String() {
		t.Errorf("the server got %q", p.out.String())
	}
}
