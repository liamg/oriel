package shell_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// A picker source whose items are still coming shows a busy row, and "No matches" only once it's done.
func TestPickerSourceLoading(t *testing.T) {
	s := omniScene(t, "")
	s.Press("<Esc>")
	loading := true
	s.Registry().Picker(uireg.PickerSource{ID: "slow", Title: "Slow", PickerOnly: true,
		Items:   func(registry.Ctx, string) []uireg.PickerItem { return nil },
		Loading: func() bool { return loading }})
	s.Run("picker.open", registry.Args{"source": "slow", "query": "x"})
	s.Frame()
	if strings.Contains(s.Text(), "No matches") {
		t.Error("no matches while the source is still loading")
	}
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("Slow still loading…")
	loading = false
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Frame()
	s.WantFrame("No matches")
}

// oriel.picker.source's loading = fn() is the same signal for a Lua source.
func TestLuaPickerSourceLoading(t *testing.T) {
	s := apptest.LuaScene(t, `busy = true
oriel.picker.source{ name = "wiki", title = "Wiki", picker_only = true, items = function(q) return {} end, loading = function() return busy end }`)
	s.Run("picker.open", registry.Args{"source": "wiki", "query": "x"})
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.WantFrame("Wiki still loading…")
	s.Lua(`(function() busy = false end)()`)
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Frame()
	s.WantFrame("No matches")
}

// The files picker walks the tree off the loop: it says it's loading until the walk is done, then has the files.
func TestFilesPickerWalksOffTheLoop(t *testing.T) {
	s := omniScene(t, "")
	src := mustSource(t, s, "files")
	if shell.SourceLoading(src) {
		t.Fatal("still loading after the walk")
	}
	g, ok := shell.RankSource(s.Headless, src, "round", shell.PickerMax)
	if !ok || g.Items()[0].Label != "internal/pricing/round.go" {
		t.Errorf("hits: %+v", g.Items())
	}
}
