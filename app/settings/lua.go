package settings

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// LuaPoll is how often init.lua and lua/ are read for changes when the OS can't watch them.
const LuaPoll = time.Second

var (
	tLuaPrompt = theme.Declare("lua.prompt", "accent")
	tLuaOut    = theme.Declare("lua.output", "fg.muted")
	tLuaErr    = theme.Declare("lua.error", "error")
	tHookKind  = theme.Declare("hooks.kind", "status.permission")
	tHookSlow  = theme.Declare("hooks.slow", "warn")
)

// Lua is the Lua config: init.lua and lua/ in the config directory, loaded into one VM with
// the oriel.* API, reloaded when they change, with :Hooks, the :lua REPL and :Inspect over what's registered.
type Lua struct {
	kit.Disposer
	h    plugin.Host
	Dir  string
	vm   *script.VM
	apis *api.APIs
	sum  [32]byte
	err  error
	good bool // the config running loaded without an error

	marks map[string][]registry.Diagnostic // the config's errors, by file

	repl     []replLine
	input    string
	history  []string
	hist     int
	inspect  bool
	modes    []string // the key modes when :Inspect opened, before it took the keys
	loadedAt time.Time
	took     time.Duration
}

type replLine struct {
	text string
	kind int // 0 input, 1 output, 2 error, 3 a frame of its trace
}

// Events features fire later, declared now so hooks on them show what they wait for.
var laterEvents = []registry.EventInfo{
	{Name: "PrRaise", Doc: "A pull request about to be raised; a veto stops it. { project, title }"},
	{Name: "ModeChanged", Doc: "The editor's mode changing (not fired yet). { old, new }"},
}

func (x *Lua) Manifest() plugin.Manifest { return plugin.Manifest{ID: "lua"} }

func (x *Lua) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	for _, e := range laterEvents {
		x.Add(h.DeclareEvent(e))
	}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.View("hooks", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "hooks", Fn: x.hooksView} })))
	x.Add(h.View("lua", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "lua", Fn: x.replView} })))
	x.Add(h.View(kit.OverlayPrefix+"inspect", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "inspect", Fn: x.inspectView}
	})))
	x.Add(h.Contribute(registry.InspectSection{Name: "KEYMAPS HERE", Order: 50, Rows: x.keymapRows, Source: "builtin:lua"}))
	x.Add(h.Contribute(registry.InspectSection{Name: "VIEWS", Order: 60, Rows: x.viewRows, Source: "builtin:lua"}))
	x.Reload(false)
	w := kit.WatchPaths(h, LuaPoll, func() {
		if x.Changed() {
			h.Do(func() { x.Reload(true) })
		}
	})
	w.Add(x.initLua())
	w.AddTree(filepath.Join(x.Dir, "lua"), nil)
	x.Add(kit.OnDispose(func() {
		w.Close()
		if x.apis != nil {
			x.apis.Dispose()
			x.vm.Close()
		}
	}))
	return nil
}

func (x *Lua) commands() []registry.Command {
	return []registry.Command{
		{ID: "lua", Title: "Run a line of Lua, or open the Lua REPL", Run: func(_ registry.Ctx, a registry.Args) error {
			if line, _ := a["args"].(string); strings.TrimSpace(line) != "" {
				x.eval(line)
				return nil
			}
			return x.h.Run(context.Background(), "view.open", registry.Args{"id": "lua", "place": "dock", "edge": "bottom", "title": "Lua", "size": 260})
		}},
		{ID: "lua.reload", Title: "Load init.lua again", Run: func(registry.Ctx, registry.Args) error { x.Reload(true); return nil }},
		{ID: "Hooks", Title: "List every hook, who registered it and its p99 time", Run: func(registry.Ctx, registry.Args) error {
			return x.h.Run(context.Background(), "view.open", registry.Args{"id": "hooks", "place": "dock", "edge": "right", "title": "Hooks", "size": 640})
		}},
		{ID: "Inspect", Title: "What's under the cursor: syntax, highlight, decorations, keymaps and views", Run: func(registry.Ctx, registry.Args) error {
			x.inspect = true
			x.modes = x.h.KeyModes()
			if x.modes == nil {
				x.modes = []string{"n", "global"}
			}
			return nil
		}},
	}
}

func (x *Lua) initLua() string { return filepath.Join(x.Dir, "init.lua") }

// sources is a checksum of init.lua and every file under lua/, to see a save.
func (x *Lua) sources() [32]byte {
	h := sha256.New()
	if b, err := os.ReadFile(x.initLua()); err == nil {
		h.Write(b)
	}
	filepath.WalkDir(filepath.Join(x.Dir, "lua"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".lua") {
			if b, err := os.ReadFile(p); err == nil {
				h.Write([]byte(p))
				h.Write(b)
			}
		}
		return nil
	})
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// Changed says whether the config's files changed since it last loaded.
func (x *Lua) Changed() bool { return x.sources() != x.sum }

// Reload loads the config again, in a new VM. One that loads replaces the config running, and a toast says what
// changed. One that fails leaves the last good config running, and a notice and a diagnostic say where it failed;
// with no good config to keep (at startup), what ran before the error stays.
func (x *Lua) Reload(toast bool) {
	x.sum = x.sources()
	vm := script.New(filepath.Join(x.Dir, "lua"))
	vm.OnSlow = func(src string, took time.Duration) {
		x.h.Log().Warn("slow Lua", "function", src, "took", took)
	}
	vm.OnError = x.mark
	apis := api.Install(vm, x.h, x.Dir)
	x.clearMarks()
	src, err := os.ReadFile(x.initLua())
	if os.IsNotExist(err) {
		x.swap(vm, apis)
		x.err, x.good = nil, true
		x.h.Dismiss("lua.load")
		return
	}
	start := time.Now()
	if err == nil {
		err = vm.Load(script.Chunk{Name: "init.lua", Source: src})
	}
	took := time.Since(start)
	apis.ApplyPending()
	x.err = err
	if err != nil {
		kept := x.apis != nil && x.good
		if kept {
			apis.Revert()
			apis.Dispose()
			vm.Close()
		} else {
			x.swap(vm, apis)
		}
		x.loadError(err, kept)
		return
	}
	before := 0
	if x.apis != nil {
		before = x.apis.Registered()
	}
	x.swap(vm, apis)
	x.good, x.took, x.loadedAt = true, took, time.Now()
	x.h.Dismiss("lua.load")
	if toast {
		x.h.Notify(core.Notice{ID: "lua.reload", Severity: core.SevInfo, Title: "Config",
			Text: fmt.Sprintf("reloaded init.lua · %dms · %d registered (was %d)", took.Milliseconds(), apis.Registered(), before)})
	}
}

// swap makes vm the config running, the one before it gone.
func (x *Lua) swap(vm *script.VM, apis *api.APIs) {
	if x.apis != nil {
		x.apis.Handover(apis)
		x.apis.Dispose()
		x.vm.Close()
	}
	x.vm, x.apis = vm, apis
}

// loadError is the notice for a config that didn't load, with a link to the line.
func (x *Lua) loadError(err error, kept bool) {
	text := err.Error()
	if kept {
		text += " · the last good config is still running"
	}
	n := core.Notice{ID: "lua.load", Severity: core.SevError, Source: "init.lua", Title: "Config error", Text: text}
	var e *script.Error
	if errors.As(err, &e) {
		file, _ := script.SplitLoc(e.Where())
		n = kit.WithTrace(n, e, x.Dir, "Edit "+filepath.Base(cmp.Or(file, "init.lua")))
		x.mark(e)
	}
	x.h.Notify(n)
}

// luaPrefix is what a Lua error's message starts with that a diagnostic on the line doesn't need.
var luaPrefix = regexp.MustCompile(`^[\w./-]+\.lua(:\d+:| line:\d+\(column:\d+\))\s*`)

// mark puts a Lua error on its line in the config, as a diagnostic, until the config next loads.
func (x *Lua) mark(e *script.Error) {
	file, line := script.SplitLoc(e.Where())
	if line == 0 || !strings.HasSuffix(file, ".lua") {
		return
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(x.Dir, file)
	}
	msg := strings.Join(strings.Fields(luaPrefix.ReplaceAllString(kit.FirstLine(e.Message), "")), " ")
	d := registry.Diagnostic{Line: line - 1, Severity: registry.SevError, Message: msg}
	if slices.Contains(x.marks[file], d) {
		return
	}
	if x.marks == nil {
		x.marks = map[string][]registry.Diagnostic{}
	}
	x.marks[file] = append(x.marks[file], d)
	x.h.SetDiagnostics("lua", file, x.marks[file])
}

func (x *Lua) clearMarks() {
	for file := range x.marks {
		x.h.SetDiagnostics("lua", file, nil)
	}
	x.marks = nil
}

// eval runs a line at the REPL: as an expression first, so `1 + 1` shows 2, then as a statement. An error shows
// with its trace; the expression is in a table, so a call in it keeps its frame rather than being a tail call.
func (x *Lua) eval(line string) {
	x.repl = append(x.repl, replLine{text: line})
	x.history = append(x.history, line)
	x.hist = len(x.history)
	out, err := x.vm.Eval(script.Chunk{Name: "repl", Source: []byte("return unpack({ " + line + "\n})")})
	if err != nil && (strings.Contains(err.Error(), "syntax error") || strings.Contains(err.Error(), "parse error")) {
		out, err = x.vm.Eval(script.Chunk{Name: "repl", Source: []byte(line)})
	}
	if err != nil {
		x.repl = append(x.repl, replLine{text: err.Error(), kind: 2})
		if e, ok := err.(*script.Error); ok {
			for _, f := range e.Frames() {
				if !f.Go {
					x.repl = append(x.repl, replLine{text: "  " + f.Loc() + ": " + f.Fn, kind: 3})
				}
			}
			x.mark(e)
		}
		return
	}
	for _, v := range out {
		x.repl = append(x.repl, replLine{text: Show(v), kind: 1})
	}
}

// Show is a Lua value as the REPL prints it.
func Show(v any) string {
	switch v := v.(type) {
	case nil:
		return "nil"
	case string:
		return fmt.Sprintf("%q", v)
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprint(int64(v))
		}
		return fmt.Sprint(v)
	case *script.Fn:
		return "function: " + v.Source
	case []any:
		var parts []string
		for _, x := range v {
			parts = append(parts, Show(x))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case script.Table:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+" = "+Show(v[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return fmt.Sprint(v)
}

var completeRe = regexp.MustCompile(`([A-Za-z_][\w.]*)$`)

// complete is what the REPL offers for the word before the cursor: the names under oriel that it starts.
func (x *Lua) complete(line string) (prefix string, names []string) {
	m := completeRe.FindString(line)
	if m == "" {
		return "", nil
	}
	path, last := "", m
	if i := strings.LastIndexByte(m, '.'); i >= 0 {
		path, last = m[:i], m[i+1:]
	}
	root, rest, _ := strings.Cut(path, ".")
	if root != "oriel" {
		return "", nil
	}
	if path == root {
		rest = ""
	}
	for _, k := range x.vm.Keys(rest) {
		if strings.HasPrefix(k, last) {
			names = append(names, k)
		}
	}
	return m, names
}

func (x *Lua) replView(c *ui.Ctx) {
	th := c.Theme()
	mono := func(tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12}, Color: th.C(tok)}
	}
	c.Column("lua", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 6, Help: "plugins/lua"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			c.Text("t", ":LUA", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.Accent)})
			c.Text("api", "REPL · oriel api "+api.Version, mono(theme.FgFaint))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("keys", "⇥ complete · ↵ eval · ↑ history", mono(theme.FgGhost))
		})
		c.Column("out", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 4, ScrollY: true}, func(c *ui.Ctx) {
			for i, l := range x.repl {
				switch l.kind {
				case 0:
					c.Text(i, "› "+l.text, mono(theme.Fg))
				case 1:
					c.Text(i, l.text, mono(tLuaOut))
				case 3:
					c.Text(i, l.text, mono(theme.FgFaint))
				default:
					c.Text(i, l.text, mono(tLuaErr))
				}
			}
		})
		in := c.Next("input")
		for _, k := range c.Interaction(ui.Child(in, "in")).Keys {
			switch k.Key {
			case "Tab":
				if prefix, names := x.complete(x.input); len(names) > 0 {
					x.input = strings.TrimSuffix(x.input, prefix) + prefix[:len(prefix)-len(lastPart(prefix))] + names[0]
				}
			case "Up":
				if x.hist > 0 {
					x.hist--
					x.input = x.history[x.hist]
				}
			}
		}
		_, names := x.complete(x.input)
		c.Row("input", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("p", "›", mono(tLuaPrompt))
			if widget.Input(c, "in", &x.input, widget.InputOpts{Label: "Lua", Placeholder: "oriel.", Help: "plugins/lua"}).Submitted && x.input != "" {
				x.eval(x.input)
				x.input = ""
			}
		})
		if len(names) > 0 {
			c.Text("complete", strings.Join(names[:min(len(names), 12)], "  "), mono(theme.FgFaint))
		}
	})
}

func lastPart(s string) string {
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// hooksView is :Hooks: every hook by event, what it does, who registered it and its p99, slow ones
// marked against the 2 ms budget, and events nothing fires yet.
func (x *Lua) hooksView(c *ui.Ctx) {
	th := c.Theme()
	hooks := x.h.Hooks()
	fired := map[string]bool{}
	docs := map[string]string{}
	for _, e := range x.h.Events() {
		fired[e.Name], docs[e.Name] = e.Fired, e.Doc
	}
	mono := func(size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
	events := map[string]bool{}
	for _, h := range hooks {
		events[h.Event] = true
	}
	c.Column("hooks", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 6, ScrollX: true, ScrollY: true, Help: "plugins/hooks"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			c.Text("t", ":HOOKS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.Accent)})
			c.Text("n", fmt.Sprintf("%d hooks · %d events", len(hooks), len(events)), mono(10.5, theme.FgFaint))
		})
		cols := []float32{140, 170, 80, 200}
		c.Row("cols", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			for i, h := range []string{"EVENT", "HOOK", "KIND", "SOURCE"} {
				c.Box(i, ui.Style{W: ui.Px(cols[i])}, func(c *ui.Ctx) { c.Text("h", h, mono(9.5, theme.FgFaint)) })
			}
			c.Text("p99", "P99", mono(9.5, theme.FgFaint))
		})
		var slow []string
		for i, h := range hooks {
			kind := h.Kind
			if kind == "" {
				kind = h.Did
			}
			label := h.Label
			if label == "" {
				label = "—"
			}
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(24), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("e", ui.Style{W: ui.Px(cols[0])}, func(c *ui.Ctx) { c.Text("t", h.Event, mono(11.5, theme.Fg)) })
				c.Box("l", ui.Style{W: ui.Px(cols[1]), Clip: true}, func(c *ui.Ctx) { c.Text("t", label, mono(11.5, theme.FgMuted)) })
				c.Box("k", ui.Style{W: ui.Px(cols[2])}, func(c *ui.Ctx) {
					c.Text("t", strings.ToUpper(cmp.Or(kind, "—")), mono(9.5, tHookKind))
				})
				c.Box("s", ui.Style{W: ui.Px(cols[3]), Clip: true}, func(c *ui.Ctx) { c.Text("t", h.Source, mono(11, theme.Accent)) })
				switch {
				case h.Off != nil:
					c.Text("off", "off: "+h.Off.Error(), mono(10.5, theme.Error))
				case !fired[h.Event]:
					c.Text("wait", "waits: "+cmp.Or(docs[h.Event], "nothing fires it yet"), mono(10, theme.FgGhost))
				case h.Calls == 0:
					c.Text("p", "—", mono(11, theme.FgGhost))
				default:
					tok := theme.FgMuted
					if h.P99 > 2*time.Millisecond {
						tok = tHookSlow
						slow = append(slow, fmt.Sprintf("%s on %s (%s)", h.Source, h.Event, h.P99))
					}
					c.Text("p", kit.Duration(h.P99), mono(11, tok))
				}
			})
		}
		for i, s := range slow {
			c.Text(fmt.Sprint("slow", i), "⚠ "+s+" is over the 2 ms hot-path budget", mono(11, tHookSlow))
		}
		if pending := x.apis.Pending(); len(pending) > 0 {
			keys := make([]string, 0, len(pending))
			for k, w := range pending {
				keys = append(keys, k+" ("+w+")")
			}
			slices.Sort(keys)
			c.Text("pending", "set before anything registered them: "+strings.Join(keys, ", "), mono(10.5, theme.FgFaint))
		}
	})
}

// keymapRows are the keymaps in the modes keys go through now, the shadowed ones faded.
func (x *Lua) keymapRows() []registry.InspectRow {
	var out []registry.InspectRow
	won := map[string]bool{}
	for _, m := range x.modes {
		for _, k := range x.h.Keymaps() {
			if k.Mode != m || k.Command == "" {
				continue
			}
			out = append(out, registry.InspectRow{Key: k.Keys, Value: k.Command, Source: k.Source, Faded: won[k.Keys]})
			won[k.Keys] = true
		}
	}
	// Yours and plugins' first, then the built-ins.
	slices.SortStableFunc(out, func(a, b registry.InspectRow) int {
		ab, bb := strings.HasPrefix(a.Source, "builtin"), strings.HasPrefix(b.Source, "builtin")
		switch {
		case ab == bb:
			return 0
		case bb:
			return -1
		}
		return 1
	})
	if n := len(out); n > 12 {
		out = append(out[:12], registry.InspectRow{Key: fmt.Sprintf("and %d more", n-12), Value: ":Keys lists them", Faded: true})
	}
	return out
}

// viewRows are the window's views, and which of them something has replaced.
func (x *Lua) viewRows() []registry.InspectRow {
	var out []registry.InspectRow
	for _, id := range []string{kit.TitleBarView, kit.RootView, kit.StatusLineView, kit.EditorView} {
		src := "builtin"
		for _, v := range x.h.Views() {
			if v == id {
				if s, ok := x.h.ViewSource(id); ok {
					src = s
				}
			}
		}
		out = append(out, registry.InspectRow{Key: id, Value: "", Source: src})
	}
	return out
}

// inspectView is :Inspect: every section contributed, over the window.
func (x *Lua) inspectView(c *ui.Ctx) {
	if !x.inspect || c.Theme().IsProbe() {
		return
	}
	th := c.Theme()
	var secs []registry.InspectSection
	for _, s := range x.h.Contributions("inspect.section") {
		secs = append(secs, s.(registry.InspectSection))
	}
	slices.SortFunc(secs, func(a, b registry.InspectSection) int { return a.Order - b.Order })
	size := c.Size()
	at := render.Point{X: size.X / 2, Y: size.Y / 2}
	id := c.Next("inspect")
	for _, k := range c.Interaction(id).Keys {
		if k.Key == "Esc" || k.Key == "q" {
			x.inspect = false
			return
		}
	}
	mono := func(size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
	c.Column("inspect", ui.Style{W: ui.Px(min(1000, size.X-40)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 8, Clip: true,
		Z: ui.LayerDialog, Float: &ui.Float{Point: &at, Self: render.Point{X: .5, Y: .5}}, Enter: &ui.Transition{Fade: true, Shift: render.Point{Y: 6}},
		Focusable: true, Trap: true, KeyMode: "inspect", Role: ui.RoleDialog, Label: "Inspect", Help: "plugins/inspect"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(36), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", ":INSPECT", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.Accent)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			kit.KbdKey(c, "esc")
		})
		for i, s := range secs {
			rows := s.Rows()
			c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row(i, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 8), Gap: 16}, func(c *ui.Ctx) {
				c.Box("name", ui.Style{W: ui.Px(110)}, func(c *ui.Ctx) { c.Text("n", s.Name, mono(9.5, theme.FgFaint)) })
				c.Column("rows", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
					if len(rows) == 0 {
						c.Text("none", "—", mono(11, theme.FgGhost))
					}
					for j, r := range rows {
						key, val, src := theme.Fg, theme.FgMuted, theme.Accent
						if r.Faded {
							key, val, src = theme.FgGhost, theme.FgGhost, theme.FgGhost
						}
						c.Row(j, ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
							c.Box("k", ui.Style{W: ui.Px(220), Clip: true}, func(c *ui.Ctx) { c.Text("t", r.Key, mono(11.5, key)) })
							c.Box("v", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", r.Value, mono(11.5, val)) })
							c.Text("s", r.Source, mono(11, src))
						})
					}
				})
			})
		}
	})
}

// VM is the VM running the user's config, until it's loaded again.
func (x *Lua) VM() *script.VM { return x.vm }

// Err is the error the config last loaded with; nil once it loads.
func (x *Lua) Err() error { return x.err }
