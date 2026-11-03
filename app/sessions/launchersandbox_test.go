package sessions_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// sandbox.default is where the launcher's Sandbox toggle starts, a sandbox unless it says host; a command that
// names sandbox still wins.
func TestLauncherSandboxStartsFromTheOption(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	l := launcherOf(s)
	l.OpenWith(registry.Args{})
	if !l.Sandbox() {
		t.Error("the sandbox is off by default")
	}
	if err := s.Registry().Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}
	l.OpenWith(registry.Args{})
	if l.Sandbox() {
		t.Error("the sandbox is on with sandbox.default = host")
	}
	l.OpenWith(registry.Args{"sandbox": true})
	if !l.Sandbox() {
		t.Error("session.new {sandbox = true} has the sandbox off")
	}
}
