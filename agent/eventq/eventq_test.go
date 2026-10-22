package eventq

import (
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/internal/isolate"
)

// Pushing never waits on a reader, and what was pushed comes out in order before the channel closes.
func TestQueue(t *testing.T) {
	isolate.Test(t)
	q := New()
	done := make(chan struct{})
	go func() {
		for i := range 1000 {
			q.Push(agent.Ready{ID: string(rune('a' + i%26))})
		}
		q.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Push waited for a reader")
	}
	n := 0
	for ev := range q.Out() {
		if ev.(agent.Ready).ID != string(rune('a'+n%26)) {
			t.Fatalf("event %d out of order: %+v", n, ev)
		}
		n++
	}
	if n != 1000 {
		t.Errorf("%d events came out, want 1000", n)
	}
	q.Push(agent.Ready{})
}
