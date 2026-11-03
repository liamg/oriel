package runner

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

// steps times the steps of a launch and logs them as one line, so a slow
// start says which step it was.
type steps struct {
	what        string
	start, last time.Time
	parts       []string
}

func startSteps(what string) *steps {
	now := time.Now()
	return &steps{what: what, start: now, last: now}
}

// Mark ends the step called name.
func (s *steps) Mark(name string) {
	now := time.Now()
	s.parts = append(s.parts, fmt.Sprintf("%s %s", name, round(now.Sub(s.last))))
	s.last = now
}

// Done logs the run: its total, then each step.
func (s *steps) Done() {
	log.Printf("%s: %s (%s)", s.what, round(time.Since(s.start)), strings.Join(s.parts, ", "))
}

func round(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// executable is os.Executable, refused under go test: a test binary handed
// a subcommand runs the whole suite again.
func executable() (string, error) {
	if testing.Testing() {
		return "", errors.New("refusing to use a test binary as oriel")
	}
	return os.Executable()
}
