package shell

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// themes registers the built-in themes and the theme files in the user's themes directory.
type themes struct {
	kit.Disposer
	dir string
}

func (t *themes) Manifest() plugin.Manifest { return plugin.Manifest{ID: "themes"} }

func (t *themes) Activate(_ context.Context, h plugin.Host) error {
	for _, s := range theme.Builtins() {
		t.Add(h.Theme(s))
	}
	files, _ := filepath.Glob(filepath.Join(t.dir, "*.toml"))
	for _, f := range files {
		t.Add(h.Theme(theme.File{Path: f}))
	}
	t.Add(h.Picker(themePicker(h)))
	t.Add(h.Command(registry.Command{ID: "Themes", Title: "Pick a theme, each shown as it's highlighted", Run: func(ctx registry.Ctx, _ registry.Args) error {
		return h.Run(ctx, "picker.open", registry.Args{"source": "themes"})
	}}))
	t.Add(h.Keymap(registry.Keymap{Mode: "n", Keys: "<Leader>T", Command: "picker.open", Args: registry.Args{"source": "themes"}, Desc: "Themes"}))
	return nil
}

// themePreviews are the theme picker's resolved themes and highlighted sample, kept until the themes or languages
// change or the picker closes.
type themePreviews struct {
	themes, langs int
	by            map[string]themePreview // by name and variant
	sample        *kit.SourceFile
}

type themePreview struct {
	r   *theme.Resolved
	err error
}

func (p *themePreviews) of(h plugin.Host, name string, v theme.Variant) (*theme.Resolved, error, kit.SourceFile) {
	srcs, themes := h.Themes()
	_, langs := h.Languages()
	if p.by == nil || p.themes != themes || p.langs != langs {
		*p = themePreviews{themes: themes, langs: langs, by: map[string]themePreview{}}
	}
	k := name + "\x00" + string(v)
	t, ok := p.by[k]
	if !ok {
		t.r, t.err = resolveTheme(srcs, name, v)
		p.by[k] = t
	}
	if p.sample == nil {
		sample := strings.TrimRight(themeSample, "\n")
		p.sample = kit.HighlightAround(h, "sample.go", []byte(sample), 1, 0, strings.Count(sample, "\n"))
	}
	return t.r, t.err, *p.sample
}

// themeSwatches are a theme's main colours, in the variant in use (or its own, if it has one), for the picker's preview.
func themeSwatches(h plugin.Host, p *themePreviews, name string) ui.Node {
	variant := theme.Dark
	if cur := h.CurrentTheme(); cur != nil {
		variant = cur.Variant
	}
	r, err, code := p.of(h, name, variant)
	return &ui.Func{Key: "swatches", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		mono := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgMuted)}
		c.Column("swatches", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			shown := variant
			if r != nil {
				shown = r.Variant
			}
			c.Text("name", strings.ToUpper(name)+" · "+string(shown), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.FgFaint)})
			if err != nil {
				c.Text("err", err.Error(), ui.TextStyle{Font: mono.Font, Color: th.C(theme.Error)})
				return
			}
			code.Title, code.Theme, code.Size, code.Fill = "sample.go", r, 13.5, true
			code.Build(c, nil)
			swatches := []struct {
				tok  theme.TokenID
				name string
			}{{theme.BgBase, "bg.base"}, {theme.BgSurface, "bg.surface"}, {theme.BgRaised, "bg.raised"}, {theme.Fg, "fg"},
				{theme.FgMuted, "fg.muted"}, {theme.Accent, "accent"}, {theme.Brand, "brand"}, {theme.OK, "ok"}, {theme.Warn, "warn"},
				{theme.Error, "error"}}
			c.Row("grid", ui.Style{W: ui.Flex(1), Gap: 24}, func(c *ui.Ctx) {
				for col := range 2 {
					c.Column(col, ui.Style{Gap: 8}, func(c *ui.Ctx) {
						for i, sw := range swatches[col*5 : col*5+5] {
							c.Row(i, ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
								c.Box("sw", ui.Style{W: ui.Px(44), H: ui.Px(18), Radius: 4, Fill: r.C(sw.tok), Stroke: th.C(theme.LineStrong), Border: 1}, nil)
								c.Text("n", sw.name, mono)
							})
						}
					})
				}
			})
		})
	}}
}

// themeSample is the code a theme's preview highlights: keywords, types, strings, numbers, comments and calls.
const themeSample = `package pricing

import (
	"errors"
	"fmt"
	"time"
)

// ErrNoTier is returned for a tier nobody has heard of.
var ErrNoTier = errors.New("pricing: no such tier")

// Tier is a plan's price, in pence, and when it changed.
type Tier struct {
	Name    string
	Pence   int
	Changed time.Time
}

// PriceFor is a tier's price for qty seats, with 10% off from 100.
func PriceFor(t *Tier, qty int) (int, error) {
	if t == nil {
		return 0, ErrNoTier
	}
	if qty <= 0 {
		return 0, fmt.Errorf("bad quantity %d", qty)
	}
	total := t.Pence * qty
	if qty >= 100 {
		total = total * 9 / 10 // volume discount
	}
	return total, nil
}
`

// themeNames are the registered themes' names, sorted.
func themeNames(h registry.Registry) []string {
	srcs, _ := h.Themes()
	var out []string
	for _, src := range srcs {
		if !slices.Contains(out, src.Name()) {
			out = append(out, src.Name())
		}
	}
	slices.Sort(out)
	return out
}

// themePicker is :Themes: each theme shows as it's highlighted, Enter keeps it in your settings, and Esc goes back
// to the one before.
func themePicker(h plugin.Host) uireg.PickerSource {
	type prev struct {
		v      any // the user layer's
		had    bool
		source string
		cur    any // what was in use
	}
	var before *prev // the user layer's ui.theme before the first preview
	previews := &themePreviews{}
	o := h.Options()
	restore := func() {
		*previews = themePreviews{}
		if before == nil {
			return
		}
		if before.had {
			o.SetFrom("ui.theme", registry.LayerUser, registry.At{}, before.v, before.source)
		} else {
			o.Unset("ui.theme", registry.LayerUser, registry.At{})
		}
		before = nil
	}
	return uireg.PickerSource{ID: "themes", Title: "Themes", Order: 80, PickerOnly: true, Source: "builtin:themes",
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			cur, _, _ := o.Get("ui.theme", registry.At{})
			if before != nil {
				cur = before.cur
			}
			var out []uireg.PickerItem
			for _, n := range themeNames(h) {
				it := uireg.PickerItem{ID: n, Label: n, Current: n == cur}
				if it.Current {
					it.Note = "current"
				}
				out = append(out, it)
			}
			return out
		},
		Hover: func(it uireg.PickerItem) {
			if before == nil {
				v, had := o.Lookup("ui.theme", registry.LayerUser, registry.At{})
				cur, l, _ := o.Get("ui.theme", registry.At{})
				src := ""
				if l == registry.LayerUser {
					src = o.Source("ui.theme", registry.At{})
				}
				before = &prev{v, had, src, cur}
			}
			o.Set("ui.theme", registry.LayerUser, registry.At{}, it.ID)
		},
		Cancel:  restore,
		Preview: func(it uireg.PickerItem) ui.Node { return themeSwatches(h, previews, it.ID) },
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			restore()
			return h.Run(ctx, "settings.set", registry.Args{"name": "ui.theme", "value": it.ID, "scope": "user"})
		},
	}
}
