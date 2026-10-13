package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/regex"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=keymap
type Keymap struct{ a *APIs }

//oriel:api doc="Map keys in one or more modes (n, i, x, a panel's own) to a command id, or a Lua function. opts: desc, args." optional=opts
func (k Keymap) Set(modes []string, lhs string, rhs any, opts script.Table, where string) error {
	cmd := ""
	switch v := rhs.(type) {
	case string:
		cmd = v
	case *script.Fn:
		cmd = k.a.anon(v)
	default:
		return fmt.Errorf("%s: the right-hand side is a command id or a function, not %T", lhs, rhs)
	}
	args, _ := opts["args"].(script.Table)
	for _, m := range modes {
		k.a.own(k.a.Host.Keymap(registry.Keymap{Mode: m, Keys: lhs, Command: cmd, Args: registry.Args(args), Desc: str(opts, "desc"), Source: where}))
	}
	return nil
}

//oriel:api name=view
type View struct{ a *APIs }

//oriel:api doc="Register a view under id, replacing any of that id (a built-in panel, the status line): fn(ctx) returns oriel.ui nodes."
func (v View) Override(id string, fn *script.Fn, where string) error {
	if fn == nil {
		return fmt.Errorf("%s: no function", id)
	}
	v.a.own(v.a.Host.View(id, registry.SourcedView{Source: where, View: registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: id, Fn: func(c *ui.Ctx) {
			out, err := v.a.VM.Call(fn, v.a.viewCtx(vc))
			v.a.Host.Report(registry.KindView, id, err)
			if err != nil {
				c.Text("error", id+": "+err.Error(), ui.TextStyle{Color: c.Theme().C(theme.Error)})
				return
			}
			if len(out) > 0 {
				build(c, out[0], v.a)
			}
		}}
	})}))
	return nil
}

// viewCtx is what a Lua view gets: the window's size, and the session in front.
func (a *APIs) viewCtx(vc registry.ViewCtx) script.Table {
	t := script.Table{"width": vc.Width, "height": vc.Height, "instance": vc.Instance}
	sess := script.Table{}
	here := a.Host.Here()
	if s, ok := a.Host.Session(coreSession(here.Session)); ok {
		sess = script.Table{"id": string(s.ID()), "name": s.Name(), "kind": string(s.Kind()), "status": string(s.Status())}
	}
	t["session"] = sess
	return t
}

//oriel:api name=decor
type Decor struct{ a *APIs }

//oriel:api doc="Decorate the lines in view: fn(buf, top, bottom) returns a list of { line, sign, hl, text } (1-based lines)."
func (d Decor) Provider(name string, fn *script.Fn) error {
	d.a.own(d.a.Host.Decorations(name, registry.DecorationFunc(func(ctx registry.DecorationCtx) []registry.Decoration {
		out, err := d.a.VM.Call(fn, ctx.Name, ctx.Top+1, ctx.Bottom+1)
		d.a.Host.Report(registry.KindDecoration, name, err)
		if err != nil {
			return nil
		}
		if len(out) == 0 {
			return nil
		}
		items, _ := out[0].([]any)
		var ds []registry.Decoration
		for _, it := range items {
			t, ok := it.(script.Table)
			if !ok {
				continue
			}
			line, _ := t["line"].(float64)
			if s := str(t, "sign"); s != "" {
				ds = append(ds, registry.Decoration{Kind: registry.Sign, Line: int(line) - 1, Text: s, Style: str(t, "hl")})
			}
			if s := str(t, "text"); s != "" {
				ds = append(ds, registry.Decoration{Kind: registry.VirtualText, Line: int(line) - 1, Text: s, Style: str(t, "hl")})
			}
		}
		return ds
	})))
	return nil
}

//oriel:api name=agent
type Agent struct{ a *APIs }

//oriel:api doc="Offer a tool to the session's agent over MCP: { name, desc, params = { name = 'string' }, run = fn(p) }."
func (g Agent) Tool(spec script.Table, where string) error {
	run, _ := spec["run"].(*script.Fn)
	if str(spec, "name") == "" || run == nil {
		return fmt.Errorf("a tool needs a name and a run function")
	}
	params := map[string]string{}
	if p, ok := spec["params"].(script.Table); ok {
		for k, v := range p {
			params[k] = fmt.Sprint(v)
		}
	}
	g.a.own(g.a.Host.Contribute(registry.AgentTool{Name: str(spec, "name"), Desc: str(spec, "desc"), Params: params, Source: where,
		Run: func(_ registry.Ctx, p map[string]any) (any, error) {
			out, err := g.a.VM.Call(run, p)
			if err != nil || len(out) == 0 {
				return nil, err
			}
			return out[0], nil
		}}))
	return nil
}

//oriel:api doc="A prompt pack: { prompt, tools }, for sessions to start with."
func (g Agent) Pack(name string, spec script.Table, where string) error {
	g.a.own(g.a.Host.Contribute(registry.PromptPack{Name: name, Prompt: str(spec, "prompt"), Tools: strs(spec["tools"]), Source: where}))
	return nil
}

//oriel:api name=session
type Session struct{ a *APIs }

//oriel:api doc="A kind of session: { pack, gates, status }."
func (s Session) Kind(name string, spec script.Table, where string) error {
	s.a.own(s.a.Host.Contribute(registry.SessionKind{Name: name, Pack: str(spec, "pack"), Gates: strs(spec["gates"]), Status: str(spec, "status"),
		Source: where}))
	return nil
}

//oriel:api name=hud
type HUD struct{ a *APIs }

//oriel:api doc="A HUD tat: opts { slot, every = '30s' }, fn() returns oriel.ui nodes."
func (h HUD) Tat(name string, opts script.Table, fn *script.Fn, where string) error {
	every, _ := time.ParseDuration(str(opts, "every"))
	h.a.own(h.a.Host.Contribute(registry.Tat{Name: name, Slot: str(opts, "slot"), Every: every, Source: where,
		Build: func(registry.Ctx) (ui.Node, error) {
			out, err := h.a.VM.Call(fn)
			if err != nil {
				return nil, err
			}
			var v any
			if len(out) > 0 {
				v = out[0]
			}
			return &ui.Func{Key: name, Fn: func(c *ui.Ctx) { build(c, v, h.a) }}, nil
		}}))
	return nil
}

//oriel:api name=status
type Status struct{ a *APIs }

//oriel:api doc="A status rule: fn(session) returns a status colour name, or nil to leave it."
func (s Status) Rule(fn *script.Fn, where string) error {
	s.a.own(s.a.Host.Contribute(registry.StatusRule{Name: where, Source: where, Rule: func(sess map[string]any) (string, error) {
		out, err := s.a.VM.Call(fn, sess)
		if err != nil || len(out) == 0 {
			return "", err
		}
		v, _ := out[0].(string)
		return v, nil
	}}))
	return nil
}

//oriel:api name=regex
type Regex struct{ a *APIs }

func compile(pat, flags string) (*regex.Regex, error) {
	var f regex.Flags
	for _, c := range flags {
		switch c {
		case 'i':
			f.Caseless = true
		case 'm':
			f.Multiline = true
		case 's':
			f.DotAll = true
		case 'x':
			f.Extended = true
		case 'n':
			f.NoAuto = true
		}
	}
	return regex.Compile(pat, f)
}

//oriel:api doc="The first PCRE2 match of pat in s: its start and end (1-based, inclusive, as string.find), then its groups; nil if none. flags: imsxn." optional=flags returns="integer?,integer?,string..."
func (Regex) Find(pat, s, flags string) ([]any, error) {
	re, err := compile(pat, flags)
	if err != nil {
		return nil, err
	}
	loc, err := re.FindIndex(s, 0)
	if err != nil || loc == nil {
		return []any{nil}, err
	}
	out := []any{loc[0] + 1, loc[1]}
	for i := 2; i < len(loc); i += 2 {
		if loc[i] < 0 {
			out = append(out, nil)
			continue
		}
		out = append(out, s[loc[i]:loc[i+1]])
	}
	return out, nil
}

//oriel:api doc="Whether pat (PCRE2) matches s." optional=flags returns=boolean
func (Regex) Match(pat, s, flags string) (bool, error) {
	re, err := compile(pat, flags)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

//oriel:api doc="Every match of pat in s, each { start, finish, groups }." optional=flags returns=table
func (Regex) FindAll(pat, s, flags string) ([]any, error) {
	re, err := compile(pat, flags)
	if err != nil {
		return nil, err
	}
	locs, err := re.FindAllIndex(s, -1)
	var out []any
	for _, loc := range locs {
		var groups []any
		for i := 2; i < len(loc); i += 2 {
			if loc[i] >= 0 {
				groups = append(groups, s[loc[i]:loc[i+1]])
			} else {
				groups = append(groups, false)
			}
		}
		out = append(out, script.Table{"start": loc[0] + 1, "finish": loc[1], "groups": groups})
	}
	return out, err
}

//oriel:api doc="s with every match of pat replaced by repl ($1, ${name}, $$), and how many." optional=flags returns="string,integer"
func (Regex) Replace(pat, s, repl, flags string) ([]any, error) {
	re, err := compile(pat, flags)
	if err != nil {
		return nil, err
	}
	out, n, err := re.ReplaceAll(s, repl)
	return []any{out, n}, err
}

//oriel:api name=language
type Language struct{ a *APIs }

//oriel:api doc="Add a language: { name, extensions, filenames, shebangs, grammar = 'path/to/grammar.wasm', queries = { highlights = '…' } or a directory of .scm files }."
func (l Language) Add(spec script.Table, where string) error {
	name := str(spec, "name")
	if name == "" {
		return fmt.Errorf("a language needs a name")
	}
	lang := registry.Language{Name: name, Extensions: strs(spec["extensions"]), Filenames: strs(spec["filenames"]),
		Shebangs: strs(spec["shebangs"]), Queries: map[string]string{}, Source: where}
	if g := expand(str(spec, "grammar")); g != "" {
		lang.Grammar = func() ([]byte, error) { return os.ReadFile(g) }
	}
	switch q := spec["queries"].(type) {
	case script.Table:
		for k, v := range q {
			if s, ok := v.(string); ok {
				lang.Queries[k] = s
			}
		}
	case string:
		files, _ := filepath.Glob(filepath.Join(expand(q), "*.scm"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			lang.Queries[strings.TrimSuffix(filepath.Base(f), ".scm")] = string(b)
		}
	}
	l.a.own(l.a.Host.Language(lang))
	return nil
}
