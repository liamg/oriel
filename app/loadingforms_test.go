package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

const slowTracker = `pending = nil
asked = 0
oriel.ticket.tracker{ name = "slow", title = "Slow", keys = "^SLO-[0-9]+$",
  lookup = function(key, done) done(nil, "no " .. key) end,
  assigned = function(done) asked = asked + 1; pending = done end }`

// The tickets page shows skeleton rows while its tracker first answers, says nothing's assigned only once it has,
// and a refresh keeps the list up with Refresh busy.
func TestTicketsPageLoading(t *testing.T) {
	s := apptest.LuaScene(t, slowTracker)
	s.Now = apptest.At(9, 0, 0)
	vm := lua(s).VM()
	s.Run("tickets.open", registry.Args{"tracker": "slow"})
	s.Frame()
	s.Until("the ask", func() bool { return global(vm, "asked") == float64(1) })
	if strings.Contains(s.Text(), "Nothing assigned") {
		t.Error("the empty message showed before the tracker answered")
	}
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	if n := s.Node("Tickets"); n.Role != "status" {
		t.Errorf("the skeleton reads as %+v", n)
	}
	answer := func() {
		t.Helper()
		if _, err := vm.Eval(script.Chunk{Name: "t", Source: []byte("pending({})")}); err != nil {
			t.Fatal(err)
		}
	}
	answer()
	s.Now = s.Now.Add(time.Second)
	s.Until("the answer", func() bool { return strings.Contains(s.Text(), "Nothing assigned to you in Slow") })

	s.Press("r")
	s.Until("the second ask", func() bool { return global(vm, "asked") == float64(2) })
	s.Frame()
	s.Midway(func() { s.WantFrame("Refreshing…", "Nothing assigned to you in Slow") })
	answer()
	s.Until("the refresh", func() bool { return !strings.Contains(s.Text(), "Refreshing…") })
}
