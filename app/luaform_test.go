package app_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
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
func tab(s *apptest.Scene, t *testing.T) {
	t.Helper()
	modes := []string{s.UI().KeyMode(), "global"}
	k, _ := s.Registry().Lookup(modes, []string{"<Tab>"})
	if k == nil {
		if !s.UI().Key(kit.KeyEvent("<Tab>")) {
			t.Fatal("nothing takes Tab")
		}
	} else {
		s.Run(k.Command, k.Args)
	}
	s.Frame()
}

// A Lua panel with an input, a toggle and a select: typing, toggling and choosing call the plugin's functions, and
// the panel redraws from its state. Tab moves focus through the controls; Enter submits the input; the controls
// carry help ids that resolve. A callback that errors shows as a Lua error notice with its `init.lua:N` line.
func TestLuaFormWidgets(t *testing.T) {
	s := apptest.LuaScene(t, formPlugin)
	s.Run("view.open", registry.Args{"id": "deploy-form", "place": "dock", "edge": "right", "title": "Deploy", "size": 420})
	s.Frame()
	s.Frame()
	in := s.Node("Name")
	if in == nil {
		t.Fatalf("no Name input:\n%s", s.Text())
	}
	s.Click(apptest.Centre(in.Bounds), 0)
	s.Type("v", "2")
	s.Frame() // the callback asked for a redraw: the view's Lua runs again with the new state
	if !strings.Contains(s.Text(), "name=v2 notify=false env=staging") {
		t.Fatalf("typing didn't reach the plugin's state:\n%s", s.Text())
	}
	s.Type("CR")
	if n, _ := apptest.LastNotice(s.Store().State(), "lua."); n.Text != "submitted v2" {
		t.Errorf("Enter didn't submit: %+v", n)
	}

	tab(s, t) // to the toggle
	if f := s.Node("Notify"); f == nil || !f.Focused {
		t.Fatalf("Tab didn't move to the toggle: %+v", f)
	}
	s.Type(" ")
	s.Frame()
	if !strings.Contains(s.Text(), "notify=true") {
		t.Errorf("Space didn't toggle:\n%s", s.Text())
	}
	tab(s, t) // to the select
	s.Type("CR")
	s.Frame()
	s.ClickText("prod")
	s.Frame()
	if !strings.Contains(s.Text(), "env=prod") {
		t.Errorf("choosing prod didn't reach the plugin:\n%s", s.Text())
	}

	for _, name := range []string{"Name", "Notify"} {
		if n := s.Node(name); n == nil || n.Description != "plugins/lua#ui" {
			t.Errorf("%s's help id: %+v", name, n)
		}
	}

	s.ClickText("Fail")
	n, _ := apptest.LastNotice(s.Store().State(), "lua.callback.")
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

// A Lua view draws a widget.Chart through oriel.ui.chart, and a click on a bar reaches its on_click with the
// series and point, counted from 1.
func TestLuaChart(t *testing.T) {
	s := apptest.LuaScene(t, chartPlugin)
	s.Run("view.open", registry.Args{"id": "chart-demo", "place": "tab", "title": "Chart"})
	s.Frame()
	s.Frame()
	s.WantFrame("api", "web", "Mon", "Wed", "release")
	// Tuesday's bar, just above its label: api's 4 is the bottom of the stack.
	tue := s.TextPoint("Tue")
	s.Click(render.Point{X: tue.X, Y: tue.Y - 20}, 0)
	s.Frame()
	s.WantFrame("clicked=1@2")
}

const datePlugin = `local s = oriel.ui.state("dates-demo", { from = "", to = "" })
oriel.view.override("dates-demo", function(ctx)
  return oriel.ui.column{ pad = 14, gap = 10,
    oriel.ui.date_picker{ id = "range", label = "Deploy window", placeholder = "pick dates", from = s.from, to = s.to,
      on_change = function(from, to) s.from, s.to = from, to end },
    oriel.ui.text("range=" .. s.from .. ".." .. s.to),
  }
end)
`

// A Lua view picks a range of days through oriel.ui.date_picker (widget.DatePicker): its button opens the
// picker, a typed range is picked with ⏎, and on_change gets its days as YYYY-MM-DD.
func TestLuaDatePicker(t *testing.T) {
	s := apptest.LuaScene(t, datePlugin)
	s.Run("view.open", registry.Args{"id": "dates-demo", "place": "tab", "title": "Dates"})
	s.Frame()
	s.Frame()
	s.Click(apptest.Centre(s.Node("pick dates").Bounds), 0)
	s.Frame()
	s.TypeInto("Deploy window, typed", "2026-09-01..2026-09-30")
	s.Press("<CR>")
	s.Frame()
	s.WantFrame("range=2026-09-01..2026-09-30", "Sep 1 – Sep 30")
}

// The multi-line input in Lua: `lines` makes oriel.ui.input take several lines, ⏎ a new line.
func TestLuaMultiLineInput(t *testing.T) {
	s := apptest.LuaScene(t, `local s = oriel.ui.state("notes-form", { notes = "" })
oriel.view.override("notes-form", function(ctx)
  return oriel.ui.column{ pad = 14, gap = 10,
    oriel.ui.input{ id = "notes", label = "Notes", value = s.notes, lines = 4, on_change = function(t) s.notes = t end,
      on_submit = function(t) oriel.notify("sent " .. #t) end },
    oriel.ui.text("notes=" .. s.notes:gsub("\n", "|")),
  }
end)
`)
	s.Run("view.open", registry.Args{"id": "notes-form", "place": "dock", "edge": "right", "title": "Jot", "size": 420})
	s.Frame()
	s.Frame()
	s.Click(apptest.Centre(s.Node("Notes").Bounds), 0)
	s.Type("a", "CR", "b")
	s.Frame()
	if !strings.Contains(s.Text(), "notes=a|b") {
		t.Fatalf("⏎ in a Lua input with lines didn't make a new line:\n%s", s.Text())
	}
}

// A Lua input with enter_submits is a message's field: ⇧⏎ a new line, ⏎ submits.
func TestLuaMessageInput(t *testing.T) {
	s := apptest.LuaScene(t, `local s = oriel.ui.state("reply-form", { text = "" })
oriel.view.override("reply-form", function(ctx)
  return oriel.ui.column{ pad = 14, gap = 10,
    oriel.ui.input{ id = "reply", label = "Reply", value = s.text, lines = 4, enter_submits = true,
      on_change = function(t) s.text = t end, on_submit = function(t) s.sent = t:gsub("\n", "|") end },
    oriel.ui.text("sent=" .. (s.sent or "")),
  }
end)
`)
	s.Run("view.open", registry.Args{"id": "reply-form", "place": "dock", "edge": "right", "title": "Jot", "size": 420})
	s.Frame()
	s.Frame()
	s.Click(apptest.Centre(s.Node("Reply").Bounds), 0)
	s.Type("a")
	s.Press("<S-CR>")
	s.Type("b")
	s.Press("<CR>")
	s.Frame()
	if !strings.Contains(s.Text(), "sent=a|b") {
		t.Fatalf("⇧⏎ then ⏎ in a Lua input with enter_submits didn't send two lines:\n%s", s.Text())
	}
}
