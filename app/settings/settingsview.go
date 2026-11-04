package settings

import (
	"context"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

func (s *Plugin) build(c *ui.Ctx) {
	if !s.open || c.Theme().IsProbe() {
		return
	}
	th := c.Theme()
	o := s.h.Options()
	specs := o.Specs()
	sections := map[string]int{}
	at := s.atFor(s.scope)
	for _, sp := range specs {
		sections[sectionOf(sp)] += 0
		if _, l, _ := o.Get(sp.Name, at); l != registry.LayerDefault {
			sections[sectionOf(sp)]++
		}
	}
	own := map[string]uireg.SettingsSection{}
	for _, sec := range uireg.SettingsSections(s.h) {
		own[sec.Name] = sec
		n := 0
		if sec.Count != nil {
			registry.Call(func() error { n = sec.Count(); return nil })
		}
		sections[sec.Name] += n
	}
	names := slices.Sorted(maps.Keys(sections))
	if s.section == "" && len(names) > 0 {
		s.section = names[0]
	}
	sec, isOwn := own[s.section]
	isOwn = isOwn && s.query == ""
	var rows []registry.OptionSpec
	for _, sp := range specs {
		if s.query != "" && s.matches(sp, at) || s.query == "" && sectionOf(sp) == s.section {
			rows = append(rows, sp)
		}
	}
	po := widget.PageOpts{KeyMode: "settings", Label: "Settings", Help: "shell/settings"}
	widget.Page(c, "settings", &s.open, po, func(c *ui.Ctx) {
		s.header(c, th)
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Row("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("sections", ui.Style{W: ui.Px(240), H: ui.Flex(1), Pad: ui.PadXY(8, 12), Gap: 2, Fill: th.C(tThemePanel), ScrollY: true}, func(c *ui.Ctx) {
				c.Text("t", "SECTIONS", capLabel(th))
				c.Box("g", ui.Style{H: ui.Px(6)}, nil)
				for _, name := range names {
					on := s.query == "" && name == s.section
					fill, fg := render.Color{}, theme.FgMuted
					if on {
						fill, fg = th.C(theme.AccentBg), theme.Fg
					}
					if widget.Clickable(c, name, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center, Fill: fill,
						Radius: 5, Help: "shell/settings#sections", Role: ui.RoleListItem, Label: name, Selected: on}, func(c *ui.Ctx) {
						c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(fg)})
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if n := sections[name]; n > 0 {
							c.Text("m", fmt.Sprintf("● %d", n), kit.MonoFont(10, th.C(tSetModified)))
						}
					}).Clicked {
						s.section, s.query = name, ""
					}
				}
			})
			c.Box("d", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(28, 16), Gap: 4, ScrollY: true}, func(c *ui.Ctx) {
				title := s.section
				if s.query != "" {
					title = fmt.Sprintf("%d matching %q", len(rows), s.query)
				}
				c.Row("title", ui.Style{W: ui.Flex(1), H: ui.Px(36), Cross: ui.Center, Gap: 12}, func(c *ui.Ctx) {
					c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 18, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					if s.query == "" && !isOwn && widget.Button(c, "resetsec", "Reset section", widget.ButtonOpts{Ghost: true, Help: "shell/settings#reset"}) {
						s.fail(s.resetWhere(func(sp registry.OptionSpec) bool { return sectionOf(sp) == s.section }, s.scope))
					}
				})
				if isOwn {
					c.Column("own", ui.Style{W: ui.Flex(1), Gap: 4}, sec.Build)
				}
				for _, sp := range rows {
					s.row(c, th, sp, at)
				}
				for _, f := range s.allFiles() {
					f.mu.Lock()
					unknown := slices.Clone(f.unknown)
					f.mu.Unlock()
					if len(unknown) > 0 && s.query == "" {
						widget.Wrap(c, "unknown"+f.path, fmt.Sprintf("%s also sets %s, which nothing has registered.", f.path, strings.Join(unknown, ", ")),
							700, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
					}
				}
			})
		})
		s.footer(c, th)
	})
}

func (s *Plugin) header(c *ui.Ctx, th *theme.Resolved) {
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(16, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", "SETTINGS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.SemiBold, Tracking: .14}, Color: th.C(theme.Accent)})
		c.Box("q", ui.Style{W: ui.Size{Kind: ui.Grow, Value: 1, Max: 420}}, func(c *ui.Ctx) {
			widget.Input(c, "in", &s.query, widget.InputOpts{Label: "Search settings", Placeholder: "search · @modified · @project · @plugin:id",
				Help: "shell/settings#search"})
		})
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("scope", "EDITING", capLabel(th))
		scope := s.scope
		if widget.Segmented(c, "scopes", scopeNames, &scope, widget.SegmentedOpts{Help: "shell/settings#scope"}) {
			if _, err := s.fileFor(scope); err != nil {
				s.fail(err)
			} else {
				s.scope = scope
			}
		}
		toml := 0
		if s.asTOML {
			toml = 1
		}
		if widget.Segmented(c, "as", []string{"CONTROLS", "TOML"}, &toml, widget.SegmentedOpts{Help: "shell/settings#toml"}) {
			s.asTOML = toml == 1
		}
	})
}

func (s *Plugin) sourceLabel(name string, l registry.Layer, at registry.At) string {
	switch l {
	case registry.LayerDefault:
		if sp, ok := s.h.Options().Spec(name); ok && sp.Follows != "" {
			return "follows " + sp.Follows
		}
		return "default"
	case registry.LayerLua:
		return "set in " + s.h.Options().Source(name, at)
	case registry.LayerSession, registry.LayerFile, registry.LayerBuffer, registry.LayerWindow:
		if src := s.h.Options().Source(name, at); src != "" {
			return l.String() + " · " + src
		}
	}
	return l.String()
}

func (s *Plugin) row(c *ui.Ctx, th *theme.Resolved, sp registry.OptionSpec, at registry.At) {
	o := s.h.Options()
	v, l, _ := o.Get(sp.Name, at)
	_, setHere := o.Lookup(sp.Name, s.layerFor(s.scope), at)
	lua := l == registry.LayerLua
	canScope := sp.Scopes&[...]registry.Scope{registry.ScopeUser, registry.ScopeProject, registry.ScopeSession}[s.scope] != 0
	disabled := s.readOnly(sp.Name, s.scope) != nil || !canScope
	shadowL, shadowSrc, shadowed := s.shadow(sp.Name, s.scope)
	c.Row(sp.Name, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 10), Gap: 16, Cross: ui.Center, Help: "shell/settings#rows"}, func(c *ui.Ctx) {
		mark := render.Color{}
		if l != registry.LayerDefault {
			mark = th.C(tSetModified)
		}
		c.Box("mod", ui.Style{W: ui.Px(3), H: ui.Px(36), Radius: 2, Fill: mark}, nil)
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
			c.Row("name", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", sp.Name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12.5, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
				if len(sp.Aliases) > 0 {
					c.Text("a", strings.Join(sp.Aliases, " · "), kit.MonoFont(10, th.C(theme.FgGhost)))
				}
			})
			if s.asTOML {
				sec, key, _ := strings.Cut(sp.Name, ".")
				c.Text("toml", fmt.Sprintf("[%s] %s = %s", sec, key, formatValue(toTOML(sp, v))), kit.MonoFont(11, th.C(tThemeTOMLVal)))
				c.Text("lua", fmt.Sprintf("oriel.option[%q] = %s", sp.Name, luaValue(toTOML(sp, v))), kit.MonoFont(11, th.C(theme.FgFaint)))
			} else if sp.Help != "" {
				widget.Wrap(c, "h", sp.Help, 560, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: th.C(theme.FgFaint)})
			}
			if shadowed {
				s.shadowNote(c, th, shadowL, shadowSrc)
			}
		})
		srcTok, src := tSetSource, s.sourceLabel(sp.Name, l, at)
		if lua {
			srcTok = tSetLua
		}
		if t, ok := s.saved[sp.Name]; ok && c.Now().Before(t.Add(settingsSavedFor)) {
			srcTok, src = tSetModified, "✓ saved"
			c.WakeAt(t.Add(settingsSavedFor))
		}
		c.Text("src", src, kit.MonoFont(10.5, th.C(srcTok)))
		fade := float32(0)
		if disabled {
			fade = .55
		}
		ctlW := float32(260)
		if sp.Type == registry.List && sp.Choices == nil {
			ctlW = 340
		}
		c.Box("ctl", ui.Style{W: ui.Px(ctlW), Main: ui.End, Fade: fade}, func(c *ui.Ctx) { s.control(c, th, sp, v, disabled) })
		if widget.Button(c, "reset", "↺", widget.ButtonOpts{Ghost: true, Disabled: !setHere || disabled, Help: "shell/settings#reset"}) {
			s.fail(s.reset(sp.Name, s.scope))
		}
	})
}

// shadowNote says what decides a row's value over the layer the page edits: init.lua, with a link to the line, or
// a later layer.
func (s *Plugin) shadowNote(c *ui.Ctx, th *theme.Resolved, l registry.Layer, src string) {
	c.Row("shadow", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		if l != registry.LayerLua {
			c.Text("t", "overridden here by "+strings.TrimSuffix(s.sourceLabel("", l, registry.At{})+" · "+src, " · "), kit.MonoFont(10.5, th.C(theme.FgFaint)))
			return
		}
		c.Text("t", "init.lua wins over settings.toml", kit.MonoFont(10.5, th.C(tSetLua)))
		file, line := script.SplitLoc(src)
		if line == 0 {
			return
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(s.ConfigDir, file)
		}
		if widget.Clickable(c, "open", ui.Style{Dir: ui.Row, Label: "Open " + src, Help: "shell/settings#rows"}, func(c *ui.Ctx) {
			c.Text("t", "Open "+src, kit.MonoFont(10.5, th.C(theme.Accent)))
		}).Clicked {
			s.fail(s.h.Run(context.Background(), "editor.open", registry.Args{"path": file, "line": line}))
		}
	})
}

// control is a row's typed control: a toggle, choices, a list or map editor, or a field that's checked when entered.
func (s *Plugin) control(c *ui.Ctx, th *theme.Resolved, sp registry.OptionSpec, v any, disabled bool) {
	const help = "shell/settings#rows"
	switch sp.Type {
	case registry.List:
		items, _ := v.([]string)
		if sp.Choices != nil {
			if l, ok := widget.MultiSelect(c, "multi", items, sp.Choices, widget.MultiSelectOpts{Label: sp.Name, Disabled: disabled, Help: help}); ok {
				s.save(sp.Name, l)
			}
			return
		}
		if l, ok := widget.ListEditor(c, "list", items, widget.ListEditorOpts{Label: sp.Name, Disabled: disabled, Help: help, Width: 340,
			Placeholder: "add one, ⏎", Validate: itemCheck(sp)}); ok {
			s.save(sp.Name, l)
		}
		return
	case registry.Map:
		m, _ := v.(map[string]string)
		if nm, ok := widget.MapField(c, "map", m, widget.MapEditorOpts{Title: sp.Name, KeyLabel: sp.KeyLabel, ValueLabel: sp.ValueLabel,
			Disabled: disabled, Help: help, Validate: entryCheck(sp)}); ok {
			s.save(sp.Name, nm)
		}
		return
	case registry.Table:
		m, _ := v.(map[string]any)
		c.Text("table", fmt.Sprintf("%d %s · in settings.toml", len(m), kit.Plural(len(m), "entry", "entries")), kit.MonoFont(11, th.C(theme.FgMuted)))
		return
	case registry.Bool:
		on, _ := v.(bool)
		if widget.Toggle(c, "toggle", on, widget.ToggleOpts{Label: sp.Name, Disabled: disabled, Help: "shell/settings#rows"}) {
			s.save(sp.Name, !on)
		}
	case registry.Enum:
		cur, _ := v.(string)
		if len(sp.Values) <= 4 {
			i := slices.Index(sp.Values, cur)
			if widget.Segmented(c, "choices", kit.Upper(sp.Values), &i, widget.SegmentedOpts{Help: "shell/settings#rows"}) && !disabled {
				s.save(sp.Name, sp.Values[i])
			}
			return
		}
		s.dropdown(c, sp, cur, sp.Values, disabled)
	case registry.String:
		if sp.Choices != nil {
			cur, _ := v.(string)
			s.dropdown(c, sp, cur, sp.Choices(), disabled)
			return
		}
		s.field(c, sp, v, disabled)
	default:
		s.field(c, sp, v, disabled)
	}
}

// save sets an option from the page at the scope it edits, and marks its row saved.
func (s *Plugin) save(name string, v any) {
	if err := s.set(name, v, s.scope); err != nil {
		s.fail(err)
		return
	}
	if s.saved == nil {
		s.saved = map[string]time.Time{}
	}
	if u := s.h.UI(); u != nil {
		s.saved[name] = u.Now()
	}
}

// itemCheck is a list option's Validate asked about one item.
func itemCheck(sp registry.OptionSpec) func(string) error {
	if sp.Validate == nil {
		return nil
	}
	return func(item string) error { return sp.Validate([]string{item}) }
}

// entryCheck is a map option's Validate asked about one entry.
func entryCheck(sp registry.OptionSpec) func(k, v string) error {
	if sp.Validate == nil {
		return nil
	}
	return func(k, v string) error { return sp.Validate(map[string]string{k: v}) }
}

// dropdown is a menu of values for a row.
func (s *Plugin) dropdown(c *ui.Ctx, sp registry.OptionSpec, cur string, values []string, disabled bool) {
	if v, ok := widget.Select(c, "drop", cur, values, widget.SelectOpts{Label: sp.Name, Disabled: disabled, Help: "shell/settings#rows"}); ok {
		s.save(sp.Name, v)
	}
}

// field is a text field for a value, checked as it's typed and set when entered, when it loses focus or once
// typing pauses. A number with a range and a step has a slider beside it too, set when it's let go.
func (s *Plugin) field(c *ui.Ctx, sp registry.OptionSpec, v any, disabled bool) {
	text := fieldText(v)
	fid := c.Next("field")
	st := ui.State[fieldState](c, fid)
	line := ui.Child(fid, "line")
	in := c.Interaction(ui.Child(line, "in"))
	if !in.Focused && !st.sliding && st.from != text {
		st.text, st.from, st.typing = text, text, false
	}
	var accept func(rune) bool
	switch sp.Type {
	case registry.Int:
		accept = widget.Digits
	case registry.Float:
		accept = widget.Decimal
	}
	slider := sp.Step > 0 && sp.Max > sp.Min
	w := float32(200)
	if slider {
		w = 260
	}
	c.Column("field", ui.Style{W: ui.Px(w), Gap: 4}, func(c *ui.Ctx) {
		var res widget.InputResult
		c.Row("line", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			if slider {
				sid := c.Next("slide")
				if !st.sliding {
					st.slide, _ = toFloat(v)
				}
				if widget.Slider(c, "slide", &st.slide, widget.SliderOpts{Min: sp.Min, Max: sp.Max, Step: sp.Step, Width: ui.Flex(1),
					Label: sp.Name, Help: "shell/settings#rows"}) && !disabled {
					st.sliding, st.text = true, fieldText(numberAs(sp, st.slide))
				}
				if st.sliding && !c.Interaction(sid).Held {
					st.sliding = false
					s.save(sp.Name, numberAs(sp, st.slide))
				}
			}
			iw := ui.Flex(1)
			if slider {
				iw = ui.Px(64)
			}
			before := st.text
			res = widget.Input(c, "in", &st.text, widget.InputOpts{Label: sp.Name, Width: iw, Disabled: disabled, Help: "shell/settings#rows",
				Accept: accept, Invalid: st.bad != nil})
			if st.text != before {
				st.typing, st.typed = true, c.Now()
			}
		})
		val, bad := parseField(sp, st.text)
		if bad == nil {
			bad = sp.Check(val)
		}
		st.bad = bad
		blurred := st.focused && !in.Focused
		st.focused = in.Focused
		switch {
		case res.Cancelled:
			st.text, st.typing = text, false
		case res.Submitted:
			st.typing = false
			if bad != nil {
				s.fail(bad)
			} else if st.text != text {
				s.save(sp.Name, val)
			}
		case st.typing && (blurred || !c.Now().Before(st.typed.Add(settingsTypingPause))):
			st.typing = false
			if bad == nil && st.text != text {
				s.save(sp.Name, val)
			}
		case st.typing:
			c.WakeAt(st.typed.Add(settingsTypingPause))
		}
		if bad != nil && st.text != text {
			widget.Wrap(c, "bad", bad.Error(), w, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 11}, Color: c.Theme().C(theme.Error)})
		}
	})
}

// settingsTypingPause is how long a field waits after the last key before it sets what was typed.
const settingsTypingPause = 800 * time.Millisecond

// fieldState is a field's text, the value it was last filled from, and its slider's value while it's moved.
type fieldState struct {
	text, from string
	typing     bool      // text was typed and isn't set yet
	typed      time.Time // the last key that changed it
	focused    bool
	bad        error
	slide      float64
	sliding    bool
}

// toFloat is an Int's or a Float's value as a float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// numberAs is f as sp's type: an Int rounds.
func numberAs(sp registry.OptionSpec, f float64) any {
	if sp.Type == registry.Int {
		return int(math.Round(f))
	}
	return f
}

// fieldText is a value as its field shows it.
func fieldText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case time.Duration:
		return registry.FormatDuration(v)
	}
	return fmt.Sprint(v)
}

// parseField reads what was typed into a field as sp's type.
func parseField(sp registry.OptionSpec, s string) (any, error) {
	switch sp.Type {
	case registry.Int:
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("%s is a whole number, not %q", sp.Name, s)
		}
		return n, nil
	case registry.Float:
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fmt.Errorf("%s is a number, not %q", sp.Name, s)
		}
		return f, nil
	case registry.Duration:
		return registry.ParseDuration(s)
	}
	return s, nil
}

func (s *Plugin) footer(c *ui.Ctx, th *theme.Resolved) {
	f, _ := s.fileFor(s.scope)
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(16, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tThemePanel), Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
		where := "this session: not written to disk"
		if f != nil {
			where = "writing to " + kit.TildePath(f.path)
		}
		c.Text("w", where, kit.MonoFont(10.5, th.C(theme.FgFaint)))
		if f != nil {
			f.mu.Lock()
			err, untrusted := f.err, f.untrusted
			f.mu.Unlock()
			switch {
			case err != nil:
				c.Text("err", "✕ "+err.Error()+"; the last good values stay", kit.MonoFont(10.5, th.C(theme.Error)))
			case untrusted:
				c.Text("trust", "not trusted yet, so not applied", kit.MonoFont(10.5, th.C(theme.Warn)))
			}
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "undo", "Undo", widget.ButtonOpts{Ghost: true, Disabled: len(s.undo) == 0, Help: "shell/settings#reset"}) {
			s.fail(s.undoLast())
		}
		if widget.Button(c, "all", "Reset all", widget.ButtonOpts{Ghost: true, Help: "shell/settings#reset"}) {
			s.fail(s.resetWhere(func(registry.OptionSpec) bool { return true }, s.scope))
		}
		c.Text("keys", "esc close", kit.MonoFont(10.5, th.C(theme.FgFaint)))
	})
}

func (s *Plugin) fail(err error) {
	if err != nil {
		s.h.Notify(core.Notice{ID: "settings.set", Severity: core.SevError, Title: "Settings", Text: err.Error(), Key: true})
	}
}
