package app

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

const formPlugin = `local s = oriel.ui.state("deploy-form", { name = "", notify = false, env = "staging" })
oriel.view.override("deploy-form", function(ctx)
  return oriel.ui.column{ pad = 14, gap = 10,
    oriel.ui.input{ id = "name", label = "Name", value = s.name, placeholder = "release name",
      on_change = function(t) s.name = t end,
      on_submit = function(t) oriel.notify("submitted " .. t) end },
    oriel.ui.toggle{ id = "notify", label = "Notify", on = s.notify, on_change = function(v) s.notify = v end },
    oriel.ui.select{ id = "env", label = "Env", value = s.env, options = { "staging", "prod" },
      on_change = function(v) s.env = v end },
    oriel.ui.text("name=" .. s.name .. " notify=" .. tostring(s.notify) .. " env=" .. s.env),
    oriel.ui.button{ label = "Fail", on_click = function()
      error("boom")
    end },
  }
end)
`

// tab moves focus as the window's keys do: Tab through the focused control's key mode.
func (s *errScene) tab(t *testing.T) {
	t.Helper()
	modes := []string{s.ui.KeyMode(), "global"}
	k, _ := s.reg.Lookup(modes, []string{"<Tab>"})
	if k == nil {
		if !s.ui.Key(fromNotation("<Tab>")) {
			t.Fatal("nothing takes Tab")
		}
	} else {
		s.run(k.Command, k.Args)
	}
	s.frame()
}

// PL-14: "A Lua panel with an input, a toggle and a select: typing, toggling and choosing call the plugin's
// functions, and the panel redraws from its state." "Tab moves focus through the controls; Enter submits the input;
// the controls carry help ids that resolve." "A callback that errors shows as a PL-12 notice with its `init.lua:N`
// line."
func TestLuaFormWidgets(t *testing.T) {
	s := luaScene(t, formPlugin)
	s.run("view.open", registry.Args{"id": "deploy-form", "place": "dock", "edge": "right", "title": "Deploy", "size": 420})
	s.frame()
	s.frame()
	in := s.access("Name")
	if in == nil {
		t.Fatalf("no Name input:\n%s", s.frameText())
	}
	s.click(centre(in.Bounds))
	s.typeKeys("v", "2")
	s.frame() // the callback asked for a redraw: the view's Lua runs again with the new state
	if !strings.Contains(s.frameText(), "name=v2 notify=false env=staging") {
		t.Fatalf("typing didn't reach the plugin's state:\n%s", s.frameText())
	}
	s.typeKeys("CR")
	if n, _ := lastNotice(s.store.State(), "lua."); n.Text != "submitted v2" {
		t.Errorf("Enter didn't submit: %+v", n)
	}

	s.tab(t) // to the toggle
	if f := s.access("Notify"); f == nil || !f.Focused {
		t.Fatalf("Tab didn't move to the toggle: %+v", f)
	}
	s.typeKeys(" ")
	s.frame()
	if !strings.Contains(s.frameText(), "notify=true") {
		t.Errorf("Space didn't toggle:\n%s", s.frameText())
	}
	s.tab(t) // to the select
	s.typeKeys("CR")
	s.frame()
	s.clickText(t, "prod")
	s.frame()
	if !strings.Contains(s.frameText(), "env=prod") {
		t.Errorf("choosing prod didn't reach the plugin:\n%s", s.frameText())
	}

	for _, name := range []string{"Name", "Notify"} {
		if n := s.access(name); n == nil || n.Description != "plugins/lua#ui" {
			t.Errorf("%s's help id: %+v", name, n)
		}
	}

	s.clickText(t, "Fail")
	n, _ := lastNotice(s.store.State(), "lua.callback.")
	if n.Location != "init.lua:12" || !strings.Contains(n.Text, "boom") {
		t.Errorf("the failing callback's notice: %+v", n)
	}
}

const chartPlugin = `local s = oriel.ui.state("chart-demo", { clicked = "none" })
oriel.view.override("chart-demo", function(ctx)
  return oriel.ui.column{ pad = 14, gap = 10,
    oriel.ui.chart{ id = "c", label = "Deploys per day", kind = "bars", height = 120, labels = { "Mon", "Tue", "Wed" },
      y_ticks = 2, format = "count", legend = true, hover = true,
      series = { { name = "api", values = { 2, 4, 1 }, color = "cyan" }, { name = "web", values = { 1, 2, 3 }, color = "#ff7ad6" } },
      marks = { { at = 2, label = "release" } },
      on_click = function(series, point) s.clicked = series .. "@" .. point end },
    oriel.ui.text("clicked=" .. s.clicked),
  }
end)
`

// AI-12: a Lua view draws a widget.Chart through oriel.ui.chart, and a click on a bar reaches its on_click with the
// series and point, counted from 1.
func TestLuaChart(t *testing.T) {
	s := luaScene(t, chartPlugin)
	s.run("view.open", registry.Args{"id": "chart-demo", "place": "tab", "title": "Chart"})
	s.frame()
	s.frame()
	s.wantFrame(t, "api", "web", "Mon", "Wed", "release")
	// Tuesday's bar, just above its label: api's 4 is the bottom of the stack.
	tue := s.textPoint(t, "Tue")
	s.click(render.Point{X: tue.X, Y: tue.Y - 20})
	s.frame()
	s.wantFrame(t, "clicked=1@2")
}
