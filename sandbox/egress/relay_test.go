package egress

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/raw" && IsUpgrade(r) && r.Header.Get("X-Oriel-Token") == "tok" {
			c, err := Hijack(w)
			if err != nil {
				return
			}
			b := make([]byte, 4)
			_, _ = io.ReadFull(c, b)
			_, _ = c.Write([]byte("pong:" + string(b)))
			_ = c.Close()
			return
		}
		_, _ = io.WriteString(w, "hello "+r.URL.Path)
	}))
	defer srv.Close()
	dir, err := os.MkdirTemp("", "relay")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	portFile := filepath.Join(dir, "port")
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	if err := os.WriteFile(portFile, []byte(port+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addr := PortFileAddr("127.0.0.1", portFile)
	httpSock, rawSock := filepath.Join(dir, "h.sock"), filepath.Join(dir, "r.sock")
	go func() { _ = Relay{Addr: addr}.Serve(ctx, httpSock) }()
	go func() { _ = Relay{Addr: addr, Token: "tok", UpgradePath: "/raw"}.Serve(ctx, rawSock) }()
	dial := func(p string) net.Conn {
		t.Helper()
		for range 50 {
			if c, err := net.Dial("unix", p); err == nil {
				return c
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s never opened", p)
		return nil
	}

	c := dial(httpSock)
	_, _ = io.WriteString(c, "GET /api/x HTTP/1.1\r\nHost: oriel\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = c.Close()
	if string(b) != "hello /api/x" {
		t.Fatalf("http relay: %q", b)
	}

	c = dial(rawSock)
	_, _ = c.Write([]byte("ping"))
	b, _ = io.ReadAll(c)
	_ = c.Close()
	if string(b) != "pong:ping" {
		t.Fatalf("raw relay: %q", b)
	}

	// No port yet: the connection is closed, not hung.
	_ = os.WriteFile(portFile, nil, 0o600)
	c = dial(httpSock)
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: oriel\r\n\r\n")
	b, err = io.ReadAll(c)
	if len(b) != 0 || (err != nil && strings.Contains(err.Error(), "timeout")) {
		t.Fatalf("with no port: %q %v", b, err)
	}
}
