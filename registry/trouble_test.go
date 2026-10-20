package registry_test

import (
	"reflect"
	"testing"

	"github.com/liamg/oriel/registry"
)

// TR-6: ide.md, "Wherever Oriel offers "ask the agent to fix" … it also offers **ask to explain**": a menu item
// that runs agent.fix, a plugin's too, gets an explain twin right after it with the same args, and a Trouble that
// says there's nothing hides both.
func TestFixMenuItemsGetExplainTwins(t *testing.T) {
	r := registry.New()
	tr := registry.Trouble{Kind: "command", Subject: "`make` exited 2", Context: "$ make\nboom", Fix: "Fix make."}
	r.Contribute(registry.MenuItem{Name: "a.copy", Label: "Copy", Group: "edit", Order: 1})
	r.Contribute(registry.MenuItem{Name: "plugin.fix", Label: "Ask the agent to fix the build", Group: "agent", Order: 10,
		Command: registry.FixCommand, Args: tr.Args()})
	failed := false
	r.Contribute(registry.MenuItem{Name: "term.fix", Label: "Ask the agent to fix the failed command", Group: "agent", Order: 20,
		Command: registry.FixCommand, Trouble: func(registry.MenuCtx) (registry.Trouble, bool) { return tr, failed }})
	r.Contribute(registry.MenuItem{Name: "z.ask", Label: "Ask about this…", Group: "agent", Order: 30, Command: "thread.new"})

	names := func() []string {
		var out []string
		for _, m := range registry.MenuItems(r, registry.MenuCtx{View: "editor"}) {
			out = append(out, m.Name)
		}
		return out
	}
	if got, want := names(), []string{"a.copy", "plugin.fix", "plugin.fix.explain", "z.ask"}; !reflect.DeepEqual(got, want) {
		t.Errorf("items %v, want %v", got, want)
	}
	failed = true
	items := registry.MenuItems(r, registry.MenuCtx{View: "editor"})
	if got, want := names(), []string{"a.copy", "plugin.fix", "plugin.fix.explain", "term.fix", "term.fix.explain", "z.ask"}; !reflect.DeepEqual(got, want) {
		t.Errorf("items %v, want %v", got, want)
	}
	for _, m := range items {
		if m.Name != "term.fix.explain" {
			continue
		}
		if m.Command != registry.ExplainCommand || m.Label != registry.ExplainLabel || registry.TroubleOf(m.Args) != tr {
			t.Errorf("the twin: %+v", m)
		}
	}
}

func TestTroubleArgsRoundTrip(t *testing.T) {
	tr := registry.Trouble{Kind: "diagnostic", Subject: "s", Context: "c", File: "/a.go", Line: 3, EndLine: 4, Fix: "f"}
	if got := registry.TroubleOf(tr.Args()); got != tr {
		t.Errorf("%+v came back as %+v", tr, got)
	}
	if got := (registry.Trouble{Subject: "it broke"}).FixText(); got != "Fix this: it broke" {
		t.Errorf("FixText %q", got)
	}
}

func TestMenuWhenSubject(t *testing.T) {
	it := registry.MenuItem{When: registry.MenuWhen{Subject: "session:"}}
	if !it.Matches(registry.MenuCtx{Subject: "session:fix-login"}) {
		t.Error("an item for sessions doesn't match a session")
	}
	if it.Matches(registry.MenuCtx{View: "editor"}) || it.Matches(registry.MenuCtx{Subject: "pr:12"}) {
		t.Error("an item for sessions matches what isn't one")
	}
}
