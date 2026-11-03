package plugintest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/plugintest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
)

func TestMain(m *testing.M) { plugintest.Main(m) }

// The harness drives testdata/hello as an installed plugin: its panel at the Inbox's foot on the fixed clock, its
// option and command, a click, typing into its field, and its agent tool, answered by its process in its data
// directory.
func TestHarness(t *testing.T) {
	now := time.Date(2026, 10, 13, 14, 7, 21, 0, time.UTC)
	a := plugintest.New(t, plugintest.Options{Now: now, Size: render.Point{X: 1440, Y: 1000}})
	a.Apply(core.SessionSynced{Session: core.SessionState{ID: "pricing", Name: "pricing", Kind: core.KindWork, Status: core.StatusQuiet}},
		core.SessionFocused{ID: "pricing"})
	dir := plugintest.Install(t, "testdata/hello/plugin", "./testdata/hello/cmd/oriel-hello")
	p := a.Load(dir, nil)
	if p.ID != "test/hello" {
		t.Errorf("loaded %q", p.ID)
	}

	a.Run("inbox.open", nil)
	a.UntilText("hello · 0", "14:07:21")
	a.SetNow(now.Add(time.Minute))
	a.UntilText("14:08:21")

	a.SetOption("plugin.hello.greeting", "hi")
	a.Run("hello.count", nil)
	a.UntilText("hi · 1")
	if n := a.Find("More"); n == nil || n.Role != "button" {
		t.Errorf("the button in the accessibility tree: %+v", n)
	}
	a.ClickText("More")
	a.UntilText("hi · 2")

	a.Run("hud.toggle", nil)
	a.UntilText("HELLO", "say something…")
	a.TypeIn("hey", "say something…")
	a.Type("CR")
	a.Run("hud.toggle", nil)
	a.UntilText("hey · 2")

	out, err := a.CallTool("assistant", a.Tool("assistant", "greet"), map[string]any{"who": "tom"})
	var said struct {
		Said  string
		Count int
	}
	if err != nil || json.Unmarshal([]byte(out), &said) != nil || said.Said != "hey, tom" || said.Count != 2 {
		t.Errorf("greet: %s %v", out, err)
	}
	if b, err := os.ReadFile(filepath.Join(p.Data, "said")); err != nil || string(b) != "hey, tom" {
		t.Errorf("the data directory has %q %v", b, err)
	}
}
