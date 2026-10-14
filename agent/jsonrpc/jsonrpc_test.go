package jsonrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func TestCloseFailsPendingCalls(t *testing.T) {
	c, _ := pair(t, Handlers{}, Handlers{Request: func(r *Request) {}}, Options{})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "never", nil, nil) }()
	time.Sleep(20 * time.Millisecond)
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
