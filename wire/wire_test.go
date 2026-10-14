package wire

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// pair starts an app listening on loopback and returns its address and fingerprint: the path a remote worker takes.
func pair(t *testing.T, auth Authenticator) (addr, fingerprint string, accepted chan *Peer) {
	t.Helper()
	cert, fp, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted = make(chan *Peer, 1)
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			p, err := Accept(raw, ServerTLS(cert), auth)
			if err != nil {
				continue
			}
			accepted <- p
		}
	}()
	return ln.Addr().String(), fp, accepted
}

func allow() Authenticator {
	return AuthenticatorFunc(func(net.Addr, Hello) Welcome { return Welcome{Status: StatusOK} })
}

func hello(name string) Hello { return Hello{Name: name, Key: "k", OS: "linux", Arch: "amd64"} }

func dial(t *testing.T, addr, fp string, h Hello) (*Peer, Welcome, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return Dial(ctx, addr, fp, h)
}

func TestHelloRoundTrips(t *testing.T) {
	var got Hello
	var gotRemote net.Addr
	addr, fp, accepted := pair(t, AuthenticatorFunc(func(r net.Addr, h Hello) Welcome {
		got, gotRemote = h, r
		return Welcome{Status: StatusOK}
	}))
	p, w, err := dial(t, addr, fp, hello("devbox"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Conn.Close()
	<-accepted
	if w.Status != StatusOK {
		t.Fatalf("status = %q, want ok", w.Status)
	}
	if got.Name != "devbox" || got.Protocol != Protocol {
		t.Errorf("hello = %+v", got)
	}
	if h, _, _ := net.SplitHostPort(gotRemote.String()); h != "127.0.0.1" {
		t.Errorf("remote = %s, want the socket's address", gotRemote)
	}
}

func TestWrongFingerprintNeverSendsTheKey(t *testing.T) {
	var reached bool
	addr, _, _ := pair(t, AuthenticatorFunc(func(net.Addr, Hello) Welcome {
		reached = true
		return Welcome{Status: StatusOK}
	}))
	if _, _, err := dial(t, addr, strings.Repeat("ab", 32), hello("impostor")); err == nil {
		t.Fatal("a wrong pin connected")
	}
	if reached {
		t.Error("the hello reached the app despite a failed pin")
	}
}

func TestEmptyFingerprintIsRefusedOutright(t *testing.T) {
	if _, _, err := dial(t, "127.0.0.1:1", "", hello("x")); err == nil {
		t.Fatal("dialling with no pin was allowed")
	}
}

func TestProtocolMismatchIsRefusedBeforeTheAuthenticator(t *testing.T) {
	var reached bool
	addr, _, _ := pair(t, AuthenticatorFunc(func(net.Addr, Hello) Welcome {
		reached = true
		return Welcome{Status: StatusOK}
	}))
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tc := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	h := hello("old")
	h.Protocol = Protocol + 99
	if err := writeHandshake(tc, h); err != nil {
		t.Fatal(err)
	}
	var w Welcome
	if err := readHandshake(tc, &w); err != nil {
		t.Fatal(err)
	}
	if w.Status != StatusMismatch || w.Protocol != Protocol {
		t.Fatalf("welcome = %+v, want a mismatch saying what the app speaks", w)
	}
	if reached {
		t.Error("a mismatched worker reached the authenticator")
	}
}

func TestKeyAuth(t *testing.T) {
	keys := map[string]string{"devbox": "secret"}
	addr, fp, _ := pair(t, KeyAuth(func(n string) (string, bool) { k, ok := keys[n]; return k, ok }))
	h := hello("devbox")
	h.Key = "secret"
	p, _, err := dial(t, addr, fp, h)
	if err != nil {
		t.Fatal(err)
	}
	p.Conn.Close()
	h.Key = "wrong"
	_, w, err := dial(t, addr, fp, h)
	var rejected *ErrRejected
	if !errors.As(err, &rejected) || w.Status != StatusDenied {
		t.Fatalf("a wrong key got %+v, %v", w, err)
	}
}

func TestOversizedHelloIsRefused(t *testing.T) {
	addr, _, _ := pair(t, allow())
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tc := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.Write([]byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	_ = tc.SetReadDeadline(time.Now().Add(5 * time.Second))
	var w Welcome
	if readHandshake(tc, &w) == nil {
		t.Fatal("an oversized frame was answered")
	}
}

func TestWordsAreStableAndShort(t *testing.T) {
	a := Words("abc")
	if a != Words("abc") || a == Words("abd") {
		t.Fatal("words aren't stable, or don't tell fingerprints apart")
	}
	if n := strings.Count(a, "-"); n != 5 {
		t.Fatalf("got %q, want six words", a)
	}
}

func TestCertIsReusedAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	_, first, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("fingerprints %s then %s", first, second)
	}
}

type pt struct{ X, Y int }

func TestCallBothWays(t *testing.T) {
	a, b := Pipe()
	defer a.Close()
	defer b.Close()
	b.Handle("add", Func(func(_ context.Context, p pt) (int, error) { return p.X + p.Y, nil }))
	a.Handle("neg", Func(func(_ context.Context, n int) (int, error) { return -n, nil }))
	var sum, neg int
	if err := a.Call(context.Background(), "add", pt{2, 3}, &sum); err != nil || sum != 5 {
		t.Fatalf("add = %d, %v", sum, err)
	}
	if err := b.Call(context.Background(), "neg", 4, &neg); err != nil || neg != -4 {
		t.Fatalf("neg = %d, %v", neg, err)
	}
}

func TestCallErrors(t *testing.T) {
	a, b := Pipe()
	defer a.Close()
	defer b.Close()
	b.Handle("fail", Func(func(context.Context, struct{}) (any, error) { return nil, errors.New("no luck") }))
	b.Handle("panic", Func(func(context.Context, struct{}) (any, error) { panic("boom") }))
	var re *RemoteError
	if err := a.Call(context.Background(), "fail", nil, nil); !errors.As(err, &re) || re.Message != "no luck" {
		t.Fatalf("fail: %v", err)
	}
	if err := a.Call(context.Background(), "panic", nil, nil); !errors.As(err, &re) || !strings.Contains(re.Message, "boom") {
		t.Fatalf("panic: %v", err)
	}
	if err := a.Call(context.Background(), "nope", nil, nil); err == nil || !strings.Contains(err.Error(), ErrNoMethod.Error()) {
		t.Fatalf("nope: %v", err)
	}
	// The connection outlives a panicking handler.
	b.Handle("ok", Func(func(context.Context, struct{}) (string, error) { return "ok", nil }))
	var s string
	if err := a.Call(context.Background(), "ok", nil, &s); err != nil || s != "ok" {
		t.Fatalf("after a panic: %q, %v", s, err)
	}
}

func TestStream(t *testing.T) {
	a, b := Pipe()
	defer a.Close()
	defer b.Close()
	b.HandleStream("count", StreamFunc(func(_ context.Context, n int, send func(any) error) error {
		for i := range n {
			if err := send(i); err != nil {
				return err
			}
		}
		return nil
	}))
	s, err := a.Stream(context.Background(), "count", 3)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for {
		var v int
		if err := s.Recv(&v); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if len(got) != 3 || got[2] != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestClosingAStreamCancelsTheHandler(t *testing.T) {
	a, b := Pipe()
	defer a.Close()
	defer b.Close()
	stopped := make(chan struct{})
	b.HandleStream("forever", StreamFunc(func(ctx context.Context, _ struct{}, send func(any) error) error {
		defer close(stopped)
		_ = send(1)
		<-ctx.Done()
		return ctx.Err()
	}))
	s, err := a.Stream(context.Background(), "forever", nil)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.Recv(&v); err != nil {
		t.Fatal(err)
	}
	s.Close()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler kept running after its stream closed")
	}
}

func TestCallHonoursContext(t *testing.T) {
	a, b := Pipe()
	defer a.Close()
	defer b.Close()
	b.Handle("slow", Func(func(ctx context.Context, _ struct{}) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
}

func TestDroppedConnectionEndsStreams(t *testing.T) {
	a, b := Pipe()
	b.HandleStream("forever", StreamFunc(func(ctx context.Context, _ struct{}, send func(any) error) error {
		_ = send(1)
		<-ctx.Done()
		return nil
	}))
	s, err := a.Stream(context.Background(), "forever", nil)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	_ = s.Recv(&v)
	b.Close()
	done := make(chan error, 1)
	go func() { done <- s.Recv(&v) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stream on a dropped connection still delivered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stream on a dropped connection hung")
	}
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the other end never noticed")
	}
}
