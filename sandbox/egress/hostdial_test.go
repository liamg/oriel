package egress

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestCheckAddr(t *testing.T) {
	for _, c := range []struct {
		ip      string
		public  bool
		private bool
	}{
		{"1.1.1.1", true, true},
		{"2606:4700::1111", true, true},
		{"10.1.78.104", false, true},
		{"172.16.0.1", false, true},
		{"192.168.1.1", false, true},
		{"100.106.118.98", false, true},
		{"fd7a:115c:a1e0::1", false, true},
		{"127.0.0.1", false, false},
		{"::1", false, false},
		{"::ffff:127.0.0.1", false, false},
		{"169.254.169.254", false, false},
		{"fe80::1", false, false},
		{"0.0.0.0", false, false},
		{"64:ff9b::7f00:1", false, false},
		{"64:ff9b::a9fe:a9fe", false, false},
		{"64:ff9b::a01:4e68", false, true},
		{"64:ff9b::101:101", true, true},
		{"64:ff9b:1::7f00:1", false, false},
		{"64:ff9b:1::101:101", false, true},
		{"2002:7f00:1::", false, false},
		{"2002:a01:4e68::1", false, true},
		{"2002:101:101::1", true, true},
		{"2001:0:4136:e378:8000:63bf:80ff:fffe", false, false},
		{"2001:0:4136:e378:8000:63bf:f5fe:b197", false, true},
		{"::127.0.0.1", false, false},
		{"::ffff:10.0.0.1", false, true},
	} {
		ip := netip.MustParseAddr(c.ip)
		if got := CheckAddr(ip, Public) == nil; got != c.public {
			t.Errorf("%s public: got %v", c.ip, got)
		}
		if got := CheckAddr(ip, Private) == nil; got != c.private {
			t.Errorf("%s private: got %v", c.ip, got)
		}
	}
}

func TestDialControl(t *testing.T) {
	check := DialControl(Public)
	for addr, ok := range map[string]bool{
		"1.1.1.1:443":            true,
		"127.0.0.1:80":           false,
		"[64:ff9b::7f00:1]:80":   false,
		"[2002:a01:4e68::1]:443": false,
		"localhost:80":           false,
	} {
		if got := check("tcp", addr, nil) == nil; got != ok {
			t.Errorf("%s: allowed %v", addr, got)
		}
	}
}

type fixedResolver []netip.Addr

func (f fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return f, nil
}

// A name that resolves to loopback is refused, whatever it is called.
func TestDialCheckedRefusesRebinding(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	r := fixedResolver{netip.MustParseAddr("127.0.0.1")}
	for _, scope := range []Scope{Public, Private} {
		if c, err := DialChecked(context.Background(), r, "evil.example.com", port, scope); err == nil {
			_ = c.Close()
			t.Fatalf("scope %d: dialled loopback", scope)
		}
	}
}

// The session end hijacks, the proxy end gets the raw upstream stream.
func TestDialViaSession(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = up.Close() }()
	go func() {
		c, err := up.Accept()
		if err != nil {
			return
		}
		b := make([]byte, 4)
		_, _ = io.ReadFull(c, b)
		_, _ = c.Write([]byte("pong:" + string(b)))
		_ = c.Close()
	}()

	var gotHost, gotPort, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DialPath || !IsUpgrade(r) {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		gotHost, gotPort, gotToken = r.URL.Query().Get("host"), r.URL.Query().Get("port"), r.Header.Get("X-Oriel-Token")
		c, err := net.Dial("tcp", up.Addr().String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_ = ServeDial(w, c)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}
	c, err := DialViaSession(ctx, dial, "tok", "litellm.internal.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(c)
	if string(b) != "pong:ping" {
		t.Fatalf("got %q", b)
	}
	if gotHost != "litellm.internal.example" || gotPort != "443" || gotToken != "tok" {
		t.Fatalf("request: host=%q port=%q token=%q", gotHost, gotPort, gotToken)
	}
}

func TestDialViaSessionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not allowed", http.StatusForbidden)
	}))
	defer srv.Close()
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}
	_, err := DialViaSession(context.Background(), dial, "tok", "example.com", 443)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}
