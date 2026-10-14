package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type deciderFunc func(ctx context.Context, host string, port int) Decision

func (f deciderFunc) Decide(ctx context.Context, host string, port int) Decision {
	return f(ctx, host, port)
}

func always(d Decision) Decider {
	return deciderFunc(func(context.Context, string, int) Decision { return d })
}

// events collects Log calls.
type events struct {
	ch chan Event
}

func newEvents() *events { return &events{ch: make(chan Event, 16)} }

func (e *events) log(ev Event) { e.ch <- ev }

func (e *events) next(t *testing.T) Event {
	t.Helper()
	select {
	case ev := <-e.ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no log event")
		return Event{}
	}
}

// countingDial records dialled addresses.
type countingDial struct {
	mu    sync.Mutex
	addrs []string
	to    string // when set, every dial goes here instead
}

func (d *countingDial) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.addrs = append(d.addrs, addr)
	d.mu.Unlock()
	if d.to != "" {
		addr = d.to
	}
	var nd net.Dialer
	return nd.DialContext(ctx, network, addr)
}

func (d *countingDial) dialled() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addrs...)
}

func startProxy(t *testing.T, p *Proxy) *url.URL {
	t.Helper()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u
}

func proxyClient(proxy *url.URL, tlsCfg *tls.Config) (*http.Client, *http.Transport) {
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: tlsCfg}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}, tr
}

func hostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, _ := url.Parse(raw)
	port, _ := strconv.Atoi(u.Port())
	return u.Hostname(), port
}

func TestConnectAllowed(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	defer target.Close()
	host, port := hostPort(t, target.URL)

	var gotHost string
	var gotPort int
	ev := newEvents()
	p := &Proxy{
		Decider: deciderFunc(func(_ context.Context, h string, pt int) Decision {
			gotHost, gotPort = h, pt
			return Allow
		}),
		Log: ev.log,
	}
	client, tr := proxyClient(startProxy(t, p), target.Client().Transport.(*http.Transport).TLSClientConfig)

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
	if gotHost != host || gotPort != port {
		t.Fatalf("decided %s:%d, want %s:%d", gotHost, gotPort, host, port)
	}
	tr.CloseIdleConnections()

	e := ev.next(t)
	if !e.Allowed || e.Method != http.MethodConnect || e.Host != host || e.Port != port {
		t.Fatalf("event = %+v", e)
	}
	if e.BytesUp == 0 || e.BytesDown == 0 {
		t.Fatalf("byte counts not recorded: %+v", e)
	}
	if e.Proto != "tls" || e.Status != 0 || e.Path != "" {
		t.Errorf("a TLS tunnel is opaque but named: %+v", e)
	}
}

func TestConnectDenied(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()

	ev := newEvents()
	d := &countingDial{}
	p := &Proxy{Decider: always(Deny), Log: ev.log, Dial: d.dial}
	proxy := startProxy(t, p)
	client, _ := proxyClient(proxy, target.Client().Transport.(*http.Transport).TLSClientConfig)

	if _, err := client.Get(target.URL); err == nil {
		t.Fatal("expected an error")
	}

	// Raw CONNECT to see the status and body.
	conn, err := net.Dial("tcp", proxy.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = fmt.Fprintf(conn, "CONNECT Example.COM.:443 HTTP/1.1\r\nHost: Example.COM.:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "oriel: example.com is not allowed for this session") {
		t.Fatalf("body = %q", body)
	}

	for range 2 {
		if e := ev.next(t); e.Allowed {
			t.Fatalf("event allowed: %+v", e)
		}
	}
	if got := d.dialled(); len(got) != 0 {
		t.Fatalf("dialled %v", got)
	}
}

func TestHTTPForward(t *testing.T) {
	var gotHeader http.Header
	var gotHost string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader, gotHost = r.Header.Clone(), r.Host
		w.Header().Set("Connection", "X-Resp-Hop")
		w.Header().Set("X-Resp-Hop", "1")
		w.Header().Set("X-Kept", "yes")
		_, _ = io.WriteString(w, "path="+r.URL.Path)
	}))
	defer target.Close()

	ev := newEvents()
	p := &Proxy{Decider: always(Allow), Log: ev.log}
	proxy := startProxy(t, p)

	req, _ := http.NewRequest(http.MethodGet, target.URL+"/some/path", nil)
	req.Header.Set("Connection", "X-Custom-Hop")
	req.Header.Set("X-Custom-Hop", "1")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Authorization", "Basic Zm9vOmJhcg==")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("X-End-To-End", "1")
	client, _ := proxyClient(proxy, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "path=/some/path" {
		t.Fatalf("body = %q", body)
	}
	if resp.Header.Get("X-Resp-Hop") != "" || resp.Header.Get("X-Kept") != "yes" {
		t.Fatalf("response headers = %v", resp.Header)
	}
	for _, h := range []string{"X-Custom-Hop", "Keep-Alive", "Proxy-Authorization", "Upgrade", "Proxy-Connection"} {
		if gotHeader.Get(h) != "" {
			t.Errorf("%s reached target", h)
		}
	}
	if gotHeader.Get("X-End-To-End") != "1" {
		t.Error("end-to-end header dropped")
	}
	if want := strings.TrimPrefix(target.URL, "http://"); gotHost != want {
		t.Errorf("Host = %q, want %q", gotHost, want)
	}

	e := ev.next(t)
	if !e.Allowed || e.Method != http.MethodGet || e.BytesDown != int64(len(body)) {
		t.Fatalf("event = %+v", e)
	}
	if e.Proto != "http" || e.Path != "/some/path" || e.Status != http.StatusOK {
		t.Errorf("plain HTTP is logged with its path and status: %+v", e)
	}
}

func TestHTTPDenied(t *testing.T) {
	var hit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	defer target.Close()

	ev := newEvents()
	p := &Proxy{Decider: always(Deny), Log: ev.log}
	client, _ := proxyClient(startProxy(t, p), nil)
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if hit.Load() {
		t.Fatal("target was reached")
	}
	if e := ev.next(t); e.Allowed {
		t.Fatalf("event = %+v", e)
	}
}

func TestBadRequests(t *testing.T) {
	p := &Proxy{Decider: always(Allow)}
	proxy := startProxy(t, p)

	resp, err := http.Get("http://" + proxy.Host + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("origin-form status = %d", resp.StatusCode)
	}

	for _, target := range []string{"example.com:0", "example.com:70000", "example.com"} {
		conn, err := net.Dial("tcp", proxy.Host)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
		_ = conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("CONNECT %s status = %d", target, resp.StatusCode)
		}
	}
}

func TestSplitHost(t *testing.T) {
	tests := []struct {
		in, host string
		def      int
		port     int
		bad      bool
	}{
		{in: "Example.COM.:443", host: "example.com", port: 443},
		{in: "[::1]:8080", host: "::1", port: 8080},
		{in: "[::1]", def: 80, host: "::1", port: 80},
		{in: "example.com", def: 80, host: "example.com", port: 80},
		{in: "example.com", bad: true},
		{in: "example.com:0", bad: true},
		{in: "example.com:65536", bad: true},
		{in: "example.com:x", bad: true},
		{in: ":443", bad: true},
	}
	for _, tt := range tests {
		host, port, err := splitHost(tt.in, tt.def)
		if tt.bad {
			if err == nil {
				t.Errorf("%q: expected error", tt.in)
			}
			continue
		}
		if err != nil || host != tt.host || port != tt.port {
			t.Errorf("%q = %q, %d, %v", tt.in, host, port, err)
		}
	}
}

func TestDecideTimeout(t *testing.T) {
	ev := newEvents()
	p := &Proxy{
		Decider: deciderFunc(func(ctx context.Context, _ string, _ int) Decision {
			<-ctx.Done()
			return Allow // too late to count
		}),
		Log:           ev.log,
		DecideTimeout: 50 * time.Millisecond,
	}
	client, _ := proxyClient(startProxy(t, p), nil)
	resp, err := client.Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if e := ev.next(t); e.Allowed || e.Err == "" {
		t.Fatalf("event = %+v", e)
	}
}

func TestClientDisconnectCancelsDecide(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	p := &Proxy{
		Decider: deciderFunc(func(ctx context.Context, _ string, _ int) Decision {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return Deny
		}),
	}
	proxy := startProxy(t, p)

	conn, err := net.Dial("tcp", proxy.Host)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	<-started
	_ = conn.Close()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Decide ctx not cancelled on disconnect")
	}
}

func TestRewrite(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	_, port := hostPort(t, target.URL)

	var decided string
	d := &countingDial{to: strings.TrimPrefix(target.URL, "http://")}
	p := &Proxy{
		Decider: deciderFunc(func(_ context.Context, h string, _ int) Decision {
			decided = h
			return Allow
		}),
		Dial:    d.dial,
		Rewrite: map[string]string{"host.docker.internal": "172.17.0.1"},
	}
	client, _ := proxyClient(startProxy(t, p), nil)
	resp, err := client.Get(fmt.Sprintf("http://host.docker.internal:%d/", port))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if decided != "host.docker.internal" {
		t.Fatalf("decider saw %q", decided)
	}
	want := fmt.Sprintf("172.17.0.1:%d", port)
	if got := d.dialled(); len(got) != 1 || got[0] != want {
		t.Fatalf("dialled %v, want %s", got, want)
	}
}

func TestListenAndServe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&Proxy{Decider: always(Deny)}).ListenAndServe(ctx, addr) }()

	var resp *http.Response
	for range 50 {
		if resp, err = http.Get("http://" + addr + "/"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return")
	}
}

// Anything that is not HTTP goes through as a tunnel, and is logged with its
// host and port and named by what the client said first.
func TestTunnelIsNamedByWhatItCarries(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				_, _ = c.Write(buf[:n])
				_ = c.Close()
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	ev := newEvents()
	proxy := startProxy(t, &Proxy{Decider: always(Allow), Log: ev.log})
	for hello, want := range map[string]string{"SSH-2.0-OpenSSH_9.9\r\n": "ssh", "PING\r\n": "tcp"} {
		c, err := DialVia(context.Background(), proxy.String(), "127.0.0.1", port)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(c, hello)
		_, _ = io.ReadAll(c)
		_ = c.Close()
		e := ev.next(t)
		if !e.Allowed || e.Host != "127.0.0.1" || e.Port != port || e.Proto != want {
			t.Errorf("%q: event = %+v, want proto %s", hello, e, want)
		}
		if e.BytesUp != int64(len(hello)) || e.BytesDown != int64(len(hello)) {
			t.Errorf("%q: bytes = %d up, %d down", hello, e.BytesUp, e.BytesDown)
		}
	}
}

// A refused tunnel carried nothing, so its port is all there is to name it by.
func TestProtoOfARefusedTunnel(t *testing.T) {
	for port, want := range map[int]string{443: "tls", 22: "ssh", 5432: "tcp"} {
		if got := protoOf(nil, port); got != want {
			t.Errorf("port %d: %s, want %s", port, got, want)
		}
	}
	if got := protoOf([]byte("SSH-"), 443); got != "ssh" {
		t.Errorf("ssh on 443: %s", got)
	}
}
