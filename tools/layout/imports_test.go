package layout

import (
	"slices"
	"testing"
)

func TestImportRulesHold(t *testing.T) {
	r := root(t)
	rules, err := ImportRules(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Fatal("found no import rules in architecture.md, the rule parser is broken")
	}
	for _, m := range []string{"text", "vim", "core", "agent"} {
		if !slices.ContainsFunc(rules, func(rl ImportRule) bool {
			return rl.From == m && slices.Contains(rl.Never, "ui") && slices.Contains(rl.Never, "render")
		}) {
			t.Errorf("no rule keeps %s from importing ui and render", m)
		}
	}
	vs, err := Violations(r, rules)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestImportRulesCatchViolations(t *testing.T) {
	rules, err := ImportRules("testdata/importrule")
	if err != nil {
		t.Fatal(err)
	}
	want := []ImportRule{{From: "text", Never: []string{"ui", "render"}}, {From: "core", Never: []string{"ui", "render"}}}
	if !slices.EqualFunc(rules, want, func(a, b ImportRule) bool { return a.From == b.From && slices.Equal(a.Never, b.Never) }) {
		t.Fatalf("rules = %v, want %v", rules, want)
	}
	vs, err := Violations("testdata/importrule", rules)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	wantVs := []string{
		"core/ok_test.go imports github.com/liamg/oriel/ui, but core never imports ui",
		"core/sub/bad.go imports github.com/liamg/oriel/render/headless, but core never imports render",
	}
	if !slices.Equal(got, wantVs) {
		t.Errorf("violations:\n%q\nwant:\n%q", got, wantVs)
	}
}
