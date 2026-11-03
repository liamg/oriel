package egress

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestDialViaTunnelsRawTCP(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	go func() {
		c, err := echo.Accept()
		if err != nil {
			return
		}
		_, _ = io.Copy(c, c)
		_ = c.Close()
	}()
	host, portStr, _ := net.SplitHostPort(echo.Addr().String())
	port, _ := strconv.Atoi(portStr)

	var asked []string
	p := &Proxy{Decider: deciderFunc(func(_ context.Context, h string, pt int) Decision {
		asked = append(asked, net.JoinHostPort(h, strconv.Itoa(pt)))
		return Allow
	})}
	srv := httptest.NewServer(p)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := DialVia(ctx, srv.URL, host, port)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("SSH-2.0-test\r\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 14)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "SSH-2.0-test\r\n" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
	if len(asked) != 1 || asked[0] != echo.Addr().String() {
		t.Errorf("decider asked about %v", asked)
	}
}

func TestDialViaDenied(t *testing.T) {
	p := &Proxy{Decider: deciderFunc(func(context.Context, string, int) Decision { return Deny })}
	srv := httptest.NewServer(p)
	defer srv.Close()
	if _, err := DialVia(context.Background(), srv.URL, "github.com", 22); err == nil {
		t.Fatal("a denied destination tunnelled")
	}
}
