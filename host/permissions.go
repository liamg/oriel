package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
)

// Where a rule comes from, as the Permissions view names it.
const (
	SourceSession  = "this session"
	SourceProject  = "project settings"
	SourceSettings = "your settings"
	SourceAlways   = "always, on this machine"
	SourceReadOnly = "read-only, allowed by default"
	SourceDefault  = "built-in default"
	SourceOriel    = "Oriel"
)

// Permissions is what a sandboxed session's agent may do: each gateway tool and each network rule in force, with
// where its answer comes from, the connections it made or tried, and the destinations waiting on a card.
type Permissions struct {
	// Sandboxed is false for an agent on the host, which none of this applies to.
	Sandboxed bool
	// Filtered is false when the egress proxy is off: the sandbox has plain internet.
	Filtered bool
	Tools    []ToolPermission
	Hosts    []egress.Rule
	Conns    []egress.Event
	Waiting  []string
}

// ToolPermission is one gateway tool: what a call meets, its source, and the session's own rule for it.
type ToolPermission struct {
	gateway.ToolPolicy
	Source  string
	Session string
}

// Permissions reads session id's sandbox permissions. A session whose agent isn't running in a sandbox has none.
func (w *Host) Permissions(ctx context.Context, id core.SessionID) (_ Permissions, err error) {
	defer w.recover("Permissions", &err)
	b := w.boxOf(id)
	if b == nil {
		return Permissions{}, nil
	}
	return b.permissions(ctx), nil
}

// SetToolRule sets session id's own rule for a gateway tool: allow, ask or deny, or "" to drop it.
func (w *Host) SetToolRule(id core.SessionID, tool, action string) (err error) {
	defer w.recover("SetToolRule", &err)
	switch action {
	case "", gateway.Allow, gateway.Ask, gateway.Deny:
	default:
		return fmt.Errorf("%q isn't a tool rule: allow, ask, deny, or none", action)
	}
	if tool == "" {
		return errors.New("a tool rule needs a tool")
	}
	b := w.boxOf(id)
	if b == nil {
		return fmt.Errorf("session %s isn't sandboxed", id)
	}
	rules := slices.DeleteFunc(b.grants(), func(r gateway.Rule) bool { return r.Tool == tool })
	if action != "" {
		rules = append(rules, gateway.Rule{Tool: tool, Action: action})
	}
	return b.saveGrants(rules)
}

// SetNetRule allows or blocks host[:port] for the rest of session id ("allow", "block"), or drops the session's
// rule for it ("").
func (w *Host) SetNetRule(id core.SessionID, host string, port int, action string, private bool) (err error) {
	defer w.recover("SetNetRule", &err)
	b := w.boxOf(id)
	if b == nil {
		return fmt.Errorf("session %s isn't sandboxed", id)
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return errors.New("a network rule needs a host")
	}
	switch action {
	case "allow", "block":
		return b.egress.SetRule(host, port, action == "allow", private)
	case "":
		return b.egress.UpdateGrants(func(rules []egress.Rule) []egress.Rule {
			return slices.DeleteFunc(rules, func(r egress.Rule) bool { return r.Host == host && r.Port == port })
		})
	}
	return fmt.Errorf("%q isn't a network rule: allow, block, or none", action)
}

// boxOf is session id's sandbox while its agent runs in one.
func (w *Host) boxOf(id core.SessionID) *box {
	h, _ := w.sessionFor(id, false)
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	b, _ := h.box.(*box)
	return b
}

func (b *box) permissions(ctx context.Context) Permissions {
	p := Permissions{Sandboxed: true, Filtered: !b.cfg.NoEgressProxy, Waiting: b.egress.Waiting()}
	always := sandbox.LoadAlways()
	session := b.grants()
	for _, t := range b.gateway.Policy(ctx) {
		tp := ToolPermission{ToolPolicy: t, Source: SourceDefault}
		switch t.From {
		case gateway.FromSession:
			tp.Source = SourceSession
		case gateway.FromProject:
			tp.Source = SourceProject
		case gateway.FromGlobal:
			tp.Source = SourceSettings
			mine := func(r gateway.Rule) bool { return r.Tool == t.Rule }
			if slices.ContainsFunc(always.Rules, mine) && !slices.ContainsFunc(b.cfg.Rules, mine) {
				tp.Source = SourceAlways
			}
		case gateway.FromReadOnly:
			tp.Source = SourceReadOnly
		case gateway.FromOriel:
			tp.Source = SourceOriel
		}
		if i := slices.IndexFunc(session, func(r gateway.Rule) bool { return r.Tool == t.Name }); i >= 0 {
			tp.Session = session[i].Action
		}
		p.Tools = append(p.Tools, tp)
	}
	p.Hosts = b.egress.Rules()
	p.Conns = b.egress.Conns()
	return p
}
