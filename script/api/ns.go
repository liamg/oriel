package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/regex"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=keymap cap=ui
type Keymap struct{ a *APIs }

//oriel:api doc="Map keys in one or more modes (n, i, x, a panel's own) to a command id, or a Lua function. opts: desc, args, nowait (run at once, not waiting for a longer mapping)." optional=opts
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
	nowait, _ := opts["nowait"].(bool)
	for _, m := range modes {
		k.a.own(k.a.Host.Keymap(registry.Keymap{Mode: m, Keys: lhs, Command: cmd, Args: registry.Args(args), Desc: str(opts, "desc"), Source: where,
			Nowait: nowait}))
	}
	return nil
}

//oriel:api name=view cap=ui
type View struct{ a *APIs }

//oriel:api doc="Register a view under id, replacing any of that id (a built-in panel, the status line): fn(ctx) returns oriel.ui nodes."
func (v View) Override(id string, fn *script.Fn, where string) error {
	if fn == nil {
		return fmt.Errorf("%s: no function", id)
	}
	v.a.own(v.a.Host.View(id, uireg.SourcedView{Source: where, View: uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: id, Fn: func(c *ui.Ctx) {
			out, err := v.a.VM.Call(fn, v.a.viewCtx(vc))
			v.a.Host.Report(registry.KindView, id, err)
			if err != nil {
				c.Text("error", id+": "+err.Error(), ui.TextStyle{Color: c.Theme().C(theme.Error)})
				return
			}
			if len(out) > 0 {
				buildView(c, out[0], v.a)
			}
		}}
	})}))
	return nil
}

// viewCtx is what a Lua view gets: the window's size, the instance and the project it was opened for, and the session in front.
func (a *APIs) viewCtx(vc uireg.ViewCtx) script.Table {
	t := script.Table{"width": vc.Width, "height": vc.Height, "instance": vc.Instance, "project": vc.Project}
	sess := script.Table{}
	here := a.Host.Here()
	if s, ok := a.Host.Session(coreSession(here.Session)); ok {
		sess = script.Table{"id": string(s.ID()), "name": s.Name(), "kind": string(s.Kind()), "status": string(s.Status())}
	}
	t["session"] = sess
	return t
}

//oriel:api name=decor cap=ui
type Decor struct{ a *APIs }

//oriel:api doc="Decorate the lines in view: fn(buf, top, bottom) returns a list of { line, sign, hl, text, bg, command, args, tooltip, menu, on_hover, every_line } (1-based lines; bg draws the text as a chip on that colour; a sign runs command with args when clicked, menu = { { label, command, args } } is what a right-click on it offers, and on_hover shows it only while the pointer is on its line, as the + that comments on a line does; every_line puts the sign on every line instead of line, its args given that line and {line} in its tooltip the number). fn is called again when the buffer's text or the lines in view change, or after decor.changed(name)."
func (d Decor) Provider(name string, fn *script.Fn) error {
	d.a.own(d.a.Host.Decorations(name, uireg.DecorationFunc(func(ctx uireg.DecorationCtx) []uireg.Decoration {
		out, err := d.a.VM.Call(fn, ctx.Name, ctx.Top+1, ctx.Bottom+1)
		d.a.Host.Report(registry.KindDecoration, name, err)
		if err != nil {
			return nil
		}
		if len(out) == 0 {
			return nil
		}
		items, _ := out[0].([]any)
		var ds []uireg.Decoration
		for _, it := range items {
			t, ok := it.(script.Table)
			if !ok {
				continue
			}
			line, _ := t["line"].(float64)
			if s := str(t, "sign"); s != "" {
				if every, _ := t["every_line"].(bool); every {
					ds = append(ds, luaSign(t, uireg.EveryLine, s))
				} else if line >= 1 {
					ds = append(ds, luaSign(t, int(line)-1, s))
				}
			}
			if s := str(t, "text"); s != "" {
				ds = append(ds, uireg.Decoration{Kind: uireg.VirtualText, Line: int(line) - 1, Text: s, Style: str(t, "hl"), Bg: str(t, "bg")})
			}
		}
		return ds
	})))
	return nil
}

// luaSign is a Lua decoration's sign: its command, tooltip and right-click menu.
func luaSign(t script.Table, line int, text string) uireg.Decoration {
	d := uireg.Decoration{Kind: uireg.Sign, Line: line, Text: text, Style: str(t, "hl"), Command: str(t, "command"),
		Args: registry.Args(tableOf(t["args"])), Tooltip: str(t, "tooltip")}
	d.OnHover, _ = t["on_hover"].(bool)
	items, _ := t["menu"].([]any)
	for _, it := range items {
		if m, ok := it.(script.Table); ok {
			d.Menu = append(d.Menu, uireg.SignItem{Label: str(m, "label"), Command: str(m, "command"), Args: registry.Args(tableOf(m["args"]))})
		}
	}
	return d
}

//oriel:api doc="Says the decorations provider name gives have changed, so it's asked again: what they're made from moved."
func (d Decor) Changed(name string) {
	d.a.Host.DecorationsChanged(name)
}

//oriel:api name=agent cap=agent.tools doc="What agents are offered and asked, and the agents themselves: a plugin brings an agent with oriel.agent.provider, served by a process of its own over the Oriel agent protocol ([agent-protocol.md](agent-protocol.md)) or by a bridge such as the bundled ACP one."
type Agent struct{ a *APIs }

//oriel:api doc="Offer a tool to agents over MCP: { name, desc, params = { name = 'string' }, run = fn(p), read_only, reads, for = { 'assistant' }, confirm = fn(p), async, adds, covered_by = { 'mcp.linear.app' } }. for names the agents it's for (session kinds, or the assistant); none is every session's. confirm makes it an act that waits for the user's yes: it returns { title, lead, rows = { { key, value, url } }, note, line }, and run's table { result, line, detail } says what was done. async gives run a second argument, done(result, err), to answer later: after a process or HTTP call. adds names another tool whose answer this one's result joins, under its reads, instead of being a tool of its own. covered_by names MCP servers that already do what it does, by name or part of where they run: an agent that reaches one (its own, or through its sandbox) isn't offered the tool."
func (g Agent) Tool(spec script.Table, where string) error {
	run, _ := spec["run"].(*script.Fn)
	if str(spec, "name") == "" || run == nil {
		return fmt.Errorf("a tool needs a name and a run function")
	}
	if err := g.a.Check(plugin.CapAgentTools, str(spec, "name")); err != nil {
		return err
	}
	params := map[string]string{}
	if p, ok := spec["params"].(script.Table); ok {
		for k, v := range p {
			params[k] = fmt.Sprint(v)
		}
	}
	readOnly, _ := spec["read_only"].(bool)
	async, _ := spec["async"].(bool)
	confirm, _ := spec["confirm"].(*script.Fn)
	t := registry.AgentTool{Name: str(spec, "name"), Desc: str(spec, "desc"), Params: params, Source: where,
		ReadOnly: readOnly, Reads: str(spec, "reads"), For: strs(spec["for"]), Adds: str(spec, "adds"),
		CoveredBy: strs(spec["covered_by"]),
		Run: func(ctx registry.Ctx, p map[string]any) (any, error) {
			var out any
			var err error
			if async {
				out, err = g.later(ctx, run, p)
			} else {
				var res []any
				if res, err = g.a.callOnLoop(ctx, run, p); len(res) > 0 {
					out = res[0]
				}
			}
			if err != nil || confirm == nil {
				return out, err
			}
			if t, ok := out.(script.Table); ok && str(t, "line") != "" {
				return registry.Acted{Result: t["result"], Line: str(t, "line"), Detail: str(t, "detail")}, nil
			}
			return out, nil
		}}
	if confirm != nil {
		t.Confirm = func(ctx registry.Ctx, p map[string]any) (registry.Confirmation, error) {
			out, err := g.a.callOnLoop(ctx, confirm, p)
			if err != nil {
				return registry.Confirmation{}, err
			}
			var c registry.Confirmation
			if len(out) > 0 {
				if t, ok := out[0].(script.Table); ok {
					c = registry.Confirmation{Title: str(t, "title"), Lead: str(t, "lead"), Note: str(t, "note"), Line: str(t, "line")}
					rows, _ := t["rows"].([]any)
					for _, r := range rows {
						if rt, ok := r.(script.Table); ok {
							c.Rows = append(c.Rows, registry.ConfirmRow{Key: str(rt, "key"), Value: str(rt, "value"), URL: str(rt, "url")})
						}
					}
				}
			}
			c.Title = cmp.Or(c.Title, str(spec, "name"))
			c.Line = cmp.Or(c.Line, c.Title)
			return c, nil
		}
	}
	g.a.own(g.a.Host.Contribute(t))
	return nil
}

// callOnLoop calls fn with args on the app's loop, where the VM runs, and waits for what it returns.
func (a *APIs) callOnLoop(ctx context.Context, fn *script.Fn, args ...any) ([]any, error) {
	type answer struct {
		out []any
		err error
	}
	ch := make(chan answer, 1)
	a.Host.Do(func() {
		out, err := a.VM.Call(fn, args...)
		ch <- answer{out, err}
	})
	a.Host.Redraw()
	select {
	case r := <-ch:
		return r.out, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// later runs an async tool's function on the loop and waits for it to call done(result, err).
func (g Agent) later(ctx context.Context, run *script.Fn, p map[string]any) (any, error) {
	type answer struct {
		v   any
		err error
	}
	ch := make(chan answer, 1)
	var once sync.Once
	done := script.GoFunc(func(c *script.Args) ([]any, error) {
		once.Do(func() {
			if msg := c.String(1); msg != "" {
				ch <- answer{err: errors.New(msg)}
				return
			}
			ch <- answer{v: c.Any(0)}
		})
		return nil, nil
	})
	g.a.Host.Do(func() {
		if _, err := g.a.VM.Call(run, p, done); err != nil {
			once.Do(func() { ch <- answer{err: err} })
		}
	})
	g.a.Host.Redraw()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

//oriel:api doc="An explainer: { name, order (lower answers first; the agent's is 10), label, byline, run = fn(req) returning markdown }. req has kind, subject, context, files, depth and format (when set, the shape the answer must take instead of prose). It only answers: explaining changes nothing." cap=agent.read
func (g Agent) Explainer(spec script.Table, where string) error {
	run, _ := spec["run"].(*script.Fn)
	if str(spec, "name") == "" || run == nil {
		return fmt.Errorf("an explainer needs a name and a run function")
	}
	e := registry.Explainer{Name: str(spec, "name"), Source: where,
		Explain: func(_ context.Context, r registry.ExplainRequest) (string, error) {
			out, err := g.a.VM.Call(run, script.Table{"kind": r.Kind, "subject": r.Subject, "context": r.Context, "files": r.Files,
				"depth": cmp.Or(r.Depth, registry.ExplainBrief), "format": r.Format})
			if err != nil || len(out) == 0 {
				return "", err
			}
			s, _ := out[0].(string)
			return s, nil
		}}
	if o, ok := spec["order"].(float64); ok {
		e.Order = int(o)
	}
	if l := str(spec, "label"); l != "" {
		e.Label = func() string { return l }
	}
	if b := str(spec, "byline"); b != "" {
		e.Byline = func() string { return b }
	}
	g.a.own(g.a.Host.Contribute(e))
	return nil
}

//oriel:api doc="A forker, which answers side questions for a session (btw, explain, the verify quiz): { name, order (lower answers first; the agent's is 10), label, run = fn(req) returning the answer }. req has session and prompt. It only answers, with no tools." cap=agent.read
func (g Agent) Forker(spec script.Table, where string) error {
	run, _ := spec["run"].(*script.Fn)
	if str(spec, "name") == "" || run == nil {
		return fmt.Errorf("a forker needs a name and a run function")
	}
	f := registry.Forker{Name: str(spec, "name"), Source: where,
		Fork: func(_ context.Context, r registry.ForkRequest) (string, error) {
			out, err := g.a.VM.Call(run, script.Table{"session": r.Session, "prompt": r.Prompt})
			if err != nil || len(out) == 0 {
				return "", err
			}
			s, _ := out[0].(string)
			return s, nil
		}}
	if o, ok := spec["order"].(float64); ok {
		f.Order = int(o)
	}
	if l := str(spec, "label"); l != "" {
		f.Label = func(string) string { return l }
	}
	g.a.own(g.a.Host.Contribute(f))
	return nil
}

//oriel:api doc="Asks a session's agent a side question, answered outside its conversation with no tools (the first forker's answer: the agent's is a fork of it): req { session (the session in front if omitted, else the newest open one), prompt, timeout (seconds, 180 by default) }. fn(answer, err, label) gets it on the loop; label says who answered: 'claude · fork, no tools'." cap=agent.prompt
func (g Agent) Fork(req script.Table, fn *script.Fn) error {
	prompt := str(req, "prompt")
	if prompt == "" || fn == nil {
		return fmt.Errorf("fork needs a prompt and a function for the answer")
	}
	session := cmp.Or(str(req, "session"), string(g.a.Host.State().Focus))
	if session == "" {
		var newest time.Time
		for _, se := range g.a.Host.Sessions() {
			if st := se.Snapshot(); !st.Archived && (session == "" || st.Created.After(newest)) {
				session, newest = string(st.ID), st.Created
			}
		}
	}
	timeout := 180 * time.Second
	if t, ok := req["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t * float64(time.Second))
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var answer, msg any
		label := ""
		if session == "" {
			msg = "no session's agent to ask"
		} else {
			out, f, err := registry.Fork(ctx, g.a.Host, registry.ForkRequest{Session: session, Prompt: prompt})
			if err != nil {
				msg = err.Error()
			} else {
				answer = out
			}
			if f.Label != nil {
				registry.Call(func() error { label = f.Label(session); return nil })
			}
		}
		g.a.Host.Do(func() { g.a.VM.Call(fn, answer, msg, label) })
	}()
	return nil
}

//oriel:api doc="A prompt pack: { prompt, tools }, for sessions to start with." cap=agent.prompt
func (g Agent) Pack(name string, spec script.Table, where string) error {
	g.a.own(g.a.Host.Contribute(registry.PromptPack{Name: name, Prompt: str(spec, "prompt"), Tools: strs(spec["tools"]), Source: where}))
	return nil
}

//oriel:api name=session cap=session.control
type Session struct{ a *APIs }

//oriel:api doc="A kind of session, shadowing a built-in of the same name: { title, desc, about, from (prompt, pr, name or dir), pack, worktrees (now, start-work, pr, none or init), needs_repo, launcher, order, prompt_file, agent_gate (the agent's own gate: ask, light, auto or bypass; ask by default), opens = { { command, args } }, can = { capabilities: add-projects, ships, owns-changes, next-pr, delete, owns-branch, comments-in-diff, restarts-agent, deletes-on-end }, gates = { ship gate names }, label = fn(session), chips = fn(session), prepare = fn(spec) }. label returns how a session of it reads; chips a list of texts or { text, help } for its card; prepare returns the fields of the spec to change before it starts (name, title, prompt, context, branch, base)."
func (s Session) Kind(name string, spec script.Table, where string) error {
	k, err := luaKind(s.a, name, spec)
	if err != nil {
		return err
	}
	s.a.own(s.a.Host.Contribute(registry.KindEntry{SessionKind: k, Source: where}))
	return nil
}

//oriel:api doc="Chips for every session, after its kind's, wherever a session is described: fn(session) returns a list of texts or { text, help, link }."
func (s Session) Chips(name string, fn *script.Fn, where string) error {
	s.a.own(s.a.Host.Contribute(registry.CardChips{Name: name, Source: where, Chips: func(se core.SessionState) []registry.Chip {
		out, err := s.a.VM.Call(fn, sessionTable(se))
		s.a.Host.Report(registry.KindView, "session.chips."+name, err)
		if err != nil || len(out) == 0 {
			return nil
		}
		return chipsOf(out[0])
	}}))
	return nil
}

//oriel:api doc="Says when a session's work looks finished: fn(session) returns why ('You approved the PR.'), or nil. Closing the tab of a finished session offers to archive or delete it."
func (s Session) Done(name string, fn *script.Fn, where string) error {
	s.a.own(s.a.Host.Contribute(registry.SessionDone{Name: name, Source: where, Done: func(se core.SessionState) string {
		out, err := s.a.VM.Call(fn, sessionTable(se))
		s.a.Host.Report(registry.KindView, "session.done."+name, err)
		if err != nil || len(out) == 0 {
			return ""
		}
		why, _ := out[0].(string)
		return why
	}}))
	return nil
}

//oriel:api doc="Says what ending a session would lose that only your plugin knows of: fn(session) returns what ('Your review isn't sent.'), or nil. Closing or ending the session asks first, and lists it."
func (s Session) Unsaved(name string, fn *script.Fn, where string) error {
	s.a.own(s.a.Host.Contribute(registry.SessionUnsaved{Name: name, Source: where, Unsaved: func(se core.SessionState) string {
		out, err := s.a.VM.Call(fn, sessionTable(se))
		s.a.Host.Report(registry.KindView, "session.unsaved."+name, err)
		if err != nil || len(out) == 0 {
			return ""
		}
		what, _ := out[0].(string)
		return what
	}}))
	return nil
}

//oriel:api doc="Keep a value for a session (the one in front when id is empty) under key, nil to forget it: the window keeps it as the session is switched away from and back, and across restarts, until the session is gone. Use your plugin's name in key. value crosses as JSON."
func (s Session) Keep(id, key string, value any) error {
	return s.a.Host.Keep(s.id(id), key, value)
}

//oriel:api doc="What oriel.session.keep last kept for a session (the one in front when id is empty) under key, or nil." returns=any
func (s Session) Kept(id, key string) (any, error) {
	b, ok := s.a.Host.Kept(s.id(id), key)
	if !ok {
		return nil, nil
	}
	var out any
	return out, json.Unmarshal(b, &out)
}

func (s Session) id(id string) core.SessionID {
	return core.SessionID(cmp.Or(id, string(s.a.Host.State().Focus)))
}

//oriel:api name=hud cap=ui
type HUD struct{ a *APIs }

//oriel:api doc="An indicator in a slot: titlebar.right, statusline.left, statusline.right, hud (a panel of the HUD), or hud.today. opts { slot, order, every = '30s', on = { event names }, title, note, span, help }. build is fn() returning oriel.ui nodes, or the nodes. It's built again every `every`, on each event named in `on`, and by oriel.hud.set or oriel.hud.refresh."
func (h HUD) Indicator(name string, opts script.Table, build any, where string) error {
	every, _ := time.ParseDuration(str(opts, "every"))
	fn, _ := build.(*script.Fn)
	if fn == nil {
		h.a.setIndicator(name, build)
	}
	ind := uireg.Indicator{Name: name, Slot: str(opts, "slot"), Title: str(opts, "title"), Help: str(opts, "help"), Every: every,
		On: strs(opts["on"]), Source: where,
		Build: func(registry.Ctx) (ui.Node, error) {
			v, set := h.a.takeIndicator(name, fn != nil)
			if fn != nil && !set {
				out, err := h.a.VM.Call(fn)
				if err != nil {
					return nil, err
				}
				if len(out) > 0 {
					v = out[0]
				}
			}
			return &ui.Func{Key: name, Fn: func(c *ui.Ctx) { buildView(c, v, h.a) }}, nil
		}}
	if o, ok := opts["order"].(float64); ok {
		ind.Order = int(o)
	}
	if n, ok := opts["span"].(float64); ok {
		ind.Span = int(n)
	}
	if note := str(opts, "note"); note != "" {
		ind.Note = func() string { return note }
	}
	h.a.own(h.a.Host.Contribute(ind))
	return nil
}

//oriel:api doc="Shows nodes in the indicator name now: until it's built again, for one built by a function; for good, for one given its nodes."
func (h HUD) Set(name string, nodes any) error {
	h.a.setIndicator(name, nodes)
	return h.Refresh(name)
}

//oriel:api doc="Builds the indicator name again now."
func (h HUD) Refresh(name string) error {
	return h.a.Host.Run(context.Background(), "hud.refresh", registry.Args{"name": name})
}

//oriel:api name=status cap=ui
type Status struct{ a *APIs }

//oriel:api doc="A status rule: fn(session) returns a status colour name, or nil to leave it. opts: name (one of its own if omitted), which a later rule of the name replaces." optional=opts
func (s Status) Rule(fn *script.Fn, opts script.Table, where string) error {
	name := str(opts, "name")
	if name == "" {
		s.a.mu.Lock()
		s.a.n++
		name = fmt.Sprintf("%s#%d", where, s.a.n)
		s.a.mu.Unlock()
	}
	s.a.own(s.a.Host.Contribute(registry.StatusRule{Name: name, Source: where, Rule: func(sess map[string]any) (string, error) {
		out, err := s.a.VM.Call(fn, sess)
		if err != nil || len(out) == 0 {
			return "", err
		}
		v, _ := out[0].(string)
		return v, nil
	}}))
	return nil
}

//oriel:api name=regex cap=ui
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
	return re.MatchString(s)
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

//oriel:api name=language cap=ui
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
