package dap

import "testing"

// A request whose arguments can't be encoded leaves nothing waiting on a response.
func TestSendMarshalErrorForgetsTheRequest(t *testing.T) {
	c := &Client{pending: map[int]chan *Message{}, done: make(chan struct{})}
	if _, err := c.send("evaluate", func() {}); err == nil {
		t.Fatal("send took arguments it can't encode")
	}
	if len(c.pending) != 0 {
		t.Errorf("pending = %v, want empty", c.pending)
	}
}
