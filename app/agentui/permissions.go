package agentui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// permsEvery is how often an open Permissions view reads the session again.
const permsEvery = 2 * time.Second

// permsMaxConns is how many connections the list shows.
const permsMaxConns = 300

var (
	tPermAsk  = theme.Declare("permissions.ask", "status.permission")
	tPermDeny = theme.Declare("permissions.deny", "error")
)

// Permissions is what the session in front's sandboxed agent may do, and the commands that change it.
type Permissions struct {
	kit.Disposer
	h  plugin.Host
	by map[core.SessionID]*permsState
}

// permsState is one session's view: its read, and what's picked and typed.
type permsState struct {
	load    *plugin.Load[host.Permissions]
	read    time.Time
	pane    int // 0 rules, 1 connections
	toolQ   string
	hostQ   string
	connQ   string
	blocked bool
	flat    bool
	builtin bool // the built-in list unfolded
}

func (x *Permissions) Manifest() plugin.Manifest { return plugin.Manifest{ID: "permissions"} }

func (x *Permissions) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.by = map[core.SessionID]*permsState{}
	open := registry.Args{"id": kit.PermissionsView, "place": "dock", "edge": "bottom", "size": 420, "title": "Permissions"}
	session := func(a registry.Args) core.SessionID {
		if id := a.String("id"); id != "" {
			return core.SessionID(id)
		}
		return h.State().Focus
	}
	for _, c := range []registry.Command{
		{ID: "agent.permissions", Title: "Show what the sandboxed agent of the session in front may do, and its connections: the Workspace page's Permissions",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "workspace.open", registry.Args{"tab": "permissions"})
			}},
		{ID: "agent.permissions.dock", Title: "Show the sandboxed agent's permissions and connections docked at the bottom, or hide them",
			Run: func(ctx registry.Ctx, _ registry.Args) error { return h.Run(ctx, "view.toggle", open) }},
		{ID: "permissions.tool", Title: "Set {id}'s own rule for a gateway {tool}: {action} allow, ask, deny, or none to drop it",
			Run: func(_ registry.Ctx, a registry.Args) error {
				id := session(a)
				return x.change(id, func(w host.SandboxCalls) error {
					return w.SetToolRule(id, a.String("tool"), noneAsEmpty(a.String("action")))
				})
			}},
		{ID: "permissions.net", Title: "Allow or block {host}[:{port}] for the rest of {id}: {action} allow, block, or none to drop the session's rule",
			Run: func(_ registry.Ctx, a registry.Args) error {
				id := session(a)
				h, port, err := hostPort(a.String("host"), a.Int("port"))
				if err != nil {
					return err
				}
				return x.change(id, func(w host.SandboxCalls) error {
					return w.SetNetRule(id, h, port, noneAsEmpty(a.String("action")), a.Bool("private"))
				})
			}},
	} {
		x.Add(h.Command(c))
	}
	for _, c := range x.rulesCommands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Contribute(x.rulesSettings()))
	x.Add(h.View(kit.PermissionsView, permsView{x}))
	return nil
}

func noneAsEmpty(s string) string {
	if s == "none" {
		return ""
	}
	return s
}

// hostPort reads "host" or "host:port", a port given apart winning.
func hostPort(s string, port int) (string, int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if h, p, ok := strings.Cut(s, ":"); ok {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, fmt.Errorf("%q isn't a port", p)
		}
		s = h
		if port == 0 {
			port = n
		}
	}
	if s == "" || strings.ContainsAny(s, " /") {
		return "", 0, errors.New("a network rule needs a host: api.example.com, *.example.com, or host:port")
	}
	return s, port, nil
}

func (x *Permissions) shown() bool {
	for _, v := range x.h.Layout().Visible() {
		if v.ID == kit.PermissionsView {
			return true
		}
	}
	return false
}

// state is session id's view, its read made on first use.
func (x *Permissions) state(id core.SessionID) *permsState {
	st := x.by[id]
	if st == nil {
		st = &permsState{}
		st.load = plugin.NewLoad(x.h, func(ctx context.Context, _ func(string, float64)) (host.Permissions, error) {
			w, err := kit.SandboxCalls(x.h)
			if err != nil {
				return host.Permissions{}, err
			}
			return w.Permissions(ctx, id)
		}, plugin.LoadOpts{Timeout: 20 * time.Second})
		x.by[id] = st
	}
	return st
}

// change runs a change on the host and reads the session again.
func (x *Permissions) change(id core.SessionID, fn func(host.SandboxCalls) error) error {
	w, err := kit.SandboxCalls(x.h)
	if err != nil {
		return err
	}
	if err := fn(w); err != nil {
		return err
	}
	x.h.Do(func() { x.state(id).load.Reload() })
	return nil
}

func (x *Permissions) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Notify(core.Notice{ID: "permissions", Severity: core.SevError, Source: "permissions", Text: err.Error(), Time: time.Now()})
	}
}

type permsView struct{ x *Permissions }

func (v permsView) Title(string) string { return "Permissions" }
func (v permsView) EdgeToEdge()         {}
func (v permsView) Build(uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "permissions", Fn: v.x.build}
}

func (x *Permissions) build(c *ui.Ctx) {
	th := c.Theme()
	se, ok := x.h.State().Session(x.h.State().Focus)
	c.Column("perms", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tConsoleBg), Role: ui.RoleRegion, Label: "Permissions", Help: "agent/permissions"},
		func(c *ui.Ctx) {
			if !ok {
				kit.PaneNote(c, "none", "No session in front: what a sandboxed agent may do shows here.")
				return
			}
			s := se.Snapshot()
			st := x.state(s.ID)
			now := c.Now()
			if st.load.State().Phase != widget.Pending && now.Sub(st.read) >= permsEvery {
				st.read = now
				st.load.Reload()
			}
			c.WakeAt(st.read.Add(permsEvery))
			x.header(c, s, st)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			p, _ := st.load.Value()
			widget.Async(c, "load", st.load.State(), widget.AsyncOpts{Label: "Reading the session's rules", Retry: st.load.Reload, Height: ui.Flex(1),
				Help: "agent/permissions"}, func(c *ui.Ctx) {
				c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(16, 12), Gap: 8, ScrollY: true, Selectable: true}, func(c *ui.Ctx) {
					switch {
					case !p.Sandboxed:
						kit.PaneNote(c, "host", "This session's agent isn't running in a sandbox: it has your access, and none of these rules apply.")
					case st.pane == 0:
						x.waiting(c, s)
						x.hosts(c, s, st, p)
						x.tools(c, s, st, p)
					default:
						x.conns(c, s, st, p, now)
					}
				})
			})
		})
}

func (x *Permissions) header(c *ui.Ctx, se core.SessionState, st *permsState) {
	th := c.Theme()
	p, _ := st.load.Value()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(16, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("where", kit.Label(se), kit.MonoFont(11, th.C(theme.FgFaint)))
		blocked := 0
		for _, e := range p.Conns {
			if !e.Allowed {
				blocked++
			}
		}
		badges := []string{"", ""}
		if blocked > 0 {
			badges[1] = fmt.Sprintf("%d blocked", blocked)
		}
		widget.Segmented(c, "pane", []string{"RULES", "CONNECTIONS"}, &st.pane, widget.SegmentedOpts{Label: "Show", Badges: badges,
			Help: "agent/permissions"})
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if p.Sandboxed && !p.Filtered {
			widget.Chip(c, "open", "network unfiltered", th.C(theme.Warn), "agent/permissions#network")
		}
	})
}

// waiting are the network and gateway asks open now, answered here as on their cards.
func (x *Permissions) waiting(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	var asks []core.Ask
	for _, a := range se.Asks {
		if a.Kind == core.AskNetwork || a.Kind == core.AskGateway {
			asks = append(asks, a)
		}
	}
	if len(asks) == 0 {
		return
	}
	kit.KickerRow(c, "waiting", fmt.Sprintf("WAITING ON YOU · %d", len(asks)))
	for _, a := range asks {
		c.Row(a.ID, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			c.Text("s", a.Summary, kit.UIFont(12.5, th.C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			for _, d := range []struct{ key, label string }{{"allow", "Allow once"}, {"always", "Allow for this session"}, {"deny", "Deny"}} {
				if widget.Button(c, d.key, d.label, widget.ButtonOpts{Primary: d.key == "allow", Ghost: d.key == "deny", Help: "agent/asks"}) {
					x.run("ask.answer", registry.Args{"id": string(se.ID), "ask": a.ID, "decision": d.key})
				}
			}
		})
	}
}

func actionTone(action string) theme.TokenID {
	switch action {
	case gateway.Allow:
		return kit.TPermAllow
	case gateway.Deny:
		return tPermDeny
	}
	return tPermAsk
}

var actionWord = map[string]string{gateway.Allow: "allowed", gateway.Ask: "asks you", gateway.Deny: "denied"}

// tools are the gateway's tools by server: what a call meets, where that comes from, and the session's own say.
func (x *Permissions) tools(c *ui.Ctx, se core.SessionState, st *permsState, p host.Permissions) {
	th := c.Theme()
	kit.KickerRow(c, "toolscap", "TOOLS")
	widget.Input(c, "toolq", &st.toolQ, widget.InputOpts{Label: "Filter tools", Placeholder: "Filter tools", Width: ui.Px(280), Help: "agent/permissions#tools"})
	q := strings.ToLower(strings.TrimSpace(st.toolQ))
	server := ""
	shown := 0
	for _, t := range p.Tools {
		if q != "" && !strings.Contains(strings.ToLower(t.Name), q) {
			continue
		}
		shown++
		if t.Server != server {
			server = t.Server
			c.Row("srv:"+server, ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 4}}, func(c *ui.Ctx) {
				c.Text("t", cmp.Or(server, "Oriel"), kit.MonoFont(10.5, th.C(theme.FgMuted)))
			})
		}
		c.Row("tool:"+t.Name, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true, Role: ui.RoleText,
			Label: t.Name + " " + actionWord[t.Action] + ", " + t.Source}, func(c *ui.Ctx) {
			c.Row("name", ui.Style{W: ui.Px(260), Clip: true}, func(c *ui.Ctx) { c.Text("t", t.Name, kit.MonoFont(11.5, th.C(theme.Fg))) })
			widget.Chip(c, "act", cmp.Or(actionWord[t.Action], t.Action), th.C(actionTone(t.Action)), "agent/permissions#tools")
			why := t.Source
			if t.Fixed {
				why = "always asks: the call is a question"
			} else if t.Rule != "" && t.Rule != "*" && t.Rule != t.Name {
				why += " · " + t.Rule
			}
			c.Row("why", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", why, kit.MonoFont(10.5, th.C(theme.FgFaint))) })
			locked := t.Fixed || t.Action == gateway.Deny && t.Source != host.SourceSession
			if locked {
				return
			}
			opts := []string{"no session rule", gateway.Allow, gateway.Ask, gateway.Deny}
			if v, ok := widget.Select(c, "sel", cmp.Or(t.Session, opts[0]), opts, widget.SelectOpts{Label: "This session's rule for " + t.Name,
				Width: 160, Help: "agent/permissions#tools"}); ok {
				if v == opts[0] {
					v = "none"
				}
				x.run("permissions.tool", registry.Args{"id": string(se.ID), "tool": t.Name, "action": v})
			}
		})
	}
	if shown == 0 {
		c.Text("notools", map[bool]string{true: "Nothing matches.", false: "No tools."}[q != ""], kit.UIFont(12, th.C(theme.FgFaint)))
	}
}

// hosts are the network rules in force, most local first, and a way to add the session's own.
func (x *Permissions) hosts(c *ui.Ctx, se core.SessionState, st *permsState, p host.Permissions) {
	th := c.Theme()
	kit.KickerRow(c, "netcap", "NETWORK")
	if !p.Filtered {
		widget.Wrap(c, "open", "Network filtering is off (sandbox.egress_proxy, or the session's network): every connection is allowed and none of these rules apply.", 560,
			kit.UIFont(12, th.C(theme.Warn)))
	}
	c.Row("add", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		r := widget.Input(c, "host", &st.hostQ, widget.InputOpts{Label: "A host for this session", Placeholder: "host or host:port, *.example.com",
			Width: ui.Px(300), Help: "agent/permissions#network"})
		add := func(action string) {
			x.run("permissions.net", registry.Args{"id": string(se.ID), "host": st.hostQ, "action": action})
			st.hostQ = ""
		}
		typed := strings.TrimSpace(st.hostQ) != ""
		if widget.Button(c, "allow", "Allow for session", widget.ButtonOpts{Disabled: !typed, Help: "agent/permissions#network"}) || r.Submitted && typed {
			add("allow")
		}
		if widget.Button(c, "block", "Block for session", widget.ButtonOpts{Disabled: !typed, Help: "agent/permissions#network"}) {
			add("block")
		}
	})
	builtin := 0
	for _, r := range p.Hosts {
		if r.Source == builtinSource {
			builtin++
		}
	}
	row := func(i int, r egress.Rule) {
		dest := r.Host
		if r.Port != 0 {
			dest += ":" + strconv.Itoa(r.Port)
		} else {
			dest += " · any port"
		}
		c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			c.Row("dest", ui.Style{W: ui.Px(260), Clip: true}, func(c *ui.Ctx) { c.Text("t", dest, kit.MonoFont(11.5, th.C(theme.Fg))) })
			word, tone := "blocked", tPermDeny
			if r.Allow {
				word, tone = "allowed", kit.TPermAllow
			}
			widget.Chip(c, "act", word, th.C(tone), "agent/permissions#network")
			why := r.Source
			if r.Allow && r.Private {
				why += " · private addresses too"
			}
			c.Row("why", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", why, kit.MonoFont(10.5, th.C(theme.FgFaint))) })
			if r.Source == host.SourceSession && widget.Button(c, "rm", "Remove", widget.ButtonOpts{Ghost: true, Help: "agent/permissions#network"}) {
				x.run("permissions.net", registry.Args{"id": string(se.ID), "host": r.Host, "port": r.Port, "action": "none"})
			}
		})
	}
	for i, r := range p.Hosts {
		if r.Source != builtinSource {
			row(i, r)
		}
	}
	if builtin > 0 {
		c.Row("builtin", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("dest", ui.Style{W: ui.Px(260)}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("%d hosts the agent needs", builtin), kit.MonoFont(11.5, th.C(theme.FgMuted)))
			})
			widget.Chip(c, "act", "allowed", th.C(kit.TPermAllow), "agent/permissions#network")
			c.Row("why", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { c.Text("t", builtinSource, kit.MonoFont(10.5, th.C(theme.FgFaint))) })
			label := "Show"
			if st.builtin {
				label = "Hide"
			}
			if widget.Button(c, "fold", label, widget.ButtonOpts{Ghost: true, Help: "agent/permissions#network"}) {
				st.builtin = !st.builtin
			}
		})
	}
	if st.builtin {
		for i, r := range p.Hosts {
			if r.Source == builtinSource {
				row(i, r)
			}
		}
	}
	c.Row("else", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Row("dest", ui.Style{W: ui.Px(260)}, func(c *ui.Ctx) { c.Text("t", "anything else", kit.MonoFont(11.5, th.C(theme.FgMuted))) })
		widget.Chip(c, "act", "asks you", th.C(tPermAsk), "agent/permissions#network")
	})
}

// builtinSource is the preset allowlist's source, folded into one row until asked for.
const builtinSource = "built-in list"

// connGroup is the connections to one destination.
type connGroup struct {
	host       string
	port       int
	proto      string
	n, blocked int
	up, down   int64
	last       time.Time
	allowed    bool
	why        string
}

func connWhy(e egress.Event) string {
	if e.Reason != "" {
		return e.Reason
	}
	return e.Err
}

// conns is every connection the agent made or tried, newest first, grouped by destination unless asked not to.
func (x *Permissions) conns(c *ui.Ctx, se core.SessionState, st *permsState, p host.Permissions, now time.Time) {
	th := c.Theme()
	c.Row("bar", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		widget.Input(c, "q", &st.connQ, widget.InputOpts{Label: "Filter connections", Placeholder: "Filter by host, port or protocol", Width: ui.Px(300),
			Help: "agent/permissions#connections"})
		st.blocked = widget.ToggleChip(c, "blocked", "BLOCKED ONLY", st.blocked, widget.ToggleChipOpts{Help: "agent/permissions#connections"})
		st.flat = !widget.ToggleChip(c, "group", "BY DESTINATION", !st.flat, widget.ToggleChipOpts{Help: "agent/permissions#connections"})
	})
	q := strings.ToLower(strings.TrimSpace(st.connQ))
	rows := slices.DeleteFunc(slices.Clone(p.Conns), func(e egress.Event) bool {
		return st.blocked && e.Allowed || q != "" && !strings.Contains(strings.ToLower(fmt.Sprintf("%s:%d %s %s", e.Host, e.Port, e.Proto, e.Path)), q)
	})
	if len(p.Waiting) > 0 {
		widget.Wrap(c, "waiting", "Held for your answer: "+strings.Join(p.Waiting, ", "), 560, kit.UIFont(12, th.C(theme.Warn)))
	}
	if len(rows) == 0 {
		c.Text("none", map[bool]string{true: "Nothing matches.", false: "No connections yet."}[len(p.Conns) > 0], kit.UIFont(12, th.C(theme.FgFaint)))
		return
	}
	act := func(c *ui.Ctx, host string, port int, allowed bool) {
		label, action := "Block", "block"
		if !allowed {
			label, action = "Allow", "allow"
		}
		if widget.Button(c, "act", label, widget.ButtonOpts{Ghost: true, Help: "agent/permissions#connections"}) {
			x.run("permissions.net", registry.Args{"id": string(se.ID), "host": host, "port": port, "action": action})
		}
	}
	verdict := func(c *ui.Ctx, allowed bool) {
		if allowed {
			widget.Chip(c, "v", "allowed", th.C(kit.TPermAllow), "agent/permissions#connections")
		} else {
			widget.Chip(c, "v", "blocked", th.C(tPermDeny), "agent/permissions#connections")
		}
	}
	if st.flat {
		for i, e := range rows[:min(len(rows), permsMaxConns)] {
			c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
				c.Row("when", ui.Style{W: ui.Px(64)}, func(c *ui.Ctx) { c.Text("t", e.At.Local().Format("15:04:05"), kit.MonoFont(10.5, th.C(theme.FgFaint))) })
				dest := fmt.Sprintf("%s:%d", e.Host, e.Port)
				if e.Proto == "http" {
					dest += " " + e.Method + " " + cmp.Or(e.Path, "/")
				}
				c.Row("dest", ui.Style{W: ui.Px(300), Clip: true}, func(c *ui.Ctx) { c.Text("t", dest, kit.MonoFont(11.5, th.C(theme.Fg))) })
				verdict(c, e.Allowed)
				c.Row("why", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", connWhy(e), kit.MonoFont(10.5, th.C(theme.FgFaint))) })
				if e.Allowed {
					c.Text("bytes", "↑"+kit.Bytes(e.BytesUp)+" ↓"+kit.Bytes(e.BytesDown), kit.MonoFont(10.5, th.C(theme.FgMuted)))
				}
				act(c, e.Host, e.Port, e.Allowed)
			})
		}
		if len(rows) > permsMaxConns {
			c.Text("more", fmt.Sprintf("The newest %d of %d.", permsMaxConns, len(rows)), kit.UIFont(11.5, th.C(theme.FgFaint)))
		}
		return
	}
	for i, g := range groupConns(rows) {
		c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			c.Row("dest", ui.Style{W: ui.Px(300), Clip: true}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("%s:%d", g.host, g.port), kit.MonoFont(11.5, th.C(theme.Fg)))
			})
			c.Text("proto", cmp.Or(g.proto, "tcp"), kit.MonoFont(10.5, th.C(theme.FgMuted)))
			verdict(c, g.allowed)
			why := g.why
			if g.blocked > 0 && g.blocked < g.n {
				why = fmt.Sprintf("%d of %d blocked", g.blocked, g.n)
			}
			c.Row("why", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", why, kit.MonoFont(10.5, th.C(theme.FgFaint))) })
			c.Text("n", kit.Counted(g.n, "connection"), kit.MonoFont(10.5, th.C(theme.FgMuted)))
			c.Text("bytes", "↑"+kit.Bytes(g.up)+" ↓"+kit.Bytes(g.down), kit.MonoFont(10.5, th.C(theme.FgMuted)))
			widget.Ago(c, "last", g.last, widget.AgoOpts{Text: kit.MonoFont(10.5, th.C(theme.FgFaint))})
			act(c, g.host, g.port, g.allowed)
		})
	}
}

// groupConns folds connections, newest first, into one group per destination, busiest first; a group reads as its
// newest connection.
func groupConns(conns []egress.Event) []connGroup {
	var out []connGroup
	at := map[string]int{}
	for _, e := range conns {
		k := fmt.Sprintf("%s:%d", e.Host, e.Port)
		i, ok := at[k]
		if !ok {
			i = len(out)
			at[k] = i
			out = append(out, connGroup{host: e.Host, port: e.Port, proto: e.Proto, last: e.At, allowed: e.Allowed, why: connWhy(e)})
		}
		g := &out[i]
		g.n++
		if !e.Allowed {
			g.blocked++
		}
		g.up += e.BytesUp
		g.down += e.BytesDown
	}
	slices.SortStableFunc(out, func(a, b connGroup) int { return b.n - a.n })
	return out
}
