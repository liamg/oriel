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
		{ImportPath: m("host/jobs"), Deps: []string{m("host/hostcore"), m("host/stats")}},
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
