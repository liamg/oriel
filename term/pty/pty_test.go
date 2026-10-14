//go:build linux || darwin

package pty

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// readUntil reads p until its output holds want, or fails after a few seconds.
func readUntil(t *testing.T, p *PTY, out *bytes.Buffer, want string) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		for !strings.Contains(out.String(), want) {
			n, err := p.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(out.String(), want) {
			t.Fatalf("reading for %q: %v; got %q", want, err, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading for %q; got %q", want, out.String())
	}
}

func TestStart(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf hi; stty size")
	p, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out bytes.Buffer
	readUntil(t, p, &out, "24 80")
	if !strings.HasPrefix(out.String(), "hi24 80") {
		t.Errorf("output %q", out.String())
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("wait: %v", err)
	}
	buf := make([]byte, 64)
	for {
		if _, err := p.Read(buf); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("read after exit = %v, want io.EOF", err)
			}
			break
		}
	}
}

func TestResize(t *testing.T) {
	cmd := exec.Command("sh", "-c", "stty size; read x; stty size")
	p, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out bytes.Buffer
	readUntil(t, p, &out, "24 80")
	if err := p.Resize(132, 43); err != nil {
		t.Fatal(err)
	}
	if c, r, err := p.Size(); err != nil || c != 132 || r != 43 {
		t.Errorf("Size = %d, %d, %v", c, r, err)
	}
	if _, err := p.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, &out, "43 132")
	if err := cmd.Wait(); err != nil {
		t.Errorf("wait: %v", err)
	}
}

func TestControllingTerminal(t *testing.T) {
	cmd := exec.Command("sh", "-c", "tty; test -t 0 && test -t 1 && echo yes")
	p, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out bytes.Buffer
	readUntil(t, p, &out, "yes")
	if !strings.Contains(out.String(), "/dev/") {
		t.Errorf("output %q", out.String())
	}
	cmd.Wait()
}

// Close hangs the terminal up: a Read in progress returns (on macOS once SIGHUP has ended the program).
func TestCloseEndsRead(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	p, err := Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	done := make(chan struct{})
	go func() {
		p.Read(make([]byte, 16))
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	p.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Read still blocked after Close")
	}
}
