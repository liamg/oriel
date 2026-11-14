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
func (testBackend) Launch(context.Context, string, string, []string, LaunchOpts) (Command, error) {
	return Command{}, nil
}
func (testBackend) Stop(context.Context, string) error                { return nil }
func (testBackend) Health(context.Context, Config) []Problem          { return nil }
func (testBackend) Sweep(context.Context, func(string) bool) []string { return nil }
func (testBackend) AgentRunDir(string) string                         { return "" }
func (testBackend) ServesPort() bool                                  { return false }

func TestBackends(t *testing.T) {
	bs := Backends{testBackend{name: "here"}, testBackend{name: "elsewhere", err: errors.New("not on this machine")}}
	if got := bs.Names(); !slices.Equal(got, []string{"here", "elsewhere"}) {
		t.Errorf("names: %v", got)
	}
	if got := bs.Available(); !slices.Equal(got, []string{"here"}) {
		t.Errorf("available: %v", got)
	}
	if b, err := bs.For(Config{Backend: "elsewhere"}); err != nil || b.Name() != "elsewhere" {
		t.Errorf("For(elsewhere) = %v, %v", b, err)
	}
	if _, err := bs.For(Config{Backend: "nope"}); err == nil || !strings.Contains(err.Error(), "here, elsewhere") {
		t.Errorf("For(nope) = %v", err)
	}
}

// The default is the most preferred backend this machine can run: orbstack when docker isn't set up.
func TestDefaultBackendPrefersAvailable(t *testing.T) {
	bs := Backends{testBackend{name: "docker", err: errors.New("no daemon")}, testBackend{name: "orbstack"}, testBackend{name: "podman"}}
	if got := bs.Default(); got != "orbstack" {
		t.Errorf("default = %q, want orbstack", got)
	}
	if got := bs.Available(); !slices.Equal(got, []string{"orbstack", "podman"}) {
		t.Errorf("available = %v, want the preferred first", got)
	}
	if b, err := bs.For(Config{}); err != nil || b.Name() != "orbstack" {
		t.Errorf("For(no backend) = %v, %v", b, err)
	}
}

// With none it can run, the default is the most preferred, which says why it can't.
func TestDefaultBackendWithNoneAvailable(t *testing.T) {
	bs := Backends{testBackend{name: "docker", err: errors.New("no daemon")}, testBackend{name: "podman", err: errors.New("no machine")}}
	if got := bs.Default(); got != "docker" {
		t.Errorf("default = %q, want docker", got)
	}
}
