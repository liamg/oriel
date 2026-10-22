package jsonrpc

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStartStdio(t *testing.T) {
	var stderr strings.Builder
	p, err := StartStdio(exec.Command("sh", "-c", "echo oops >&2; cat"), &stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if err := WriteLine(p, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	line, err := ReadLine(bufio.NewReader(p))
	if err != nil || string(line) != `{"a":1}` {
		t.Fatalf("read back %q, %v", line, err)
	}
	p.Close()
	select {
	case <-p.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("cat didn't exit once its stdin closed")
	}
	if got := stderr.String(); got != "oops\n" {
		t.Errorf("stderr = %q", got)
	}
}

// A killed process is reaped without anyone waiting on it.
func TestKilledProcessIsReaped(t *testing.T) {
	p, err := StartStdio(exec.Command("sleep", "60"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	p.Kill()
	select {
	case <-p.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("not reaped")
	}
	if p.Wait() == nil {
		t.Error("a killed process exited cleanly")
	}
	if err := p.Kill(); err != nil {
		t.Errorf("Kill after exit = %v", err)
	}
}

func TestStartStdioFails(t *testing.T) {
	if _, err := StartStdio(exec.Command("/no/such/command"), io.Discard); err == nil {
		t.Fatal("started a command that isn't there")
	}
}
