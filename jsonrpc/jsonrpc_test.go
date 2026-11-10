package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// pair is two connections joined by pipes.
func pair(t *testing.T, a, b Handlers, opt Options) (*Conn, *Conn) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	ca := NewConn(ar, aw, a, opt)
	cb := NewConn(br, bw, b, opt)
	t.Cleanup(func() { ca.Close(); cb.Close() })
	return ca, cb
}

func TestCallAndReply(t *testing.T) {
	server := Handlers{Request: func(r *Request) {
		switch r.Method {
		case "add":
			var p struct{ A, B int }
			json.Unmarshal(r.Params, &p)
			r.Reply(map[string]int{"sum": p.A + p.B}, nil)
		case "fail":
			r.Reply(nil, &Error{Code: InvalidParams, Message: "no"})
		case "boom":
			r.Reply(nil, errors.New("boom"))
		default:
			r.Reply(nil, &Error{Code: MethodNotFound, Message: "no such method"})
		}
	}}
	c, _ := pair(t, Handlers{}, server, Options{})
	var res struct{ Sum int }
	if err := c.Call(context.Background(), "add", map[string]int{"a": 2, "b": 3}, &res); err != nil || res.Sum != 5 {
		t.Fatalf("add = %+v, %v", res, err)
	}
	var e *Error
	if err := c.Call(context.Background(), "fail", nil, nil); !errors.As(err, &e) || e.Code != InvalidParams {
		t.Errorf("fail = %v", err)
	}
	if err := c.Call(context.Background(), "boom", nil, nil); !errors.As(err, &e) || e.Code != InternalError || e.Message != "boom" {
		t.Errorf("boom = %v", err)
	}
}

func TestNoRequestHandlerAnswersMethodNotFound(t *testing.T) {
	c, _ := pair(t, Handlers{}, Handlers{}, Options{})
	var e *Error
	if err := c.Call(context.Background(), "x", nil, nil); !errors.As(err, &e) || e.Code != MethodNotFound {
		t.Errorf("err = %v", err)
	}
}

// A request answered later doesn't hold up the notifications behind it, and they arrive in order.
func TestReplyLaterKeepsNotificationsFlowing(t *testing.T) {
	held := make(chan *Request, 1)
	got := make(chan string, 10)
	client := Handlers{
		Request: func(r *Request) { got <- "request"; held <- r },
		Notify:  func(m string, _ json.RawMessage) { got <- m },
	}
	_, server := pair(t, client, Handlers{}, Options{})
	answer := make(chan error, 1)
	go func() { answer <- server.Call(context.Background(), "approve", nil, nil) }()
	r := <-held
	server.Notify("one", nil)
	server.Notify("two", nil)
	for _, want := range []string{"request", "one", "two"} {
		select {
		case m := <-got:
			if m != want {
				t.Fatalf("got %q, want %q", m, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("waiting for %q", want)
		}
	}
	if err := r.Reply("ok", nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Reply("again", nil); err == nil {
		t.Error("a second reply was sent")
	}
	if err := <-answer; err != nil {
		t.Fatal(err)
	}
}

func TestCallsFromBothSides(t *testing.T) {
	var srv *Conn
	server := Handlers{Request: func(r *Request) {
		go func() {
			var s string
			err := srv.Call(context.Background(), "inner", nil, &s)
			r.Reply("outer+"+s, err)
		}()
	}}
	client := Handlers{Request: func(r *Request) { r.Reply("inner", nil) }}
	c, sc := pair(t, client, server, Options{})
	srv = sc
	var s string
	if err := c.Call(context.Background(), "outer", nil, &s); err != nil || s != "outer+inner" {
		t.Fatalf("s = %q, %v", s, err)
	}
}

// Each message is one line; "jsonrpc" is written only when the options say so.
func TestWireFormat(t *testing.T) {
	for _, version := range []string{"", "2.0"} {
		r, w := io.Pipe()
		idle, _ := io.Pipe() // nothing to read, and no EOF to close the connection before it writes
		c := NewConn(idle, w, Handlers{}, Options{Version: version})
		go c.Notify("initialized", map[string]any{})
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		want := `{"method":"initialized","params":{}}` + "\n"
		if version != "" {
			want = `{"jsonrpc":"2.0","method":"initialized","params":{}}` + "\n"
		}
		if line != want {
			t.Errorf("version %q: wrote %q, want %q", version, line, want)
		}
		c.Close()
	}
}

// Messages from the other side may carry "jsonrpc" or not, and ids may be strings.
func TestReadsEitherFormAndStringIDs(t *testing.T) {
	in := strings.NewReader(`{"id":"a","method":"ping"}` + "\n" + `not json` + "\n" + `{"jsonrpc":"2.0","method":"note","params":{"x":1}}` + "\n")
	out := &strings.Builder{}
	notes := make(chan string, 1)
	c := NewConn(in, writerFunc(func(b []byte) (int, error) { return out.Write(b) }),
		Handlers{Request: func(r *Request) { r.Reply(map[string]any{}, nil) }, Notify: func(m string, p json.RawMessage) { notes <- m + string(p) }},
		Options{})
	<-c.Done()
	if got := <-notes; got != `note{"x":1}` {
		t.Errorf("note = %q", got)
	}
	if got := out.String(); got != `{"id":"a","result":{}}`+"\n" {
		t.Errorf("reply = %q", got)
	}
}

type writerFunc func([]byte) (int, error)

// yield lets every other goroutine run a while, so what races the caller gets ahead of it.
func yield() {
	for range 100 {
		runtime.Gosched()
	}
}

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func TestCloseFailsPendingCalls(t *testing.T) {
	arrived := make(chan struct{})
	c, _ := pair(t, Handlers{}, Handlers{Request: func(r *Request) { close(arrived) }}, Options{})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "never", nil, nil) }()
	<-arrived
	c.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call is still waiting")
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("after close, err = %v", err)
	}
}

func TestEOFEndsTheConnection(t *testing.T) {
	r, w := io.Pipe()
	c := NewConn(r, io.Discard, Handlers{}, Options{})
	w.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("not done")
	}
	if !errors.Is(c.Err(), ErrClosed) {
		t.Errorf("err = %v", c.Err())
	}
}

func TestCallContext(t *testing.T) {
	c, _ := pair(t, Handlers{}, Handlers{Request: func(r *Request) {}}, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

// Requests waiting on a reply are bounded: past maxRequests the rest are refused, and an answer frees a slot.
func TestRequestsInFlightAreBounded(t *testing.T) {
	held := make(chan *Request, maxRequests+1)
	client, _ := pair(t, Handlers{}, Handlers{Request: func(r *Request) { held <- r }}, Options{})
	for range maxRequests {
		go client.Call(context.Background(), "wait", nil, nil)
	}
	var first *Request
	for i := range maxRequests {
		select {
		case r := <-held:
			if i == 0 {
				first = r
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d requests reached the handler", i, maxRequests)
		}
	}
	var e *Error
	if err := client.Call(context.Background(), "over", nil, nil); !errors.As(err, &e) || e.Code != Overloaded {
		t.Fatalf("over the limit = %v", err)
	}
	if len(held) != 0 {
		t.Fatal("a refused request reached the handler")
	}
	first.Reply(nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { (<-held).Reply("ok", nil) }()
	var got string
	if err := client.Call(ctx, "after", nil, &got); err != nil || got != "ok" {
		t.Fatalf("after a reply = %q, %v", got, err)
	}
}

// The other side's cancel notification ends the request's Context, and reaches Notify too.
func TestCancelEndsTheRequestContext(t *testing.T) {
	cancel := &Cancel{Method: "notifications/cancelled", Field: "requestId"}
	ended := make(chan error, 1)
	notes := make(chan string, 1)
	server := Handlers{
		Request: func(r *Request) {
			go func() {
				<-r.Context().Done()
				ended <- r.Context().Err()
				r.Reply(nil, r.Context().Err())
			}()
		},
		Notify: func(m string, _ json.RawMessage) { notes <- m },
	}
	c, _ := pair(t, Handlers{}, server, Options{Cancel: cancel})
	ctx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if err := c.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the request's context ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request's context didn't end")
	}
	select {
	case m := <-notes:
		if m != cancel.Method {
			t.Errorf("Notify got %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Error("Notify didn't get the cancel")
	}
}

// A server answers what isn't JSON with ParseError and a null id.
func TestParseErrors(t *testing.T) {
	in := strings.NewReader("not json\n")
	out := &strings.Builder{}
	c := NewConn(in, writerFunc(func(b []byte) (int, error) { return out.Write(b) }), Handlers{},
		Options{Version: "2.0", ParseErrors: true})
	<-c.Done()
	if got, want := out.String(), `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`+"\n"; got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

// A reply sent just before Close is still written.
func TestCloseWritesWhatsQueued(t *testing.T) {
	r, w := io.Pipe()
	idle, _ := io.Pipe()
	c := NewConn(idle, w, Handlers{}, Options{})
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		got <- line
	}()
	c.Notify("bye", nil)
	c.Close()
	select {
	case line := <-got:
		if line != `{"method":"bye"}`+"\n" {
			t.Errorf("wrote %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing written")
	}
}

// Without Queue, a send returns once it's written: a plugin may answer and exit.
func TestSendReturnsOnceWritten(t *testing.T) {
	var mu sync.Mutex
	var out strings.Builder
	idle, _ := io.Pipe()
	c := NewConn(idle, writerFunc(func(b []byte) (int, error) {
		yield() // a slow write: an unwaited one would land after Notify returned
		mu.Lock()
		defer mu.Unlock()
		return out.Write(b)
	}), Handlers{}, Options{})
	defer c.Close()
	c.Notify("one", nil)
	c.Notify("two", nil)
	mu.Lock()
	defer mu.Unlock()
	if got := out.String(); got != `{"method":"one"}`+"\n"+`{"method":"two"}`+"\n" {
		t.Errorf("written when Notify returned = %q", got)
	}
}

// ref:   acp/docs/prompt-turn.mdx#Cancellation
// quote: "it **MUST** ensure that it does so before responding to the `session/prompt` request"
// InOrder: a call's result reaches its caller after the notifications sent before it have been handled.
func TestInOrderResultsFollowTheirNotifications(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	client := Handlers{Notify: func(method string, _ json.RawMessage) {
		yield()
		mu.Lock()
		seen = append(seen, method)
		mu.Unlock()
	}}
	var server *Conn
	serverH := Handlers{Request: func(r *Request) {
		for i := 0; i < 20; i++ {
			server.Notify("update", nil)
		}
		r.Reply("done", nil)
	}}
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	c := NewConn(ar, aw, client, Options{InOrder: true})
	server = NewConn(br, bw, serverH, Options{})
	t.Cleanup(func() { c.Close(); server.Close() })
	if err := c.Call(context.Background(), "prompt", nil, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 20 {
		t.Errorf("the result came after %d of its 20 notifications", len(seen))
	}
}
