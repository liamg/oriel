// Package gateway is the one door out of a sandbox: oriel's own host actions
// and the user's MCP servers, each call allowed, asked about or refused by
// policy.
package gateway

import (
	"path"
	"strings"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/policy"
)

// The actions a rule can take.
const (
	Allow = "allow"
	Ask   = "ask"
	Deny  = "deny"
)

// Rule is the sandbox settings' rule, shared with the settings page.
type Rule = sandbox.PolicyRule

// Defaults apply when nothing else matches: everything is asked about, except Oriel's own read-only tools.
var Defaults = []Rule{
	{Tool: "*", Action: Ask},
}

// Hint is what a tool says of itself: whether it only looks.
type Hint uint8

const (
	// Writes is a tool that may change something, or says nothing.
	Writes Hint = iota
	// SaysReadOnly is a server's tool that declares readOnlyHint: allowed only when settings trust the hint.
	SaysReadOnly
	// OwnReadOnly is one of Oriel's own tools that only looks: allowed unless a rule says otherwise.
	OwnReadOnly
)

// Levels are the rule sets a decision consults, most local first.
type Levels struct {
	Session []Rule
	Project []Rule
	Global  []Rule
	// ReadOnly allows a tool that declares readOnlyHint, when settings say so.
	ReadOnly bool
}

// Decide picks the action for tool. Within a level the most specific rule
// wins, and the first level with a match decides — except that settings
// always keep a deny, and keep an ask more specific than the session's grant:
// "allow all of aws" for a session still asks about aws__delete_*.
func Decide(tool string, hint Hint, l Levels) string {
	return Explain(tool, hint, l).Action
}

// Explain is Decide, with the rule that decided.
func Explain(tool string, hint Hint, l Levels) policy.Why {
	var settings policy.Why
	var pattern string
	found := false
	for _, lv := range []struct {
		from  string
		rules []Rule
	}{{policy.FromProject, l.Project}, {policy.FromGlobal, l.Global}} {
		if r, ok := Match(lv.rules, tool); ok {
			if r.Action == Deny {
				return policy.Why{Action: Deny, From: lv.from, Rule: r.Tool}
			}
			if !found {
				settings, pattern, found = policy.Why{Action: r.Action, From: lv.from, Rule: r.Tool}, r.Tool, true
			}
		}
	}
	if r, ok := Match(l.Session, tool); ok {
		if found && settings.Action == Ask && specificity(pattern) > specificity(r.Tool) {
			return settings
		}
		return policy.Why{Action: r.Action, From: policy.FromSession, Rule: r.Tool}
	}
	if found {
		return settings
	}
	if hint == OwnReadOnly || (l.ReadOnly && hint == SaysReadOnly) {
		return policy.Why{Action: Allow, From: policy.FromReadOnly}
	}
	r, _ := Match(Defaults, tool)
	return policy.Why{Action: r.Action, From: policy.FromDefault, Rule: r.Tool}
}

// Match returns the most specific rule matching tool.
func Match(rules []Rule, tool string) (Rule, bool) {
	best, bestScore, found := Rule{}, -1, false
	for _, r := range rules {
		if !valid(r.Action) {
			continue
		}
		if ok, _ := path.Match(r.Tool, tool); !ok {
			continue
		}
		if s := specificity(r.Tool); s > bestScore {
			best, bestScore, found = r, s, true
		}
	}
	return best, found
}

// specificity ranks an exact name above any glob, and a longer literal part
// above a shorter one: aws__delete_* beats aws__*.
func specificity(pattern string) int {
	if !strings.ContainsAny(pattern, "*?[") {
		return 10000 + len(pattern)
	}
	return len(strings.NewReplacer("*", "", "?", "", "[", "", "]", "").Replace(pattern))
}

func valid(action string) bool { return action == Allow || action == Ask || action == Deny }

// ServerOf is the server part of a gateway tool name.
func ServerOf(tool string) string {
	s, _, _ := strings.Cut(tool, "__")
	return s
}
