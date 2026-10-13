package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// demoPlugin is a plugin written only against the public Lua API, putting UI everywhere a plugin can: a panel in
// a dock, a tab among the files, a segment in the title bar and in the status line, a replaced built-in, marks in
// the editor, buttons, a command and a key.
const demoPlugin = `local L = oriel
local deploys = {
  { service = "pricing", env = "prod", status = "ok", ago = "4m" },
  { service = "ledger", env = "prod", status = "warn", ago = "12m" },
  { service = "gateway", env = "staging", status = "error", ago = "1h" },
}
local rolled = {}

-- a panel, opened in the right dock
L.view.override("deploys", function(ctx)
  return L.ui.column{ pad = 14, gap = 10,
    L.ui.text("DEPLOYS", "accent"),
    L.ui.list(deploys, function(d)
      local tone = d.status
      return L.ui.row{
        L.ui.text(d.service, tone),
        L.ui.text(d.env .. " · " .. d.ago, "dim"),
        L.ui.spacer(),
        L.ui.button{ label = rolled[d.service] and "Rolled back" or "Roll back", id = "rb-" .. d.service,
          on_click = function() rolled[d.service] = true; L.notify("rolling back " .. d.service, "warn") end },
      }
    end),
    L.ui.button{ label = "Deploy all", primary = true, on_click = function() L.notify("deploying everything") end },
  }
end)

-- a tab among the files
L.view.override("dashboard", function()
  return L.ui.column{ pad = 14, gap = 10,
    L.ui.text("TEAM DASHBOARD", "accent"),
    L.ui.tat{ label = "ERROR BUDGET", bar = 0.82, warn = 0.8 },
    L.ui.tat{ label = "P99", value = "184ms", tone = "ok" },
    L.ui.tat{ label = "OPEN INCIDENTS", value = 1, tone = "error" },
  }
end)

-- a segment in the title bar, and one in the status line
L.view.override("titlebar.right.40-deploys", function()
  return L.ui.tat{ label = "DEPLOY", value = #deploys, tone = "warn" }
end)
L.view.override("statusline.right.50-ci", function()
  return L.ui.row{ L.ui.text("CI", "dim"), L.ui.text("✓ main", "ok") }
end)

-- marks in the editor, on every buffer
L.decor.provider("todo-owners", function(buf, top, bottom)
  return { { line = 3, sign = "◆", hl = "accent", text = "owned by @pricing-team" } }
end)

-- a command and a key that open the panel
L.command("Deploys", function()
  L.cmd("view.open", { id = "deploys", place = "dock", edge = "right", title = "Deploys", size = 420 })
end, { desc = "Show the deploys panel" })
L.keymap.set("n", "<leader>D", "Deploys", { desc = "Deploys" })
`

// The demo plugin's UI, all of it there: the panel, the tab, both bars' segments, the editor mark, and buttons that
// run Lua when clicked.
func TestPluginDemo(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "internal", "pricing", "round.go")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(roundGo()), 0o644)
	s := newScene(t, "", dir, mockupSize)
	s.run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.writeConfig(t, map[string]string{"init.lua": demoPlugin})
	s.lua().reload(false)
	if x := s.lua(); x.err != nil {
		t.Fatal(x.err)
	}
	s.run("Deploys", nil)
	s.run("view.open", registry.Args{"id": "dashboard", "place": "split", "side": "right", "title": "Dashboard"})
	s.frame()
	s.frame()
	text := s.frameText()
	for _, want := range []string{"DEPLOYS", "pricing", "prod · 4m", "Roll back", "Deploy all", "Dashboard", "DEPLOY", "CI", "✓ main"} {
		if !strings.Contains(text, want) {
			t.Errorf("the window lacks %q:\n%s", want, text)
		}
	}
	if off := s.reg.TurnedOff(); len(off) != 0 {
		t.Fatalf("turned off: %+v", off)
	}
	b := s.access("Roll back") // pricing's, the first
	if b == nil {
		t.Fatal("no Roll back button")
	}
	s.click(centre(b.Bounds))
	s.frame()
	if n, _ := lastNotice(s.store.State(), "lua."); n.Text != "rolling back pricing" {
		t.Errorf("the click didn't run the plugin's function: %+v", n)
	}
	if !strings.Contains(s.frameText(), "Rolled back") {
		t.Errorf("the panel didn't redraw with the plugin's state:\n%s", s.frameText())
	}
	for _, n := range s.store.State().Notifications {
		s.store.Apply(core.NoticeDismissed{Seq: n.Seq})
	}
	s.golden("plugin-demo")
}
