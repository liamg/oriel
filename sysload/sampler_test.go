package sysload

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

func TestSamplerReadsOnlyWhileWanted(t *testing.T) {
	const every = 5 * time.Second
	var reads atomic.Int64
	c := clocktest.New(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	s := NewSampler(every, func() Reading {
		reads.Add(1)
		return Reading{CPU: 10, Cores: 4}
	})
	s.Clock = c
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	c.Advance(time.Hour)
	if n := reads.Load(); n != 0 || c.Waiting() != 0 {
		t.Fatalf("%d reads, %d waiting, with no one wanting them", n, c.Waiting())
	}

	release := s.Want()
	select {
	case r := <-s.Changes():
		if r.CPU != 10 {
			t.Fatalf("got %+v", r)
		}
	case <-time.After(time.Minute):
		t.Fatal("no reading after Want")
	}
	if s.Last().Cores != 4 {
		t.Fatalf("Last is %+v", s.Last())
	}
	c.Advance(every)
	if n := reads.Load(); n != 2 {
		t.Fatalf("%d reads an interval after Want, want 2", n)
	}
	inner := s.Want()
	inner()
	c.Advance(every)
	if !s.Wanted() || reads.Load() != 3 {
		t.Fatalf("an inner release stopped it: wanted %v, %d reads", s.Wanted(), reads.Load())
	}

	release()
	release()
	c.Advance(time.Hour)
	if n := reads.Load(); n != 3 || s.Wanted() || c.Waiting() != 0 {
		t.Fatalf("%d reads after release, want 3; %d waiting", n, c.Waiting())
	}
}

func TestSamplerSendsOnlyMoves(t *testing.T) {
	const gib = 1 << 30
	readings := []Reading{
		{CPU: 10, MemUsed: 50 * gib, MemTotal: 100 * gib},
		{CPU: 10.5, MemUsed: 50 * gib, MemTotal: 100 * gib},
		{CPU: 10.99, MemUsed: 50 * gib, MemTotal: 100 * gib},
		{CPU: 11, MemUsed: 50 * gib, MemTotal: 100 * gib},        // CPU moved 1 point
		{CPU: 11.5, MemUsed: 101 * gib / 2, MemTotal: 100 * gib}, // memory moved half a point
		{CPU: 11.5, MemUsed: 51 * gib, MemTotal: 100 * gib},      // memory moved 1 point
		{CPU: 11.5, MemUsed: 51 * gib, MemTotal: 100 * gib, Err: "gone"},
	}
	i := 0
	s := NewSampler(time.Hour, func() Reading { r := readings[i]; i++; return r })
	var sent []int
	for n := range readings {
		s.sample()
		select {
		case <-s.Changes():
			sent = append(sent, n)
		default:
		}
	}
	if got, want := fmt.Sprint(sent), "[0 3 5 6]"; got != want {
		t.Fatalf("sent readings %s, want %s", got, want)
	}
	if s.Last().Err != "gone" {
		t.Fatalf("Last is %+v", s.Last())
	}
}

// The machine is read in-process: no child process on any platform.
func TestNoChildProcesses(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); p == "os/exec" {
				t.Errorf("%s imports os/exec", f)
			}
		}
	}
}
