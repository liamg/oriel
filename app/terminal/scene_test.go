package terminal_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/terminal"
	"github.com/liamg/oriel/registry"
)

func termsOf(s *apptest.Scene) *terminal.Terminals {
	return apptest.PluginOf[*terminal.Terminals](s, "terminal")
}

func termScene(t *testing.T, args registry.Args) *apptest.Scene { return apptest.TermScene(t, args) }

func typeTerm(s *apptest.Scene, keys ...string) { s.TypeTerm(keys...) }

func waitTerm(t *testing.T, s *apptest.Scene, want string) string {
	t.Helper()
	return s.WaitTerm(want)
}

func wantText(t *testing.T, s *apptest.Scene, what string, wants ...string) {
	t.Helper()
	s.WantText(what, wants...)
}
