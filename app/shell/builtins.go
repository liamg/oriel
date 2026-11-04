package shell

import (
	"context"
	"path/filepath"

	"github.com/liamg/oriel/app/kit"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// parts are the shell's own plugins, made once for as long as the app runs, and what it keeps for the features.
func (sh *shell) parts() Parts {
	sh.layout = &layoutPlugin{sh: sh}
	cfg := sh.cfg
	return Parts{Config: cfg, DirState: sh.dirState, Clipboard: desktopClipboard{&sh.desktop}, Pictures: sh.reads.Pictures, Tools: sh.tools,
		UI: &uiOptions{}, Frames: &frames{}, Themes: &themes{dir: filepath.Join(cfg.ConfigDir, "themes")}, Layout: sh.layout,
		Rail: &rail{sh: sh}, Start: &start{sh: sh}, Overview: &overview{sh: sh}, Introspect: &introspect{configDir: cfg.ConfigDir},
		StatusLine: &statusLine{sh: sh}, SessionTabs: &sessionTabs{sh: sh}, Omnibox: &omnibox{}, Picker: &picker{}, WhichKey: &whichKey{},
		Help: &helpView{configDir: cfg.ConfigDir}, Nav: &navPlugin{sh: sh}, Menus: &contextMenus{}, Notices: &notices{sh: sh},
		Desktop: &desktopPlugin{desktop: &sh.desktop}, Quit: &quitDialog{draining: &sh.draining}}
}

// introspect is :Keys and :Commands: every keymap and command, with where it came from, in a picker.
type introspect struct {
	kit.Disposer
	configDir string
}

func (i *introspect) Manifest() plugin.Manifest { return plugin.Manifest{ID: "introspect"} }

func (i *introspect) Activate(_ context.Context, h plugin.Host) error {
	pick := func(source string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "picker.open", registry.Args{"source": source})
		}
	}
	i.Add(h.Picker(commandsSource(h)))
	i.Add(h.Picker(keymapsSource(h, i.configDir)))
	i.Add(h.Command(registry.Command{ID: "Keys", Title: "List keymaps", Run: pick("keymaps")}))
	i.Add(h.Command(registry.Command{ID: "Commands", Title: "List commands", Run: pick("commands")}))
	return nil
}

// uiOptions registers the toolkit's own options.
type uiOptions struct{ kit.Disposer }

const uiFontSize = kit.UIFontSize

// partSizes are the parts with their own text size: each option scales its part over ui.font_size.
var partSizes = []struct{ name, help string }{
	{"ui.statusline.font_size", "The status line's text size; it follows ui.font_size until it's set."},
	{"ui.hover.font_size", "The text size of hovers and signature help; it follows ui.font_size until it's set."},
	{"ui.completion.font_size", "The completion menu's text size; it follows ui.font_size until it's set."},
	{"ui.tooltip.font_size", "Tooltips' text size; it follows ui.font_size until it's set."},
}

// zoomOf is the window's zoom for ui.font_size.
func zoomOf(o registry.Options) float32 {
	if v, _, err := o.Get("ui.font_size", registry.At{}); err == nil {
		return float32(v.(float64) / uiFontSize)
	}
	return 1
}

func (o *uiOptions) Manifest() plugin.Manifest { return plugin.Manifest{ID: "ui"} }

func (o *uiOptions) Activate(_ context.Context, h plugin.Host) error {
	for _, c := range []registry.Command{
		{ID: "ui.focus.next", Title: "Move keyboard focus to the next control", Run: func(registry.Ctx, registry.Args) error { return o.focus(h, 1) }},
		{ID: "ui.focus.prev", Title: "Move keyboard focus to the previous control", Run: func(registry.Ctx, registry.Args) error { return o.focus(h, -1) }},
	} {
		o.Add(h.Command(c))
	}
	for _, c := range []registry.Command{
		{ID: "ui.drag", Title: "Pick up what's focused to drag it, or drop what's held on it", Run: func(registry.Ctx, registry.Args) error {
			if u := h.UI(); u != nil {
				u.DragKey()
			}
			return nil
		}},
		{ID: "ui.drag.cancel", Title: "Put down what's being dragged", Run: func(registry.Ctx, registry.Args) error {
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
	step := func(by float64) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error {
			opts := h.Options()
			if by == 0 {
				opts.Unset("ui.font_size", registry.LayerUser, registry.At{})
				return nil
			}
			v, _, err := opts.Get("ui.font_size", registry.At{})
			if err != nil {
				return err
			}
			return opts.Set("ui.font_size", registry.LayerUser, registry.At{}, min(32, max(8, v.(float64)+by)))
		}
	}
	for _, c := range []registry.Command{
		{ID: "ui.zoom.in", Title: "Make the UI bigger", Run: step(1)},
		{ID: "ui.zoom.out", Title: "Make the UI smaller", Run: step(-1)},
		{ID: "ui.zoom.reset", Title: "Put the UI back to its size", Run: step(0)},
	} {
		o.Add(h.Command(c))
	}
	for keys, cmd := range map[string]string{"<D-=>": "ui.zoom.in", "<D-->": "ui.zoom.out", "<D-0>": "ui.zoom.reset"} {
		o.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: keys, Command: cmd}))
	}
	o.Add(h.Option(registry.OptionSpec{Name: "ui.reduced_motion", Type: registry.Bool, Default: false,
		Scopes: registry.ScopeUser, Help: "Jump straight to the end of every animation."}))
	o.Add(h.Option(registry.OptionSpec{Name: "ui.scrollbars", Type: registry.Enum, Values: []string{"auto", "always"}, Default: "auto",
		Scopes: registry.ScopeUser, Help: "When scrollbars show: auto, while the pointer is over what scrolls and for a moment after it scrolls; or always, whenever there's more than fits."}))
	o.Add(h.Option(registry.OptionSpec{Name: "ui.theme", Type: registry.String, Default: "oriel", Choices: func() []string { return themeNames(h) },
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
