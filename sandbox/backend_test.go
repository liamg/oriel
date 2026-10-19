package sandbox

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type testBackend struct {
	name string
	err  error
}

func (b testBackend) Name() string     { return b.name }
func (b testBackend) Available() error { return b.err }
func (testBackend) Launch(context.Context, string, string, []string, LaunchOptions) (Command, error) {
	return Command{}, nil
}
func (testBackend) Stop(context.Context, string) error                { return nil }
func (testBackend) Health(context.Context, Config) []Problem          { return nil }
func (testBackend) Sweep(context.Context, func(string) bool) []string { return nil }
func (testBackend) AgentRunDir(string) string                         { return "" }
func (testBackend) ServesPort() bool                                  { return false }

func TestBackendRegistry(t *testing.T) {
	Register(testBackend{name: "test-here"})
	Register(testBackend{name: "test-elsewhere", err: errors.New("not on this machine")})
	if got := Backends(); !slices.Contains(got, "test-here") || !slices.Contains(got, "test-elsewhere") {
		t.Errorf("registered: %v", got)
	}
	if got := AvailableBackends(); !slices.Contains(got, "test-here") || slices.Contains(got, "test-elsewhere") {
		t.Errorf("available: %v", got)
	}
	if b, err := BackendFor(Config{Backend: "test-here"}); err != nil || b.Name() != "test-here" {
		t.Errorf("BackendFor(test-here) = %v, %v", b, err)
	}
	if _, err := BackendFor(Config{Backend: "nope"}); err == nil || !strings.Contains(err.Error(), "test-here") {
		t.Errorf("BackendFor(nope) = %v", err)
	}
}

// The default is the most preferred backend this machine can run: orbstack when docker isn't set up.
func TestDefaultBackendPrefersAvailable(t *testing.T) {
	old := Preference
	t.Cleanup(func() { Preference = old })
	Preference = []string{"test-docker", "test-orbstack", "test-podman"}
	Register(testBackend{name: "test-docker", err: errors.New("no daemon")})
	Register(testBackend{name: "test-podman"})
	Register(testBackend{name: "test-orbstack"})
	if got := DefaultBackend(); got != "test-orbstack" {
		t.Errorf("default = %q, want test-orbstack", got)
	}
	if got := AvailableBackends(); len(got) < 2 || got[0] != "test-orbstack" || got[1] != "test-podman" {
		t.Errorf("available = %v, want the preferred first", got)
	}
	if b, err := BackendFor(Config{}); err != nil || b.Name() != "test-orbstack" {
		t.Errorf("BackendFor(no backend) = %v, %v", b, err)
	}
}
