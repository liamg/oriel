package widget

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// typeText types s at whatever has focus, a key per rune.
func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.keys(string(r))
	}
}

// clickNode clicks the middle of the accessible node named name.
func (h *harness) clickNode(name string) {
	h.t.Helper()
	h.frame()
	n := h.find(name)
	if n == nil {
		h.t.Fatalf("no %q on screen: %q", name, h.texts())
	}
	h.click(mid(n.Bounds))
}

var noSpaces = func(s string) error {
	if strings.Contains(s, " ") {
		return errors.New("no spaces in " + s)
	}
	return nil
}

// A multi-select's menu checks what's selected; choosing an item turns it on or off and leaves the menu open, and
// an item selected but not offered is still listed.
func TestMultiSelect(t *testing.T) {
	sel := []string{"GOFLAGS", "GONE"}
	changes := 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		if v, ok := MultiSelect(c, "env", sel, func() []string { return []string{"GOFLAGS", "HOME", "PATH"} }, MultiSelectOpts{Label: "env", Help: "test/multiselect"}); ok {
			sel, changes = v, changes+1
		}
	})
	if !h.drew("GOFLAGS, GONE ▾") {
		t.Fatalf("the button doesn't sum up the selection: %q", h.texts())
	}
	h.clickNode("GOFLAGS, GONE ▾")
	for name, on := range map[string]bool{"GOFLAGS": true, "GONE": true, "HOME": false, "PATH": false} {
		if n := h.find(name); n == nil || n.Selected != on {
			t.Errorf("%s's item = %+v, want checked %v", name, n, on)
		}
	}
	h.clickNode("HOME")
	if !slices.Equal(sel, []string{"GOFLAGS", "GONE", "HOME"}) || changes != 1 {
		t.Errorf("checking HOME: %v after %d changes", sel, changes)
	}
	h.clickNode("GOFLAGS")
	if !slices.Equal(sel, []string{"GONE", "HOME"}) {
		t.Errorf("unchecking GOFLAGS: %v", sel)
	}
	if h.find("PATH") == nil {
		t.Fatal("the menu closed after a choice")
	}
	h.keys("Esc")
	h.frame()
	if h.find("PATH") != nil {
		t.Error("Escape didn't close the menu")
	}
	sel = nil
	h.frame()
	if !h.drew("none ▾") {
		t.Errorf("an empty selection: %q", h.texts())
	}
}

// A list editor's rows each edit their item in place and remove it with ✕; the field under them adds one. An item
// that doesn't pass Validate says why on its row and isn't set.
func TestListEditor(t *testing.T) {
	items := []string{"allow a", "deny b"}
	h := newHarness(t, theme.Dark, render.Point{X: 600, Y: 400}, func(c *ui.Ctx) {
		if v, ok := ListEditor(c, "rules", items, ListEditorOpts{Label: "rules", Placeholder: "a rule", Validate: func(s string) error {
			if len(strings.Fields(s)) != 2 {
				return errors.New("a rule is two words")
			}
			return nil
		}, Help: "test/listeditor"}); ok {
			items = v
		}
	})
	if n := h.find("rules 2"); n == nil || n.Value != "deny b" {
		t.Fatalf("the second row = %+v", n)
	}
	h.clickNode("Remove allow a")
	if !slices.Equal(items, []string{"deny b"}) {
		t.Errorf("✕ on the first: %v", items)
	}
	h.clickNode("Add to rules")
	h.typeText("allow c")
	h.keys("CR")
	if !slices.Equal(items, []string{"deny b", "allow c"}) {
		t.Errorf("adding allow c: %v", items)
	}
	h.frame()
	if n := h.find("Add to rules"); n == nil || n.Value != "" {
		t.Errorf("the add field after adding: %+v", n)
	}
	h.typeText("bad")
	h.keys("CR")
	if len(items) != 2 || !h.drew("a rule is two words") {
		t.Errorf("an invalid item: %v, %q", items, h.texts())
	}
	h.clickNode("rules 1")
	h.keys("End")
	h.typeText(" x")
	h.frame()
	if !h.drew("a rule is two words") {
		t.Error("an invalid edit doesn't say why on its row")
	}
	h.keys("CR")
	if items[0] != "deny b" {
		t.Errorf("Enter set an invalid edit: %v", items)
	}
	h.keys("BS", "BS", "BS", "c")
	h.keys("CR")
	if items[0] != "deny c" {
		t.Errorf("editing the first row in place: %v", items)
	}
}

// A map field sums up its entries and edits them in a dialog: in place, added and removed, each checked; Escape
// or Cancel discards, Save applies.
func TestMapField(t *testing.T) {
	m := map[string]string{"docs": "https://docs", "linear": "npx linear"}
	saves := 0
	o := MapEditorOpts{Title: "servers", KeyLabel: "name", ValueLabel: "server", Help: "test/mapeditor", Validate: func(k, v string) error {
		return noSpaces(k)
	}}
	h := newHarness(t, theme.Dark, render.Point{X: 900, Y: 600}, func(c *ui.Ctx) {
		if v, ok := MapField(c, "servers", m, o); ok {
			m, saves = v, saves+1
		}
	})
	if !h.drew("2 servers") {
		t.Fatalf("no summary: %q", h.texts())
	}
	open := func() {
		t.Helper()
		h.clickNode("Edit…")
		if h.find("servers") == nil || h.find("name 1") == nil {
			t.Fatalf("Edit… didn't open the dialog: %q", h.texts())
		}
	}
	open()
	if n := h.find("server 2"); n == nil || n.Value != "npx linear" {
		t.Fatalf("the second entry = %+v", n)
	}
	h.clickNode("Remove docs")
	h.keys("Esc")
	h.frame()
	if h.find("name 1") != nil || len(m) != 2 || saves != 0 {
		t.Fatalf("Escape: open %v, %v after %d saves", h.find("name 1") != nil, m, saves)
	}

	open()
	h.clickNode("Remove docs")
	h.clickNode("New name")
	h.typeText("my docs")
	h.clickNode("New server")
	h.typeText("https://x")
	h.keys("CR")
	if !h.drew("no spaces in my docs") {
		t.Errorf("an invalid entry doesn't say why: %q", h.texts())
	}
	h.clickNode("New name")
	h.keys("BS", "BS", "BS", "BS", "BS", "BS", "BS")
	h.typeText("gh")
	h.clickNode("Add")
	h.clickNode("server 1")
	h.keys("End")
	h.typeText(" --flag")
	h.clickNode("Save")
	h.frame()
	want := map[string]string{"gh": "https://x", "linear": "npx linear --flag"}
	if !maps.Equal(m, want) || saves != 1 || h.find("name 1") != nil {
		t.Errorf("after Save: %v (%d saves), want %v and the dialog closed", m, saves, want)
	}

	open()
	h.clickNode("name 1")
	h.typeText(" x")
	h.frame()
	if n := h.find("Save"); n == nil || !n.Disabled {
		t.Errorf("Save with an invalid entry = %+v", n)
	}
	h.clickNode("Cancel")
	h.frame()
	if h.find("name 1") != nil || !maps.Equal(m, want) {
		t.Errorf("Cancel: %v", m)
	}
}

// A long multi-select filters as you type, from the moment it opens; Enter picks the one item left.
func TestMultiSelectFilters(t *testing.T) {
	var opts []string
	for i := range 30 {
		opts = append(opts, fmt.Sprintf("VAR_%02d", i))
	}
	var sel []string
	h := newHarness(t, theme.Dark, render.Point{X: 600, Y: 600}, func(c *ui.Ctx) {
		if v, ok := MultiSelect(c, "env", sel, func() []string { return opts }, MultiSelectOpts{Label: "env", Help: "test/multiselect"}); ok {
			sel = v
		}
	})
	h.clickNode("none ▾")
	h.typeText("_2")
	h.frame()
	if h.find("VAR_01") != nil || h.find("VAR_21") == nil {
		t.Errorf("filtering by _2: %q", h.texts())
	}
	h.typeText("7")
	h.keys("CR")
	if !slices.Equal(sel, []string{"VAR_27"}) {
		t.Errorf("Enter on the one left: %v", sel)
	}
	h.keys("Esc")
	h.frame()
	if h.find("VAR_27") != nil {
		t.Error("Escape in the filter didn't close the menu")
	}
}

// A long select stays inside the window, scrolling past its height, and filters as you type; Enter picks the one
// item left.
func TestSelectLongListFitsAndFilters(t *testing.T) {
	var opts []string
	for i := range 60 {
		opts = append(opts, fmt.Sprintf("Font %02d", i))
	}
	value := "Font 00"
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		if v, ok := Select(c, "font", value, opts, SelectOpts{Label: "Font", Help: "test/select"}); ok {
			value = v
		}
	})
	h.clickNode("Font 00 ▾")
	h.frame()
	d := h.find("Font")
	if d == nil {
		t.Fatalf("no menu: %q", h.texts())
	}
	if d.Bounds.Max.Y > small.Y {
		t.Errorf("the menu runs past the window: %v, window height %v", d.Bounds, small.Y)
	}
	h.typeText("4")
	h.frame()
	if h.find("Font 01") != nil || h.find("Font 14") == nil {
		t.Errorf("filtering by 4: %q", h.texts())
	}
	h.typeText("7")
	h.keys("CR")
	if value != "Font 47" {
		t.Errorf("Enter on the one left: %q", value)
	}
	h.frame()
	if h.find("Font 14") != nil {
		t.Error("the menu stayed open after a choice")
	}
}

// A select at the window's right edge opens its menu leftwards, inside the window.
func TestSelectAtTheRightEdge(t *testing.T) {
	value := "dark"
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("page", ui.Style{W: ui.Flex(1), Main: ui.End}, func(c *ui.Ctx) {
			if v, ok := Select(c, "variant", value, []string{"dark", "light", "system"}, SelectOpts{Label: "Variant", Help: "test/select"}); ok {
				value = v
			}
		})
	})
	h.clickNode("dark ▾")
	h.frame()
	d := h.find("Variant")
	if d == nil {
		t.Fatalf("no menu: %q", h.texts())
	}
	if d.Bounds.Min.X < 0 || d.Bounds.Max.X > small.X {
		t.Errorf("the menu runs past the window: %v, window width %v", d.Bounds, small.X)
	}
}
