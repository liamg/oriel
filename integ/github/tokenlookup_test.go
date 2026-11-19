package github_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	gh "github.com/liamg/oriel/integ/github"
	"github.com/liamg/oriel/internal/waitfor"
)

// A token lookup the caller gives up on (its context ended: the window closed while gh was slow to answer) stops gh
// and returns at once, leaving nothing running or waiting on it, and isn't kept as the server having no token.
func TestATokenLookupStopsWithItsCaller(t *testing.T) {
	bin := t.TempDir()
	pidAt := filepath.Join(bin, "pid")
	// A gh that says its pid, then never answers.
	script := "#!/bin/sh\necho $$ > \"" + pidAt + "\"\nexec sleep 600\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(name, "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c gh.Clients
	done := make(chan gh.Client, 1)
	go func() { done <- c.On(ctx, "", nil, "github.com", "") }()

	var pid int
	waitfor.Poll(t, "gh asked for a token", func() bool {
		b, err := os.ReadFile(pidAt)
		if err != nil || !strings.HasSuffix(string(b), "\n") {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	})
	cancel()
	waitfor.PollFor(t, 5*time.Second, "the lookup to give up with its caller", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	waitfor.PollFor(t, 5*time.Second, "gh to stop with its caller", func() bool { return syscall.Kill(pid, 0) != nil })

	// Cut short, it wasn't an answer: asked again, gh is asked again.
	if err := os.Remove(pidAt); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { done <- c.On(ctx2, "", nil, "github.com", "") }()
	waitfor.Poll(t, "gh asked again", func() bool { _, err := os.Stat(pidAt); return err == nil })
	cancel2()
	<-done
}
