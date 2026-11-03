package uireg_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// Rail entries list top ones, then the bottom ones, each by order; a disposed entry goes.
func TestRailEntries(t *testing.T) {
	r := uireg.New()
	r.Contribute(uireg.RailEntry{Name: "settings", Order: 1, Bottom: true})
	r.Contribute(uireg.RailEntry{Name: "search", Order: 20})
	d := r.Contribute(uireg.RailEntry{Name: "explorer", Order: 10})
	got := names(uireg.RailEntries(r))
	if want := "explorer search settings"; got != want {
		t.Errorf("rail = %q, want %q", got, want)
	}
	d.Dispose()
	if got, want := names(uireg.RailEntries(r)), "search settings"; got != want {
		t.Errorf("after dispose rail = %q, want %q", got, want)
	}
}

func names(es []uireg.RailEntry) string {
	s := ""
	for i, e := range es {
		if i > 0 {
			s += " "
		}
		s += e.Name
	}
	return s
}

// Rail entries are a project's panels, the session's or every session's; a project entry runs its command with the project.
func TestRailEntryScopes(t *testing.T) {
	r := uireg.New()
	r.Contribute(uireg.RailEntry{Name: "explorer", Scope: uireg.ScopeProjectEntry, Order: 10, Command: "view.toggle", Args: registry.Args{"id": "explorer"}})
	r.Contribute(uireg.RailEntry{Name: "requests", Order: 40})
	r.Contribute(uireg.RailEntry{Name: "tests", Scope: uireg.ScopeProjectEntry, Order: 60})
	r.Contribute(uireg.RailEntry{Name: "settings", Scope: uireg.ScopeGlobalEntry, Order: 10, Bottom: true})
	r.Contribute(uireg.RailEntry{Name: "stats", Scope: uireg.ScopeGlobalEntry, Order: 10})
	if got, want := names(uireg.ProjectEntries(r)), "explorer tests"; got != want {
		t.Errorf("project entries %q, want %q", got, want)
	}
	if got, want := names(uireg.SessionEntries(r)), "requests"; got != want {
		t.Errorf("session entries %q, want %q", got, want)
	}
	// Global entries (Stats, Usage, Settings) are listed last, Settings at the bottom.
	if got, want := names(uireg.GlobalEntries(r)), "stats settings"; got != want {
		t.Errorf("global entries %q, want %q", got, want)
	}
	e := uireg.ProjectEntries(r)[0]
	if a := e.ArgsFor("api"); a.String("project") != "api" || a.String("id") != "explorer" {
		t.Errorf("args for api: %v", a)
	}
	if e.Args["project"] != nil {
		t.Error("ArgsFor changed the entry's own args")
	}
	if a := uireg.SessionEntries(r)[0].ArgsFor("api"); a["project"] != nil {
		t.Errorf("a session entry's args name a project: %v", a)
	}
}

// What plugins add to a project's header line, by order.
func TestProjectStateParts(t *testing.T) {
	r := uireg.New()
	r.Contribute(uireg.ProjectState{Name: "ci", Order: 20, State: func(p, dir string) []uireg.StatePart {
		return []uireg.StatePart{{Text: "CI failing", Style: "error", Dot: true}}
	}})
	r.Contribute(uireg.ProjectState{Name: "pr", Order: 10, State: func(p, dir string) []uireg.StatePart {
		return []uireg.StatePart{{Text: "#790 " + p}}
	}})
	r.Contribute(uireg.ProjectState{Name: "boom", Order: 30, State: func(string, string) []uireg.StatePart { panic("boom") }})
	got := uireg.ProjectStateParts(r, "api", "/w/api")
	if len(got) != 2 || got[0].Text != "#790 api" || got[1].Text != "CI failing" || !got[1].Dot {
		t.Errorf("parts %+v", got)
	}
}

// The place showing is the one whose Shown says so; two at once (one opening over the other) is no answer yet, and
// one that panics isn't showing.
func TestShownPlace(t *testing.T) {
	r := uireg.New()
	stats, jobs := false, false
	r.Contribute(uireg.Place{Name: "stats", Shown: func() bool { return stats }})
	r.Contribute(uireg.Place{Name: "jobs", Shown: func() bool { return jobs }})
	r.Contribute(uireg.Place{Name: "broken", Shown: func() bool { panic("boom") }})
	check := func(want string, wantOK bool) {
		t.Helper()
		if got, ok := uireg.ShownPlace(r); got != want || ok != wantOK {
			t.Errorf("ShownPlace = %q, %v; want %q, %v", got, ok, want, wantOK)
		}
	}
	check("", true)
	stats = true
	check("stats", true)
	jobs = true
	check("", false)
	stats = false
	check("jobs", true)
	if p, ok := uireg.PlaceByName(r, "jobs"); !ok || p.Name != "jobs" {
		t.Errorf("PlaceByName(jobs) = %+v, %v", p, ok)
	}
}

// A rail entry scoped to kinds or a capability is listed only on those sessions.
func TestRailEntryKinds(t *testing.T) {
	r := kindsReg(nil)
	review := uireg.RailEntry{Name: "review", Kinds: []core.Kind{"review"}}
	pr := uireg.RailEntry{Name: "pull-request", When: core.Ships}
	if review.For(r, "work") || !review.For(r, "review") {
		t.Error("Kinds")
	}
	if !pr.For(r, "work") || pr.For(r, "plan") {
		t.Error("When")
	}
	if !(uireg.RailEntry{Name: "explorer"}).For(r, "anything") {
		t.Error("an entry with neither is listed everywhere")
	}
	if review.For(r, "") || !pr.For(r, "") {
		t.Error("with no session, only the entries with no Kinds are listed")
	}
}

// A slot's indicators come in Order; the generation moves as any contribution comes or goes, so a cache of
// what they built knows to start again.
func TestIndicators(t *testing.T) {
	r := uireg.New()
	g0 := r.ContributionsGen()
	r.Contribute(uireg.Indicator{Name: "b", Slot: uireg.SlotHUD, Order: 20})
	d := r.Contribute(uireg.Indicator{Name: "a", Slot: uireg.SlotHUD, Order: 30})
	r.Contribute(uireg.Indicator{Name: "c", Slot: uireg.SlotHUD, Order: 10})
	r.Contribute(uireg.Indicator{Name: "x", Slot: uireg.SlotTitleBar})
	var names []string
	for _, i := range uireg.Indicators(r, uireg.SlotHUD) {
		names = append(names, i.Name)
	}
	if strings.Join(names, " ") != "c b a" {
		t.Errorf("the hud slot's indicators: %v", names)
	}
	g1 := r.ContributionsGen()
	if g1 == g0 {
		t.Error("contributing didn't move the generation")
	}
	d.Dispose()
	if r.ContributionsGen() == g1 || len(uireg.Indicators(r, uireg.SlotHUD)) != 2 {
		t.Error("disposing one didn't take it out, or move the generation")
	}
}
