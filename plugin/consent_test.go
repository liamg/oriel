package plugin

import (
	"slices"
	"testing"
)

// A manifest declaring most kinds of capability, a gate among them.
const jiraManifest = `id = "acme/jira"
version = "0.4.2"
runs = "both"

[permissions]
net     = { hosts = ["acme.atlassian.net"], why = "Fetch and update Jira tickets" }
secrets = { JIRA_TOKEN = "acme.atlassian.net", why = "Authenticate to your Jira" }
"agent.tools" = { tools = ["jira_ticket", "jira_comment"], why = "Let the agent read the ticket it's working on" }
"editor.read" = { why = "Spot ticket keys like ENG-2231 in branches and commits" }
"agent.prompt" = true
gate = true
ui = true
`

func manifest(t *testing.T, src string) Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// "At install, Oriel lists every capability the plugin declares beyond the always-allowed ones, each
// with Oriel's own explanation and risk level, and the plugin's reason if the manifest gives one."
func TestConsentListsEveryShownCapability(t *testing.T) {
	c := Ask(manifest(t, jiraManifest), nil)
	if c.Update() || c.Silent() {
		t.Fatalf("an install with capabilities: Update %v, Silent %v", c.Update(), c.Silent())
	}
	var got []string
	for _, r := range c.Rows {
		got = append(got, r.Group+"/"+string(r.Cap))
		if r.Explain == "" {
			t.Errorf("%s has no explanation", r.Cap)
		}
	}
	want := []string{"Network & secrets/net", "Network & secrets/secrets", "Your code & agents/editor.read",
		"Your code & agents/agent.prompt", "Your code & agents/agent.tools", "Critical/gate"}
	if !slices.Equal(got, want) {
		t.Errorf("rows %v, want %v (ui is always allowed, not listed)", got, want)
	}
	row := func(c Consent, cap Capability) ConsentRow {
		for _, r := range c.Rows {
			if r.Cap == cap {
				return r
			}
		}
		t.Fatalf("no %s row", cap)
		return ConsentRow{}
	}
	if r := row(c, CapNet); r.Why != "Fetch and update Jira tickets" || r.Explain != "HTTPS to acme.atlassian.net only" || r.Info.Risk != RiskMedium {
		t.Errorf("net row %+v", r)
	}
	if r := row(c, CapSecrets); r.Explain != "JIRA_TOKEN → bound to acme.atlassian.net; the plugin never sees the value" {
		t.Errorf("secrets explained as %q", r.Explain)
	}
	if r := row(c, CapGate); r.Info.Risk != RiskCritical || !r.Info.Warned {
		t.Errorf("gate row %+v", r.Info)
	}
	if n := c.Count(RiskCritical); n != 1 {
		t.Errorf("%d critical, want 1", n)
	}
}

// "A plugin that only needs ui and theme installs with no prompt."
func TestConsentSilentForAlwaysAllowed(t *testing.T) {
	for _, src := range []string{
		"id = \"me/theme\"\n",
		"id = \"me/theme\"\n[permissions]\nui = true\ntheme = true\nnotify = true\n",
	} {
		if c := Ask(manifest(t, src), nil); !c.Silent() || len(c.Rows) != 0 {
			t.Errorf("%q: Silent %v, rows %v", src, c.Silent(), c.Rows)
		}
	}
}

// "An update that adds capabilities is the same choice again: accept the update with its new list, or
// stay on the current version. Removing capabilities applies silently."
func TestConsentUpdateDiff(t *testing.T) {
	old := manifest(t, `id = "acme/jira"
version = "0.4.1"
[permissions]
net = ["acme.atlassian.net"]
"editor.read" = true
exec = ["jira-cli"]
`)
	same := manifest(t, `id = "acme/jira"
version = "0.4.2"
[permissions]
net = { hosts = ["acme.atlassian.net"], why = "now with a reason" }
"editor.read" = true
exec = ["jira-cli"]
`)
	if c := Ask(same, &old); !c.Update() || !c.Silent() {
		t.Errorf("an update with the same capabilities asks: %+v", c)
	}

	fewer := manifest(t, "id = \"acme/jira\"\nversion = \"0.5.0\"\n[permissions]\nnet = [\"acme.atlassian.net\"]\n")
	if c := Ask(fewer, &old); !c.Silent() || !slices.Equal(c.Removed, []string{"editor.read", "exec:jira-cli"}) {
		t.Errorf("an update that drops capabilities: Silent %v, Removed %v", c.Silent(), c.Removed)
	}

	more := manifest(t, `id = "acme/jira"
version = "0.5.0"
[permissions]
net = ["acme.atlassian.net", "api.atlassian.com"]
"editor.read" = true
gate = true
`)
	c := Ask(more, &old)
	if c.Silent() {
		t.Fatal("an update adding capabilities is silent")
	}
	added := map[Capability][]string{}
	var fresh []Capability
	for _, r := range c.Rows {
		if len(r.Added) > 0 {
			added[r.Cap] = r.Added
		}
		if r.New() {
			fresh = append(fresh, r.Cap)
		}
	}
	if !slices.Equal(added[CapNet], []string{"net:api.atlassian.com"}) || !slices.Equal(added[CapGate], []string{"gate"}) || len(added) != 2 {
		t.Errorf("added %v", added)
	}
	if !slices.Equal(fresh, []Capability{CapGate}) {
		t.Errorf("new capabilities %v, want gate (net only widened)", fresh)
	}
	if !slices.Equal(c.Removed, []string{"exec:jira-cli"}) {
		t.Errorf("removed %v", c.Removed)
	}
}

// The lockfile records consent as "net:acme.atlassian.net", "secrets:JIRA_TOKEN@acme.atlassian.net",
// "editor.read": a plugin whose lock entry covers its capabilities activates without asking.
func TestConsentLockForm(t *testing.T) {
	m := manifest(t, jiraManifest)
	locked := Locked(m.Permissions)
	want := []string{"agent.prompt", "agent.tools:jira_comment", "agent.tools:jira_ticket", "editor.read", "gate",
		"net:acme.atlassian.net", "secrets:JIRA_TOKEN@acme.atlassian.net"}
	if !slices.Equal(locked, want) {
		t.Errorf("Locked %v, want %v", locked, want)
	}
	if d := Diff(locked, locked); len(d) != 0 {
		t.Errorf("consent to itself leaves %v", d)
	}
	if d := Diff(locked, append(slices.Clone(want[:5]), "net:*", "secrets:JIRA_TOKEN@acme.atlassian.net")); len(d) != 0 {
		t.Errorf("net:* doesn't cover a host: %v", d)
	}
	if d := Diff(locked, want[1:]); !slices.Equal(d, []string{"agent.prompt"}) {
		t.Errorf("Diff %v", d)
	}
}
