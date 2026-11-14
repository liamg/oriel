package layout

import (
	"slices"
	"testing"
)

// Each of the host's parts is kept from every other, and from none of the packages it shares with them.
func TestHostPartsStayApart(t *testing.T) {
	for _, part := range hostParts {
		if !slices.ContainsFunc(Rules, func(r Rule) bool { return slices.Equal(r.From, []string{part}) }) {
			t.Errorf("%s has no rule", part)
		}
	}
	m := func(p string) string { return Module + "/" + p }
	pkgs := []Package{
		{ImportPath: m("host/jobs"), Deps: []string{m("host/hostcore"), m("agent/usage/stats")}},
		{ImportPath: m("host/github"), TestImports: []string{m("host/hosttest")}, Deps: []string{m("host/worktree")}},
		{ImportPath: m("host/hostcore"), Deps: []string{m("gitx")}},
	}
	var got []string
	for _, v := range Check(pkgs, []Rule{apart("host/jobs"), apart("host/github"), {From: []string{"host/hostcore"}, Never: hostParts}}) {
		got = append(got, v.Key())
	}
	if want := []string{"host/github -> host/worktree"}; !slices.Equal(got, want) {
		t.Errorf("violations %q, want %q", got, want)
	}
}

// A Built rule counts what a package builds with, not what its tests import.
func TestBuiltRulesLeaveTestsOut(t *testing.T) {
	m := func(p string) string { return Module + "/" + p }
	pkgs := []Package{
		{ImportPath: m("app/kit"), Deps: []string{m("core")}, XTestImports: []string{m("app/apptest")}},
		{ImportPath: m("app/apptest"), Deps: []string{m("host/agentrun")}},
	}
	rule := Rule{From: []string{"app/kit"}, Never: []string{"host/agentrun"}}
	if got := Check(pkgs, []Rule{rule}); len(got) != 1 {
		t.Errorf("a rule counting tests: %v", got)
	}
	rule.Built = true
	if got := Check(pkgs, []Rule{rule}); len(got) != 0 {
		t.Errorf("a Built rule: %v", got)
	}
}
