// Package gateway is the one door out of a sandbox: oriel's own host actions
// and the user's MCP servers, each call allowed, asked about or refused by
// policy.
package gateway

import (
	"path"
	"strings"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/sandbox"
)

// The actions a rule can take.
const (
	Allow = "allow"
	Ask   = "ask"
	Deny  = "deny"
)

// Rule is the sandbox settings' rule, shared with the settings page.
type Rule = sandbox.PolicyRule

// Defaults apply when nothing else matches: read-only looks are allowed,
// everything else is asked about.
var Defaults = []Rule{
	{Tool: "oriel__ci", Action: Allow},
	{Tool: "oriel__fetch", Action: Allow},
	{Tool: "oriel__repos", Action: Allow},
	{Tool: "oriel__gh_repo_variable", Action: Allow},
	{Tool: "oriel__gh_workflow_runs", Action: Allow},
	{Tool: "oriel__gh_compare", Action: Allow},
	{Tool: "oriel__gh_matching_refs", Action: Allow},
	{Tool: "*", Action: Ask},
}

// Levels are the rule sets a decision consults, most local first.
type Levels struct {
	Session []Rule
	Project []Rule
	Global  []Rule
	// ReadOnly allows a tool that declares readOnlyHint, when settings say so.
	ReadOnly bool
}

// The levels a decision can come from.
const (
	FromSession  = "session"
	FromProject  = "project"
	FromGlobal   = "global"
	FromReadOnly = "read-only"
	FromDefault  = "default"
	// FromOriel is a refusal no rule can lift: an agent posting to a GitHub review (gh.ReviewTool).
	FromOriel = "oriel"
)

// Why is a decision and what made it: the level, and the rule's pattern.
type Why struct {
	Action string `json:"action"`
	From   string `json:"from"`
	Rule   string `json:"rule,omitempty"`
}

// Decide picks the action for tool. Within a level the most specific rule
// wins, and the first level with a match decides — except that settings
// always keep a deny, and keep an ask more specific than the session's grant:
// "allow all of aws" for a session still asks about aws__delete_*. A tool that
// posts to a GitHub review is denied whatever the rules say: the agent drafts,
// the user forwards.
func Decide(tool string, readOnlyHint bool, l Levels) string {
	return Explain(tool, readOnlyHint, l).Action
}

// Explain is Decide, with the rule that decided.
func Explain(tool string, readOnlyHint bool, l Levels) Why {
	if gh.ReviewTool(tool) {
		return Why{Action: Deny, From: FromOriel}
	}
	var settings Why
	var pattern string
	found := false
	for _, lv := range []struct {
		from  string
		rules []Rule
	}{{FromProject, l.Project}, {FromGlobal, l.Global}} {
		if r, ok := Match(lv.rules, tool); ok {
			if r.Action == Deny {
				return Why{Deny, lv.from, r.Tool}
			}
			if !found {
				settings, pattern, found = Why{r.Action, lv.from, r.Tool}, r.Tool, true
			}
		}
	}
	if r, ok := Match(l.Session, tool); ok {
		if found && settings.Action == Ask && specificity(pattern) > specificity(r.Tool) {
			return settings
		}
		return Why{r.Action, FromSession, r.Tool}
	}
	if found {
		return settings
	}
	if l.ReadOnly && readOnlyHint {
		return Why{Action: Allow, From: FromReadOnly}
	}
	r, _ := Match(Defaults, tool)
	return Why{r.Action, FromDefault, r.Tool}
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
