package agentcmd

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
)

// Both directions finish: what the far end sends after our input ends
// still arrives.
func TestPipeWaitsForBothDirections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		got, _ := io.ReadAll(c)
		_, _ = c.Write([]byte("reply to " + string(got)))
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out bytes.Buffer
	if err := pipe(c, strings.NewReader("hello"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "reply to hello" {
		t.Fatalf("got %q", out.String())
	}
}

func TestParsePorts(t *testing.T) {
	got := parsePorts("443, 8080,,x")
	if len(got) != 2 || got[0] != 443 || got[1] != 8080 {
		t.Fatalf("got %v", got)
	}
	if len(parsePorts("")) != 0 {
		t.Fatal("no ports from nothing")
	}
}
