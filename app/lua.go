package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// LuaPoll is how often init.lua and lua/ are read for changes.
const LuaPoll = 500 * time.Millisecond

var (
	tLuaPrompt = theme.Declare("lua.prompt", "accent")
	tLuaOut    = theme.Declare("lua.output", "fg.muted")
	tLuaErr    = theme.Declare("lua.error", "error")
	tHookKind  = theme.Declare("hooks.kind", "status.permission")
	tHookSlow  = theme.Declare("hooks.slow", "warn")
)

// luaExt is the Lua config (scripting.md, PL-1): init.lua and lua/ in the config directory, loaded into one VM with
// the oriel.* API, reloaded when they change, with :Hooks, the :lua REPL and :Inspect over what's registered.
type luaExt struct {
	disposer
	sh   *shell
	h    ext.Host
	dir  string
	vm   *script.VM
	apis *api.APIs
	sum  [32]byte
	err  error

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
	kind int // 0 input, 1 output, 2 error
}

// Events features fire later, declared now so hooks on them show what they wait for (plugins.md, "Go interfaces").
var laterEvents = []registry.EventInfo{
	{Name: "Commit", Doc: "A commit about to be made (FG-6); a veto stops it. { project, message, unread }"},
	{Name: "CommentSend", Doc: "A comment about to go to an agent (AI-5); a rewrite changes it. { thread, body, urgent }"},
	{Name: "Permission", Doc: "An agent asking to run a tool (AI-7); a decision answers it. { session, tool, input }"},
	{Name: "AgentTurnEnd", Doc: "An agent's turn ending (AI-2). { session }"},
	{Name: "PrRaise", Doc: "A pull request about to be raised (FG-6); a veto stops it. { project, title }"},
	{Name: "ModeChanged", Doc: "The editor's mode changing (a later ticket). { old, new }"},
}

func (x *luaExt) Manifest() ext.Manifest { return ext.Manifest{ID: "lua"} }

func (x *luaExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	for _, e := range laterEvents {
		x.add(h.DeclareEvent(e))
	}
	for _, c := range []registry.Command{
		{ID: "lua", Title: "Run a line of Lua, or open the Lua REPL", Run: func(_ registry.Ctx, a registry.Args) error {
			if line, _ := a["args"].(string); strings.TrimSpace(line) != "" {
				x.eval(line)
				return nil
			}
			return h.Run(context.Background(), "view.open", registry.Args{"id": "lua", "place": "dock", "edge": "bottom", "title": "Lua", "size": 260})
		}},
		{ID: "lua.reload", Title: "Load init.lua again", Run: func(registry.Ctx, registry.Args) error { x.reload(true); return nil }},
		{ID: "Hooks", Title: "List every hook, who registered it and its p99 time", Run: func(registry.Ctx, registry.Args) error {
			return h.Run(context.Background(), "view.open", registry.Args{"id": "hooks", "place": "dock", "edge": "right", "title": "Hooks", "size": 640})
		}},
		{ID: "Inspect", Title: "What's under the cursor: syntax, highlight, decorations, keymaps and views", Run: func(registry.Ctx, registry.Args) error {
			x.inspect = true
			x.modes = []string{"n", "global"}
			if x.sh.modes != nil {
				x.modes = x.sh.modes()
			}
			return nil
		}},
	} {
		x.add(h.Command(c))
	}
	x.add(h.View("hooks", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "hooks", Fn: x.hooksView} })))
	x.add(h.View("lua", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "lua", Fn: x.replView} })))
	x.add(h.View(OverlayPrefix+"inspect", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "inspect", Fn: x.inspectView}
	})))
	x.add(h.Contribute(registry.InspectSection{Name: "KEYMAPS HERE", Order: 50, Rows: x.keymapRows, Source: "builtin:lua"}))
	x.add(h.Contribute(registry.InspectSection{Name: "VIEWS", Order: 60, Rows: x.viewRows, Source: "builtin:lua"}))
	x.reload(false)
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(LuaPoll)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if x.changed() {
					h.Do(func() { x.reload(true) })
				}
			}
		}
	}()
	x.add(disposeFn(func() {
		close(stop)
		if x.apis != nil {
			x.apis.Dispose()
			x.vm.Close()
		}
	}))
	return nil
}

func (x *luaExt) initLua() string { return filepath.Join(x.dir, "init.lua") }

// sources is a checksum of init.lua and every file under lua/, to see a save.
func (x *luaExt) sources() [32]byte {
	h := sha256.New()
	if b, err := os.ReadFile(x.initLua()); err == nil {
		h.Write(b)
	}
	filepath.WalkDir(filepath.Join(x.dir, "lua"), func(p string, d fs.DirEntry, err error) error {
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

func (x *luaExt) changed() bool { return x.sources() != x.sum }

// luaLine finds where an error is: "init.lua:12" from a runtime error, "init.lua line:12(column:3)" from a syntax one.
var luaLine = regexp.MustCompile(`([\w./-]+\.lua)(?::| line:)(\d+)`)

// reload drops everything the config registered and loads it again in a new VM. A reload says so in a toast, and
// what changed; an error says where.
func (x *luaExt) reload(toast bool) {
	before := 0
	if x.apis != nil {
		before = x.apis.Registered()
		x.apis.Dispose()
		x.vm.Close()
	}
	x.sum = x.sources()
	x.vm = script.New(filepath.Join(x.dir, "lua"))
	x.vm.OnSlow = func(src string, took time.Duration) {
		x.h.Log().Warn("slow Lua", "function", src, "took", took)
	}
	x.apis = api.Install(x.vm, x.h)
	src, err := os.ReadFile(x.initLua())
	if os.IsNotExist(err) {
		x.err = nil
		return
	}
	start := time.Now()
	if err == nil {
		err = x.vm.Load(script.Chunk{Name: "init.lua", Source: src})
	}
	x.took, x.loadedAt, x.err = time.Since(start), time.Now(), err
	x.apis.ApplyPending()
	if err != nil {
		loc := ""
		if m := luaLine.FindStringSubmatch(err.Error()); m != nil {
			loc = m[1] + ":" + m[2]
		} else if strings.Contains(err.Error(), "at EOF") {
			loc = fmt.Sprintf("init.lua:%d", strings.Count(strings.TrimRight(string(src), "\n"), "\n")+1)
		}
		x.h.Notify(core.Notice{ID: "lua.load", Severity: core.SevError, Title: "Config error", Text: err.Error(), Location: loc})
		return
	}
	if toast {
		after := x.apis.Registered()
		x.h.Notify(core.Notice{ID: "lua.reload", Severity: core.SevInfo, Title: "Config",
			Text: fmt.Sprintf("reloaded init.lua · %dms · %d registered (was %d)", x.took.Milliseconds(), after, before)})
	}
}

// eval runs a line at the REPL: as an expression first, so `1 + 1` shows 2, then as a statement.
func (x *luaExt) eval(line string) {
	x.repl = append(x.repl, replLine{text: line})
	x.history = append(x.history, line)
	x.hist = len(x.history)
	out, err := x.vm.Eval(script.Chunk{Name: "repl", Source: []byte("local L = oriel\nreturn " + line)})
	if err != nil && strings.Contains(err.Error(), "syntax error") || err != nil && strings.Contains(err.Error(), "parse error") {
		out, err = x.vm.Eval(script.Chunk{Name: "repl", Source: []byte("local L = oriel\n" + line)})
	}
	if err != nil {
		x.repl = append(x.repl, replLine{text: err.Error(), kind: 2})
		return
	}
	for _, v := range out {
		x.repl = append(x.repl, replLine{text: show(v), kind: 1})
	}
}

// show is a Lua value as the REPL prints it.
func show(v any) string {
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
			parts = append(parts, show(x))
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
			parts = append(parts, k+" = "+show(v[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return fmt.Sprint(v)
}

var completeRe = regexp.MustCompile(`([A-Za-z_][\w.]*)$`)

// complete is what the REPL offers for the word before the cursor: the names under oriel (or L) that it starts.
func (x *luaExt) complete(line string) (prefix string, names []string) {
	m := completeRe.FindString(line)
	if m == "" {
		return "", nil
	}
	path, last := "", m
	if i := strings.LastIndexByte(m, '.'); i >= 0 {
		path, last = m[:i], m[i+1:]
	}
	root, rest, _ := strings.Cut(path, ".")
	if root != "oriel" && root != "L" {
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

func (x *luaExt) replView(c *ui.Ctx) {
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
			if widget.Input(c, "in", &x.input, widget.InputOpts{Label: "Lua", Placeholder: "L.", Help: "plugins/lua"}).Submitted && x.input != "" {
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

// hooksView is :Hooks (19-config): every hook by event, what it does, who registered it and its p99, slow ones
// marked against the 2 ms budget, and events nothing fires yet.
func (x *luaExt) hooksView(c *ui.Ctx) {
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
	c.Column("hooks", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 6, ScrollY: true, Help: "plugins/hooks"}, func(c *ui.Ctx) {
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
					c.Text("t", strings.ToUpper(cmpOr(kind, "—")), mono(9.5, tHookKind))
				})
				c.Box("s", ui.Style{W: ui.Px(cols[3]), Clip: true}, func(c *ui.Ctx) { c.Text("t", h.Source, mono(11, theme.Accent)) })
				switch {
				case h.Off != nil:
					c.Text("off", "off: "+h.Off.Error(), mono(10.5, theme.Error))
				case !fired[h.Event]:
					c.Text("wait", "waits: "+cmpOr(docs[h.Event], "nothing fires it yet"), mono(10, theme.FgGhost))
				case h.Calls == 0:
					c.Text("p", "—", mono(11, theme.FgGhost))
				default:
					tok := theme.FgMuted
					if h.P99 > 2*time.Millisecond {
						tok = tHookSlow
						slow = append(slow, fmt.Sprintf("%s on %s (%s)", h.Source, h.Event, h.P99))
					}
					c.Text("p", fmtDur(h.P99), mono(11, tok))
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

func cmpOr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func fmtDur(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}

// keymapRows are the keymaps in the modes keys go through now, the shadowed ones faded (20-inspect).
func (x *luaExt) keymapRows() []registry.InspectRow {
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
func (x *luaExt) viewRows() []registry.InspectRow {
	var out []registry.InspectRow
	for _, id := range []string{TitleBarView, RootView, StatusLineView, EditorView} {
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

// inspectView is :Inspect (20-inspect): every section contributed, over the window.
func (x *luaExt) inspectView(c *ui.Ctx) {
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
	c.Column("inspect", ui.Style{W: ui.Px(min(1000, size.X-40)), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder), Border: 1, Radius: 8, Clip: true,
		Z: 1000, Float: &ui.Float{Point: &at, Self: render.Point{X: .5, Y: .5}}, Enter: &ui.Transition{Fade: true, Shift: render.Point{Y: 6}},
		Focusable: true, Trap: true, KeyMode: "inspect", Role: ui.RoleDialog, Label: "Inspect", Help: "plugins/inspect"}, func(c *ui.Ctx) {
		if !c.Interaction(id).Focused {
			c.Focus(id)
		}
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(36), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", ":INSPECT", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.Accent)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			kbdKey(c, "esc")
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
