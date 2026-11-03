package claude

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/rpcagent"
	"github.com/liamg/oriel/internal/isolate"
)

// overRPC is Claude served over the Oriel agent protocol on a pipe, as a plugin would serve it: its PTY runs on
// Oriel's side of the pipe, and its hooks and tools reach the provider on the plugin's.
func overRPC(t *testing.T) agenttest.Harness {
	t.Helper()
	isolate.Test(t)
	h := fakeHarness(t)
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- rpcagent.Serve(br, bw, func(rpcagent.Init) (agent.Provider, error) { return h.Provider, nil })
	}()
	c, err := rpcagent.Dial(context.Background(), rpcagent.Options{Name: "claude", Dial: rpcagent.Pipe(ar, aw)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.(interface{ Close() error }).Close()
		bw.Close()
		<-done
	})
	h.Provider = c
	return h
}

func TestConformanceOverRPC(t *testing.T) {
	agenttest.Run(t, overRPC(t))
}

// Claude's terminal survives the round trip: what it showed comes back, and what's typed reaches it.
func TestTerminalOverRPC(t *testing.T) {
	h := overRPC(t)
	s := agenttest.Start(t, h)
	s.Send("say hello")
	s.Reply()
	term := s.S.Terminal()
	if term == nil {
		t.Fatal("no terminal over the protocol")
	}
	var shown string
	for deadline := time.Now().Add(h.Timeout); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		b, _, detach := term.Attach()
		detach()
		if shown = string(b); strings.Contains(shown, "fake claude "+s.S.ID()) {
			break
		}
	}
	if !strings.Contains(shown, "fake claude "+s.S.ID()) {
		t.Fatalf("the terminal showed %q", shown)
	}
	if err := term.Resize(120, 40); err != nil {
		t.Error(err)
	}
	if _, err := term.Write([]byte("x")); err != nil {
		t.Error(err)
	}
}
