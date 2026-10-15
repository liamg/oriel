package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// FG-6: "before this ships" is a contribution point: gates by order, a kind naming its own gates, and one that
// panics or has nothing to say left out.
func TestShipGates(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.ShipGate{Name: "tests", Title: "Tests ran this session", Order: 30, Check: func(s string) registry.Gate {
		return registry.Gate{State: registry.GatePass, Note: "go test · " + s}
	}})
	r.Contribute(registry.ShipGate{Name: "plan", Title: "Plan approved", Order: 10, Check: func(string) registry.Gate {
		return registry.Gate{State: registry.GatePass}
	}})
	r.Contribute(registry.ShipGate{Name: "quiet", Order: 20, Check: func(string) registry.Gate { return registry.Gate{} }})
	r.Contribute(registry.ShipGate{Name: "boom", Order: 40, Check: func(string) registry.Gate { panic("boom") }})
	got := registry.ShipGates(r, "s1", "work")
	if len(got) != 2 || got[0].Name != "plan" || got[1].Name != "tests" || got[1].Note != "go test · s1" {
		t.Fatalf("gates %+v", got)
	}
	r.Contribute(registry.SessionKind{Name: "hotfix", Gates: []string{"tests"}})
	if got := registry.ShipGates(r, "s1", "hotfix"); len(got) != 1 || got[0].Name != "tests" {
		t.Errorf("a kind's own gates: %+v", got)
	}
}

// PL-9: a pull request's actions are contributions: each labels itself for the PR (nil leaves it out), sorted by
// Order, and runs its command with the PR's fields added to its args.
func TestPRActions(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.PRAction{Name: "slack", Order: 60, Command: "slack.ptal", Args: registry.Args{"channel": "#prs"},
		Label: func(pr registry.PullRequest) string {
			if pr.State != "open" {
				return ""
			}
			return "Slack"
		}})
	r.Contribute(registry.PRAction{Name: "copy", Order: 10, Command: "pr.copy", Label: func(registry.PullRequest) string { return "Copy link" }})
	r.Contribute(registry.PRAction{Name: "boom", Order: 20, Label: func(registry.PullRequest) string { panic("boom") }})
	pr := registry.PullRequest{Session: "s1", Project: "api", Repo: "acme/api", Number: 812, Title: "feat: x", URL: "https://github.com/acme/api/pull/812", State: "open"}
	got := registry.PRActions(r, pr)
	if len(got) != 2 || got[0].Label != "Copy link" || got[1].Label != "Slack" {
		t.Fatalf("actions %+v", got)
	}
	a := got[1].RunArgs()
	if a["channel"] != "#prs" || a["url"] != pr.URL || a["title"] != "feat: x" || a["number"] != 812 || a["repo"] != "acme/api" ||
		a["project"] != "api" || a["session"] != "s1" || a["state"] != "open" {
		t.Errorf("args %+v", a)
	}
	pr.State = "closed"
	if got := registry.PRActions(r, pr); len(got) != 1 {
		t.Errorf("a closed PR: %+v", got)
	}
}
