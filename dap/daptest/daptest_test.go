package daptest

import (
	"bufio"
	"context"
	"encoding/json"
	"testing"

	"github.com/liamg/oriel/dap"
)

// A Responder answers each request, whatever order it's asked in, with the response recorded for it, the live seq in
// request_seq; one with nothing recorded gets an error response and is reported.
func TestResponderAnswersInAnyOrder(t *testing.T) {
	tr, err := Parse([]byte(`{"adapter":"x","version":"1"}
{"dir":"send","msg":{"seq":1,"type":"request","command":"threads"}}
{"dir":"send","msg":{"seq":2,"type":"request","command":"evaluate","arguments":{"expression":"${root}"}}}
{"dir":"recv","msg":{"seq":1,"type":"response","request_seq":2,"success":true,"command":"evaluate","body":{"result":"${root}"}}}
{"dir":"recv","msg":{"seq":2,"type":"response","request_seq":1,"success":true,"command":"threads","body":{"threads":[]}}}
`))
	if err != nil {
		t.Fatal(err)
	}
	r := NewResponder(tr, Vars{RootVar: "/r"})
	p, err := r.Launcher()(context.Background(), dap.AdapterSpec{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rd := bufio.NewReader(p)
	ask := func(seq int, command string, args any) dap.Message {
		b, _ := json.Marshal(map[string]any{"seq": seq, "type": "request", "command": command, "arguments": args})
		dap.WriteFrame(p, b)
		body, err := dap.ReadFrame(rd)
		if err != nil {
			t.Fatal(err)
		}
		var m dap.Message
		json.Unmarshal(body, &m)
		return m
	}
	if m := ask(7, "evaluate", map[string]any{"expression": "/r"}); m.RequestSeq != 7 || string(m.Body) != `{"result":"/r"}` {
		t.Errorf("evaluate got %+v", m)
	}
	if m := ask(8, "threads", nil); m.RequestSeq != 8 || m.Command != "threads" || !m.Success {
		t.Errorf("threads got %+v", m)
	}
	if m := ask(9, "pause", nil); m.Success || r.Err() == nil {
		t.Errorf("an unrecorded request got %+v, err %v", m, r.Err())
	}
}
