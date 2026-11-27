package shell

import (
	"context"
	"errors"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/draw"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/services"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

// parts are the shell's own plugins, made once for as long as the app runs, and what it keeps for the features.
func (sh *shell) parts() Parts {
	sh.layout = &layoutPlugin{sh: sh}
	cfg := sh.cfg
	return Parts{Config: cfg, DirState: sh.dirState, WorkDir: func() string { return sh.cfg.WorkDir }, Clipboard: desktopClipboard{&sh.desktop}, Pictures: sh.reads.Pictures, Tools: sh.tools,
		UI: &uiOptions{}, Frames: &frames{}, Layout: sh.layout,
		Start:      &start{sh: sh},
		StatusLine: &statusLine{},
		Nav:        &navPlugin{sh: sh}, Notices: &notices{sh: sh},
		Desktop: &desktopPlugin{desktop: &sh.desktop}, Quit: &quitDialog{draining: &sh.draining}}
}

// uiOptions registers the toolkit's own options.
type uiOptions struct{ kit.Disposer }

const uiFontSize = draw.UIFontSize

// partSizes are the parts with their own text size: each option scales its part over ui.font_size.
var partSizes = []struct{ name, help string }{
	{"ui.statusline.font_size", "The status line's text size; it follows ui.font_size until it's set."},
	{"ui.hover.font_size", "The text size of hovers and signature help; it follows ui.font_size until it's set."},
	{"ui.completion.font_size", "The completion menu's text size; it follows ui.font_size until it's set."},
	{"ui.tooltip.font_size", "Tooltips' text size; it follows ui.font_size until it's set."},
}

// zoomOf is the window's zoom for ui.font_size.
func zoomOf(o registry.Options) float32 {
	if v, ok := registry.Option[float64](o, "ui.font_size", registry.At{}); ok {
		return float32(v / uiFontSize)
	}
	return 1
}

func (o *uiOptions) Manifest() plugin.Manifest { return plugin.Manifest{ID: "ui"} }

func (o *uiOptions) Activate(_ context.Context, h plugin.Host) error {
	for _, c := range []registry.Command{
		{ID: "ui.focus.next", Grant: registry.Open, Title: "Move keyboard focus to the next control", Args: registry.NoArgs, Run: func(registry.Ctx, registry.Args) error { return o.focus(h, 1) }},
		{ID: "ui.focus.prev", Grant: registry.Open, Title: "Move keyboard focus to the previous control", Args: registry.NoArgs, Run: func(registry.Ctx, registry.Args) error { return o.focus(h, -1) }},
	} {
		o.Add(h.Command(c))
	}
	for _, c := range []registry.Command{
		{ID: "ui.drag", Grant: registry.Open, Title: "Pick up what's focused to drag it, or drop what's held on it", Args: registry.NoArgs, Run: func(registry.Ctx, registry.Args) error {
			if u := h.UI(); u != nil {
				u.DragKey()
			}
			return nil
		}},
		{ID: "ui.drag.cancel", Grant: registry.Open, Title: "Put down what's being dragged", Args: registry.NoArgs, Run: func(registry.Ctx, registry.Args) error {
			if u := h.UI(); u != nil {
				u.CancelDrag()
			}
			return nil
		}},
	} {
		o.Add(h.Command(c))
	}
	for _, mode := range []string{"button", ui.DragKeyMode} {
		o.Add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<Tab>", Command: "ui.focus.next", Desc: "Next control"}))
		o.Add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<S-Tab>", Command: "ui.focus.prev", Desc: "Previous control"}))
	}
	o.Add(h.Keymap(registry.Keymap{Mode: ui.DragKeyMode, Keys: "<Space>", Command: "ui.drag", Desc: "Pick up or drop"}))
	o.Add(h.Keymap(registry.Keymap{Mode: ui.DragKeyMode, Keys: "<Esc>", Command: "ui.drag.cancel", Desc: "Put down"}))
	o.Add(h.Option(registry.OptionSpec{Name: "ui.font_size", Type: registry.Float, Default: uiFontSize, Min: 8, Max: 32, Step: 0.5,
		Scopes: registry.ScopeUser, Help: "The UI's text size. It zooms the whole window, so panels grow with their text; ⌘= and ⌘- step it, ⌘0 resets it."}))
	for _, p := range partSizes {
		o.Add(h.Option(registry.OptionSpec{Name: p.name, Type: registry.Float, Default: uiFontSize, Min: 6, Max: 32, Step: 0.5, Follows: "ui.font_size",
			Scopes: registry.ScopeUser, Help: p.help}))
	}
	o.fonts(h)
	step := func(by float64) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error {
			opts := h.Options()
			if by == 0 {
				opts.Unset("ui.font_size", registry.LayerUser, registry.At{})
				return nil
			}
			v, ok := registry.Option[float64](opts, "ui.font_size", registry.At{})
			if !ok {
				return errors.New("ui.font_size isn't a number")
			}
			return opts.Set("ui.font_size", registry.LayerUser, registry.At{}, min(32, max(8, v+by)))
		}
	}
	for _, c := range []registry.Command{
		{ID: "ui.zoom.in", Grant: registry.Open, Title: "Make the UI bigger", Args: registry.NoArgs, Run: step(1)},
		{ID: "ui.zoom.out", Grant: registry.Open, Title: "Make the UI smaller", Args: registry.NoArgs, Run: step(-1)},
		{ID: "ui.zoom.reset", Grant: registry.Open, Title: "Put the UI back to its size", Args: registry.NoArgs, Run: step(0)},
	} {
		o.Add(h.Command(c))
	}
	for keys, cmd := range map[string]string{"<D-=>": "ui.zoom.in", "<D-->": "ui.zoom.out", "<D-0>": "ui.zoom.reset"} {
		o.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: keys, Command: cmd}))
	}
	o.Add(h.Option(registry.OptionSpec{Name: "ui.reduced_motion", Type: registry.Bool, Default: false,
		Scopes: registry.ScopeUser, Help: "Jump straight to the end of every animation, and show animated GIFs and WebPs still."}))
	o.Add(h.Option(registry.OptionSpec{Name: "ui.scrollbars", Type: registry.Enum, Values: []string{"auto", "always"}, Default: "auto",
		Scopes: registry.ScopeUser, Help: "When scrollbars show: auto, while the pointer is over what scrolls and for a moment after it scrolls; or always, whenever there's more than fits."}))
	o.Add(h.Option(registry.OptionSpec{Name: "ui.theme", Type: registry.String, Default: "oriel", Choices: func() []string { return kit.ThemeNames(h) },
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme, by name: a built-in, or a file in your themes directory. :Themes previews them."}))
	o.Add(h.Option(registry.OptionSpec{Name: "ui.variant", Type: registry.Enum, Values: []string{"system", "dark", "light"}, Default: "system",
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The theme's dark or light variant; system follows the OS."}))
	return nil
}

func (o *uiOptions) focus(h plugin.Host, dir int) error {
	if u := h.UI(); u != nil {
		u.FocusNext(dir)
	}
	return nil
}

// fontRoles are the options that pick an installed family for each of the window's roles.
var fontRoles = []struct{ name, role, help string }{
	{"ui.font", typeset.UI, "The font the UI's text is set in: a family installed on this machine; Oriel's own (Inter) when empty."},
	{"ui.font_mono", typeset.Mono, "The fixed-width font: the editor, terminals, and the labels set in it. A family installed on this " +
		"machine; Oriel's own (JetBrains Mono) when empty."},
}

// fonts are the font options, each drawing its role with the family it names as it changes.
func (o *uiOptions) fonts(h plugin.Host) {
	installed := func() []string {
		if f, ok := services.FontsOf(h); ok {
			return f.Families(h.Redraw)
		}
		return nil
	}
	for _, r := range fontRoles {
		o.Add(h.Option(registry.OptionSpec{Name: r.name, Type: registry.String, Default: "", Choices: installed, Scopes: registry.ScopeUser, Help: r.help}))
		apply := func() {
			f, ok := services.FontsOf(h)
			if !ok {
				return
			}
			family, _ := registry.Option[string](h.Options(), r.name, registry.At{})
			f.Use(r.role, strings.TrimSpace(family), func(err error) {
				if err != nil {
					kit.NotifyFailure(h, core.Notice{ID: "ui.font", Source: "ui", Title: r.name}, err)
				} else {
					h.Dismiss("ui.font")
				}
			})
		}
		o.Add(h.Options().Watch(r.name, apply))
		if v, _, _ := h.Options().Get(r.name, registry.At{}); v != "" && v != nil {
			apply()
		}
	}
}
