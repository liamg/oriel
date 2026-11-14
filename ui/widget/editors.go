package widget

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tFieldError = theme.Declare("field.error", "error")
	tEditorNote = theme.Declare("editor.summary", "fg.muted")
)

// fieldError is why a field's text isn't valid, under it.
func fieldError(c *ui.Ctx, key any, err error, width float32) {
	Wrap(c, key, err.Error(), width, uiText(c, 11, typeset.Normal, col(c, tFieldError)))
}

type MultiSelectOpts struct {
	Label     string // its accessible name, and the menu's
	Disabled  bool
	Help      string
	Width     float32 // the menu's; 240 if 0
	MaxHeight float32 // the menu's, past which it scrolls; 320 if 0
}

type multiSelectState struct {
	open, shown bool
	filter      string
}

// filterFrom is how many items a multi-select has before it offers a filter.
const filterFrom = 10

// MultiSelect is a button summing up selected that opens a menu of options (asked for only while it's open), each
// ticked while it's selected. Choosing one turns it on or off and leaves the menu open; an item selected but not
// offered is listed too. A long menu has a filter field over it, focused as it opens, where Enter picks the one item
// left. It returns the new selection and true on the frame it changes; the caller keeps it.
func MultiSelect(c *ui.Ctx, key any, selected []string, options func() []string, o MultiSelectOpts) ([]string, bool) {
	id := c.Next(key)
	st := ui.State[multiSelectState](c, id)
	w, maxH := o.Width, o.MaxHeight
	if w == 0 {
		w = 240
	}
	if maxH == 0 {
		maxH = 320
	}
	var out []string
	changed := false
	toggle := func(v string) {
		if slices.Contains(selected, v) {
			out = slices.DeleteFunc(slices.Clone(selected), func(s string) bool { return s == v })
		} else {
			out = append(slices.Clone(selected), v)
		}
		changed = true
	}
	c.Box(key, ui.Style{}, func(c *ui.Ctx) {
		b := c.Next("drop")
		if Button(c, "drop", summary(selected)+" ▾", ButtonOpts{Disabled: o.Disabled, Help: o.Help}) {
			st.open, st.shown, st.filter = true, false, ""
		}
		Popover(c, "menu", b, &st.open, PopoverOpts{Label: o.Label, Width: w + 24, Help: o.Help}, func(c *ui.Ctx) {
			all := slices.Clone(options())
			for _, s := range selected {
				if !slices.Contains(all, s) {
					all = append(all, s)
				}
			}
			shown, res := menuFilter(c, all, &st.filter, !st.shown, o.Label, o.Help)
			switch {
			case res.Cancelled:
				st.open = false
			case res.Submitted && len(shown) == 1:
				toggle(shown[0])
			}
			st.shown = true
			items := make([]MenuItem, len(shown))
			for i, v := range shown {
				items[i] = MenuItem{Label: v, Help: o.Help, Checkable: true, Checked: slices.Contains(selected, v)}
			}
			i, closed := Menu(c, "m", items, MenuOpts{Width: w, MaxHeight: maxH, Help: o.Help})
			switch {
			case i >= 0:
				toggle(shown[i])
			case closed:
				st.open = false
			}
		})
	})
	return out, changed
}

// menuFilter is a filter field over a long menu, focused when focus is set (as the menu opens), and the options
// whose names contain its text. A short menu gets no field and keeps all its options.
func menuFilter(c *ui.Ctx, all []string, filter *string, focus bool, label, help string) ([]string, InputResult) {
	if len(all) < filterFrom {
		return all, InputResult{}
	}
	f := c.Next("filter")
	if focus {
		c.Focus(f)
	}
	res := Input(c, "filter", filter, InputOpts{Label: "Filter " + label, Placeholder: "filter", Help: help})
	q := strings.ToLower(strings.TrimSpace(*filter))
	return slices.DeleteFunc(slices.Clone(all), func(v string) bool { return !strings.Contains(strings.ToLower(v), q) }), res
}

// summary is a selection in a few words: its items, or the first two and how many more.
func summary(sel []string) string {
	switch {
	case len(sel) == 0:
		return "none"
	case len(sel) <= 2:
		return strings.Join(sel, ", ")
	}
	return fmt.Sprintf("%s, %s +%d", sel[0], sel[1], len(sel)-2)
}

type ListEditorOpts struct {
	Label       string // its accessible name; row n's field is "Label n"
	Placeholder string // the add field's
	Validate    func(item string) error
	Disabled    bool
	Help        string
	Width       float32 // 300 if 0
}

// draft is a field's text, and the value it was last filled from.
type draft struct{ text, from string }

type listEditorState struct {
	rows []draft
	add  string
}

// ListEditor edits a list of strings as rows, each a field editing its item in place (set on Enter while it's
// valid) with ✕ to remove it, and a field under them that adds an item on Enter. An item that doesn't pass
// Validate says why under its row. It returns the new list and true on the frame it changes; the caller keeps it.
func ListEditor(c *ui.Ctx, key any, items []string, o ListEditorOpts) ([]string, bool) {
	id := c.Next(key)
	st := ui.State[listEditorState](c, id)
	w := o.Width
	if w == 0 {
		w = 300
	}
	check := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("an item can't be empty")
		}
		if o.Validate != nil {
			return o.Validate(s)
		}
		return nil
	}
	st.rows = st.rows[:min(len(st.rows), len(items))]
	for len(st.rows) < len(items) {
		st.rows = append(st.rows, draft{})
	}
	var out []string
	changed := false
	set := func(v []string) { out, changed = v, true }
	c.Column(key, ui.Style{W: ui.Px(w), Gap: 6, Role: ui.RoleGroup, Label: o.Label}, func(c *ui.Ctx) {
		for i, item := range items {
			d := &st.rows[i]
			c.Column(i, ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
				c.Row("row", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
					in := c.Next("in")
					if !c.Interaction(in).Focused && d.from != item {
						d.text, d.from = item, item
					}
					bad := check(d.text)
					res := Input(c, "in", &d.text, InputOpts{Label: fmt.Sprintf("%s %d", o.Label, i+1), Disabled: o.Disabled, Help: o.Help,
						Invalid: bad != nil})
					switch {
					case res.Cancelled:
						d.text = item
					case res.Submitted && bad == nil && d.text != item:
						set(slices.Replace(slices.Clone(items), i, i+1, d.text))
					}
					if !o.Disabled && IconButton(c, "remove", "✕", IconButtonOpts{Label: "Remove " + item, Help: o.Help}) {
						set(slices.Delete(slices.Clone(items), i, i+1))
					}
				})
				if err := check(d.text); err != nil {
					fieldError(c, "bad", err, w)
				}
			})
		}
		if o.Disabled {
			return
		}
		c.Column("add", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			bad := check(st.add)
			res := Input(c, "in", &st.add, InputOpts{Label: "Add to " + o.Label, Placeholder: o.Placeholder, Help: o.Help,
				Invalid: st.add != "" && bad != nil})
			switch {
			case res.Cancelled:
				st.add = ""
			case res.Submitted && bad == nil:
				set(append(slices.Clone(items), st.add))
				st.add = ""
			}
			if st.add != "" && bad != nil {
				fieldError(c, "bad", bad, w)
			}
		})
	})
	return out, changed
}

type MapEditorOpts struct {
	Title                string // the dialog's, and the Edit… button's accessible name is "Edit " + Title
	KeyLabel, ValueLabel string // its two sides: "name", "server"
	Validate             func(key, value string) error
	Disabled             bool
	Help                 string
}

type entry struct{ k, v string }

type mapEditorState struct {
	shown      bool
	entries    []entry
	addK, addV string
	focusAdd   bool
}

// MapField is a map's summary ("3 servers", counted in ValueLabels) and an Edit… button that opens MapEditor on it.
// It returns the new map and true on the frame Save changes it.
func MapField(c *ui.Ctx, key any, m map[string]string, o MapEditorOpts) (map[string]string, bool) {
	id := c.Next(key)
	open := ui.State[bool](c, id)
	var out map[string]string
	changed := false
	c.Row(key, ui.Style{Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("n", count(len(m), cmp.Or(o.ValueLabel, "entry")), uiText(c, 12.5, typeset.Normal, col(c, tEditorNote)))
		if Button(c, "edit", "Edit…", ButtonOpts{Disabled: o.Disabled, Help: o.Help}) {
			*open = true
		}
		if v, ok := MapEditor(c, "dialog", open, m, o); ok && !maps.Equal(v, m) {
			out, changed = v, true
		}
	})
	return out, changed
}

// count is n of noun: "no servers", "1 server", "3 servers".
func count(n int, noun string) string {
	plural := noun + "s"
	if strings.HasSuffix(noun, "y") {
		plural = strings.TrimSuffix(noun, "y") + "ies"
	}
	switch n {
	case 0:
		return "no " + plural
	case 1:
		return "1 " + noun
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// MapEditor is a dialog over a scrim editing m while *open: a row per entry, its key and value edited in place and ✕
// to remove it, and a row that adds one. An entry that doesn't pass Validate, or repeats a key, says why under it,
// and Save waits until none does; what's typed in the add row is saved too. Escape, Cancel or a click on the scrim discards; Save applies, returning the map
// and true.
func MapEditor(c *ui.Ctx, key any, open *bool, m map[string]string, o MapEditorOpts) (map[string]string, bool) {
	id := c.Next(key)
	st := ui.State[mapEditorState](c, id)
	if !*open {
		st.shown = false
		return nil, false
	}
	if !st.shown {
		st.shown, st.addK, st.addV = true, "", ""
		st.entries = st.entries[:0]
		for _, k := range slices.Sorted(maps.Keys(m)) {
			st.entries = append(st.entries, entry{k, m[k]})
		}
	}
	kl, vl := cmp.Or(o.KeyLabel, "key"), cmp.Or(o.ValueLabel, "value")
	check := func(i int, e entry) error {
		if strings.TrimSpace(e.k) == "" {
			return fmt.Errorf("each %s needs a %s", vl, kl)
		}
		for j, x := range st.entries {
			if j != i && x.k == e.k {
				return fmt.Errorf("%q is there twice", e.k)
			}
		}
		if o.Validate != nil {
			return o.Validate(e.k, e.v)
		}
		return nil
	}
	var out map[string]string
	saved, closing := false, false
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	card := ui.Child(c.Next(struct{ k any }{key}), "card")
	if hasKey(c.Interaction(card).Keys, "Esc") {
		*open = false
		return nil, false
	}
	const w = 640
	if Scrim(c, struct{ k any }{key}, ScrimOpts{Z: ui.LayerDialog, Fill: col(c, tScrim), Help: o.Help}, func(c *ui.Ctx) {
		c.Column("card", ui.Style{W: ui.Px(w), Fill: col(c, tModalBg), Stroke: col(c, tModalBorder), Border: 1, Radius: 10, Clip: true,
			Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}}, Enter: riseIn, Exit: fade, Trap: true, Focusable: true, Interactive: true,
			KeyMode: "modal", Help: o.Help, Role: ui.RoleDialog, Label: o.Title}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(40), Pad: ui.PadXY(16, 0), Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", o.Title, uiText(c, 13, typeset.SemiBold, col(c, tModalTitle)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				kbd(c, "esc")
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: col(c, tMenuSeparator)}, nil)
			valid := true
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Pad(16), Gap: 8}, func(c *ui.Ctx) {
				c.Row("cols", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
					c.Box("k", ui.Style{W: ui.Px(170)}, func(c *ui.Ctx) { c.Text("t", strings.ToUpper(kl), monoText(9.5, typeset.SemiBold, col(c, tLabel), .1)) })
					c.Text("v", strings.ToUpper(vl), monoText(9.5, typeset.SemiBold, col(c, tLabel), .1))
				})
				remove := -1
				for i := range st.entries {
					e := &st.entries[i]
					bad := check(i, *e)
					valid = valid && bad == nil
					c.Column(i, ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
						c.Row("row", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
							rk := Input(c, "k", &e.k, InputOpts{Label: fmt.Sprintf("%s %d", kl, i+1), Width: ui.Px(170), Help: o.Help, TabMoves: true,
								Invalid: bad != nil})
							rv := Input(c, "v", &e.v, InputOpts{Label: fmt.Sprintf("%s %d", vl, i+1), Help: o.Help, TabMoves: true, Invalid: bad != nil})
							closing = closing || rk.Cancelled || rv.Cancelled
							if IconButton(c, "remove", "✕", IconButtonOpts{Label: "Remove " + e.k, Help: o.Help}) {
								remove = i
							}
						})
						if bad != nil {
							fieldError(c, "bad", bad, w-32)
						}
					})
				}
				if remove >= 0 {
					st.entries = slices.Delete(st.entries, remove, remove+1)
				}
				add := entry{st.addK, st.addV}
				bad := check(-1, add)
				typed := st.addK != "" || st.addV != ""
				valid = valid && (!typed || bad == nil)
				c.Column("add", ui.Style{W: ui.Flex(1), Gap: 3, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
					c.Row("row", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						rk := Input(c, "k", &st.addK, InputOpts{Label: "New " + kl, Placeholder: kl, Width: ui.Px(170), Help: o.Help, TabMoves: true,
							Invalid: typed && bad != nil})
						rv := Input(c, "v", &st.addV, InputOpts{Label: "New " + vl, Placeholder: vl, Help: o.Help, TabMoves: true, Invalid: typed && bad != nil})
						closing = closing || rk.Cancelled || rv.Cancelled
						if (Button(c, "add", "Add", ButtonOpts{Disabled: !typed || bad != nil, Help: o.Help}) || rk.Submitted || rv.Submitted) && bad == nil {
							st.entries = append(st.entries, add)
							st.addK, st.addV = "", ""
						}
					})
					if typed && bad != nil {
						fieldError(c, "bad", bad, w-32)
					}
				})
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: col(c, tMenuSeparator)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(48), Pad: ui.PadXY(16, 0), Gap: 8, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				if Button(c, "cancel", "Cancel", ButtonOpts{Ghost: true, Help: o.Help}) {
					closing = true
				}
				if Button(c, "save", "Save", ButtonOpts{Primary: true, Disabled: !valid || o.Disabled, Help: o.Help}) {
					out = map[string]string{}
					for _, e := range st.entries {
						out[e.k] = e.v
					}
					if st.addK != "" {
						out[st.addK] = st.addV
					}
					saved = true
				}
			})
		})
	}) {
		closing = true
	}
	if saved || closing {
		*open = false
	}
	return out, saved
}
