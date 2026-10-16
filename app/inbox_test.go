package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/worker"
)

// fakeBus is an inbox source as the agentbus plugin (PL-5, github.com/liamg/oriel-agentbus) is: other people's agents' questions, with a countdown to the
// auto-answer, and review requests, which go once they're acted on.
type fakeBus struct {
	cards     []registry.InboxCard
	ran       []string
	dismissed []string
}

func (b *fakeBus) drop(id string) {
	for i, c := range b.cards {
		if c.ID == id {
			b.cards = append(b.cards[:i:i], b.cards[i+1:]...)
			return
		}
	}
}

func (b *fakeBus) source() registry.InboxSource {
	return registry.InboxSource{Name: "bus", Source: "test", Cards: func() []registry.InboxCard {
		out := make([]registry.InboxCard, len(b.cards))
		copy(out, b.cards)
		for i := range out {
			id := out[i].ID
			out[i].Dismissed = func() { b.dismissed = append(b.dismissed, id) }
		}
		return out
	}}
}

func busScene(t *testing.T) (*errScene, *fakeBus) {
	t.Helper()
	s := newScene(t, "", "", mockupSize)
	s.now = at(14, 30, 0)
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing-service", Name: "pricing-service", Kind: core.KindWork, Status: core.StatusQuiet}},
		core.SessionFocused{ID: "pricing-service"},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	b := &fakeBus{}
	answer := func(id, text string) func() {
		return func() { b.ran = append(b.ran, id+": "+text); b.drop(id) }
	}
	b.cards = []registry.InboxCard{
		{ID: "q1", Type: "question", Group: registry.GroupBus, Session: "pricing-service", Icon: "question", Tone: "status.needs_you",
			Kicker: "agentbus · question", Title: "tom's agent asks about RoundMinor", Sub: "agentbus · pricing-service",
			Detail: "Is pricing.RoundMinor safe to call for JPY amounts?", Interrupt: true, Created: s.now.Add(-18 * time.Second),
			Expires: s.now.Add(102 * time.Second),
			Actions: []registry.InboxAction{{Label: "Safe: precision comes from the currency table", Key: "1", Run: answer("q1", "safe")},
				{Label: "Not yet", Key: "2", Run: answer("q1", "not yet")}}},
		{ID: "r1", Type: "review-request", Group: registry.GroupBus, Mine: true, Session: "pricing-service", Icon: "review", Tone: "violet",
			Kicker: "agentbus · review request", Title: "priya → you: #4418 Add tiered discounts", Sub: "agentbus · acme/api · +212 -40",
			Interrupt: true, Created: s.now.Add(-2 * time.Minute),
			Actions: []registry.InboxAction{{Label: "Ignore", Command: "bus.review", Args: registry.Args{"id": "r1", "do": "ignore"}},
				{Label: "Claim", Command: "bus.review", Args: registry.Args{"id": "r1", "do": "claim"}},
				{Label: "Claim + Open", Primary: true, Command: "bus.review", Args: registry.Args{"id": "r1", "do": "open"}}}},
	}
	s.reg.Command(registry.Command{ID: "bus.review", Run: func(_ registry.Ctx, a registry.Args) error {
		b.ran = append(b.ran, a.String("id")+": "+a.String("do"))
		b.drop(a.String("id"))
		return nil
	}})
	s.reg.Contribute(b.source())
	s.frame()
	return s, b
}

// inPanel says whether the Inbox panel lists a card whose title starts with title.
func (s *errScene) inPanel(title string) bool {
	var find func(n *render.AccessNode, name string) *render.AccessNode
	find = func(n *render.AccessNode, name string) *render.AccessNode {
		if strings.HasPrefix(n.Name, name) {
			return n
		}
		for _, k := range n.Children {
			if f := find(k, name); f != nil {
				return f
			}
		}
		return nil
	}
	list := find(s.ui.AccessTree(), "Cards")
	return list != nil && find(list, title) != nil
}

func (s *errScene) clickAccess(t *testing.T, label string) {
	t.Helper()
	s.frame()
	s.click(centre(s.access(label).Bounds))
	s.frame() // what the click opened lays out against the frame before
	s.frame()
}

// SH-9 done when: a card type a plugin adds (agentbus's, faked) stacks over the window and acts; its ✕ dismisses it
// to the Inbox panel, where it still acts; the session's tab counts what waits.
func TestInboxCardsFromASourceActAndDismiss(t *testing.T) {
	s, b := busScene(t)
	s.wantFrame(t, "AGENTBUS · QUESTION", "tom's agent asks about RoundMinor", "1:42", "AGENTBUS · REVIEW REQUEST", "Claim + Open")
	s.access("2 waiting on you") // the tab's badge, and the title bar's
	s.access("Inbox: 2 waiting")

	s.clickText(t, "Claim")
	if len(b.ran) != 1 || b.ran[0] != "r1: claim" {
		t.Fatalf("Claim ran %v", b.ran)
	}
	s.frame()
	if strings.Contains(s.frameText(), "priya → you") {
		t.Error("the review request stayed once it was claimed")
	}
	s.access("1 waiting on you")

	s.clickAccess(t, "Dismiss")
	if strings.Contains(s.frameText(), "tom's agent asks about RoundMinor") {
		t.Error("the dismissed question is still stacked")
	}
	if len(b.dismissed) != 1 || b.dismissed[0] != "q1" {
		t.Errorf("the source heard of dismissals %v", b.dismissed)
	}
	s.access("1 waiting on you") // dismissed, it still waits

	s.run("inbox.open", nil)
	s.frame()
	s.wantFrame(t, "INBOX", "1 WAITING", "tom's agent asks about RoundMinor", "priya → you: #4418 Add tiered discounts")
	s.access("priya → you: #4418 Add tiered discounts (answered)")
	s.clickAccess(t, "tom's agent asks about RoundMinor")
	s.clickText(t, "Not yet")
	if len(b.ran) != 2 || b.ran[1] != "q1: not yet" {
		t.Fatalf("Not yet in the panel ran %v", b.ran)
	}
	s.frame()
	s.access("tom's agent asks about RoundMinor (answered)")
	if strings.Contains(s.fullText(), "waiting on you") {
		t.Error("a badge still counts a card once everything is answered")
	}
}

// SH-9: Escape on a stacked card dismisses it, and the filters keep to their group.
func TestInboxEscapeDismissesAndFilters(t *testing.T) {
	s, _ := busScene(t)
	s.clickText(t, "tom's agent asks about RoundMinor") // focuses its card
	s.typeKeys("Esc")
	s.frame()
	if strings.Contains(s.frameText(), "tom's agent asks about RoundMinor") {
		t.Fatal("Escape didn't dismiss the focused card")
	}
	s.run("inbox.open", nil)
	s.frame()
	s.clickText(t, "Sandbox")
	if s.inPanel("priya → you") || s.inPanel("tom's agent") {
		t.Error("the sandbox filter shows bus cards")
	}
	s.clickText(t, "Me")
	if !s.inPanel("priya → you") || s.inPanel("tom's agent") {
		t.Error("Me doesn't show only the review request for you")
	}
}

// SH-9: each ask's card acts from the stack (the network, gateway, question and security-key cards answer the ask
// through ask.answer) and its ✕ dismisses it while the tab still counts it.
func TestAskCardsActAndDismiss(t *testing.T) {
	s := newScene(t, "", "", mockupSize)
	s.now = at(14, 30, 0)
	se := core.SessionState{ID: "flaky-e2e", Name: "flaky-e2e", Kind: core.KindWork, Status: core.StatusPermission,
		Agent: core.AgentState{Provider: "claude", Running: true},
		Asks: []core.Ask{
			{ID: "k1", Kind: core.AskKey, Summary: "git push", Detail: "e2e"},
			{ID: "n1", Kind: core.AskNetwork, Host: "proxy.golang.org:443", Detail: "requested by go mod download"},
			{ID: "g1", Kind: core.AskGateway, Tool: "oriel__push", Summary: "git push origin rethink/rounding"},
			{ID: "q1", Kind: core.AskQuestion, Questions: []core.Question{{Question: "Which colour?", Options: []string{"blue", "green"}}}},
		}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	s.reg.Command(registry.Command{ID: "ask.answer", Run: func(_ registry.Ctx, a registry.Args) error {
		ans := a.String("decision")
		if m, ok := a["answers"].(map[string]string); ok {
			ans = m["Which colour?"]
		}
		got = append(got, a.String("ask")+"="+ans+a.String("scope"))
		return nil
	}})
	s.frame()
	for _, w := range []string{"Touch your security key", "git push · e2e · flaky-e2e", "Network: proxy.golang.org:443"} {
		if !strings.Contains(s.fullText(), w) {
			t.Errorf("the frame lacks %q:\n%s", w, s.fullText())
		}
	}
	s.access("4 waiting on you")
	s.clickText(t, "Cancel")
	s.clickText(t, "Once")
	s.clickText(t, "Allow")
	s.clickText(t, "green")
	want := []string{"k1=deny", "n1=allow", "g1=allow", "q1=green"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the cards answered %v, want %v", got, want)
	}
	s.clickAccess(t, "Dismiss") // the key's, on top
	if strings.Contains(s.frameText(), "Touch your security key") {
		t.Error("the dismissed key card is still shown")
	}
	s.access("4 waiting on you")
}

// SH-9 (crit's security-key prompt): a commit whose signing waits shows "Touch your security key" over the window,
// on its own at the top; its Cancel stops the commit and the card goes.
func TestSecurityKeyCardCancelsAWaitingCommit(t *testing.T) {
	r, _, _ := agentSessionOn(t, newWorkerWith(t, worker.Options{KeyWait: 100 * time.Millisecond}))
	st := r.store.State()
	se, _ := st.Session("w")
	p, _ := se.Snapshot().Primary()
	gpg := filepath.Join(t.TempDir(), "gpg")
	if err := os.WriteFile(gpg, []byte("#!/bin/sh\nsleep 5\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][]string{{"commit.gpgsign", "true"}, {"gpg.program", gpg}, {"user.name", "x"}, {"user.email", "x@x"}} {
		if out, err := exec.Command("git", "-C", p.Dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v: %s", err, out)
		}
	}
	os.WriteFile(filepath.Join(p.Dir, "round.go"), []byte("package pricing\n"), 0o644)
	if err := r.do(func() error {
		return r.host.Run(context.Background(), "ship.commit", registry.Args{"project": p.Name, "message": "two", "force": true})
	}); err != nil {
		t.Fatal(err)
	}
	r.waitFrame("the key's card", func(s string) bool { return containsAll(s, "Touch your security key", "Cancel") })
	r.clickText("Cancel")
	r.waitGone("Touch your security key")
	if out, _ := exec.Command("git", "-C", p.Dir, "log", "--format=%s").CombinedOutput(); strings.Contains(string(out), "two") {
		t.Errorf("the cancelled commit was made:\n%s", out)
	}
}

// SH-9: a Lua plugin adds its own card type with oriel.inbox.add: it stacks, an action runs its command and answers
// it, and oriel.inbox.remove takes another away.
func TestLuaInboxCards(t *testing.T) {
	s := newScene(t, "", "", mockupSize)
	var deployed []string
	s.reg.Command(registry.Command{ID: "deploy.approve", Run: func(_ registry.Ctx, a registry.Args) error {
		deployed = append(deployed, a.String("env"))
		return nil
	}})
	s.now = time.Now() // oriel.inbox's expires counts from the real clock
	s.eval(t, `L.inbox.add({ id = "d1", type = "deploy", group = "github", title = "Deploy api to prod?", kicker = "deploys · approval",
		interrupt = true, expires = 90, actions = { { label = "Approve", key = "y", primary = true, command = "deploy.approve", args = { env = "prod" } },
		{ label = "Hold" } } })`)
	s.eval(t, `L.inbox.add({ id = "d2", type = "deploy", title = "Deploy web to prod?", interrupt = true })`)
	s.frame()
	s.wantFrame(t, "DEPLOYS · APPROVAL", "Deploy api to prod?", "Deploy web to prod?", "1:30")
	s.clickText(t, "Approve")
	s.frame()
	if len(deployed) != 1 || deployed[0] != "prod" {
		t.Fatalf("Approve ran deploy.approve with %v", deployed)
	}
	if strings.Contains(s.frameText(), "Deploy api to prod?") {
		t.Error("the approved card is still stacked")
	}
	s.eval(t, `L.inbox.remove("d2")`)
	s.frame()
	if strings.Contains(s.frameText(), "Deploy web to prod?") {
		t.Error("oriel.inbox.remove left its card")
	}
	s.run("inbox.open", nil)
	s.frame()
	s.access("Deploy api to prod? (answered)")
}

// SH-9 (12-inbox): sessions' tabs with their badges, a security-key touch at the top, the stack of a network and a
// gateway approval and agentbus's review request and question (its countdown ring), and the Inbox panel at the
// right with what waits and what's been answered.
func TestInboxSnapshot(t *testing.T) {
	s, b := busScene(t)
	ago := func(d time.Duration) time.Time { return s.now.Add(-d) }
	for _, se := range []core.SessionState{
		{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusNeedsYou, Agent: core.AgentState{Provider: "claude", Running: true},
			Asks: []core.Ask{
				{ID: "k1", Kind: core.AskKey, Summary: "git push", Detail: "pricing", Created: ago(10 * time.Second)},
				{ID: "g1", Kind: core.AskGateway, Tool: "oriel__push", Summary: "git push origin rethink/rounding", Detail: "3 commits · +184 -22",
					Rule: "oriel__push", Created: ago(3 * time.Minute)}}},
		{ID: "usage-api", Name: "ENG-2231 usage api", Kind: core.KindWork, Status: core.StatusPermission, Agent: core.AgentState{Provider: "claude", Running: true},
			Asks: []core.Ask{{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: "make e2e", Created: ago(4 * time.Minute)}}},
		{ID: "flaky-e2e", Name: "flaky e2e sweep", Kind: core.KindJob, Status: core.StatusPermission, Agent: core.AgentState{Provider: "claude", Running: true, Sandboxed: true},
			Asks: []core.Ask{{ID: "n1", Kind: core.AskNetwork, Host: "proxy.golang.org:443", Detail: "requested by go mod download · CONNECT",
				Created: ago(2 * time.Minute)}}},
	} {
		for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
			if err := s.store.Apply(ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.store.Apply(core.SessionFocused{ID: "pricing-rounding"})
	s.reg.Contribute(registry.InboxSource{Name: "github", Cards: func() []registry.InboxCard {
		return []registry.InboxCard{
			{ID: "ci", Group: registry.GroupGitHub, Icon: "tests", Title: "CI passed · #4417 dash-filters", Sub: "github · 12 checks", Done: true, Created: ago(11 * time.Minute)},
			{ID: "c1", Group: registry.GroupGitHub, Icon: "comment", Title: "sam commented on #4417", Sub: "github · review · mirrored", Done: true, Created: ago(24 * time.Minute)},
		}
	}})
	_ = b
	s.run("inbox.open", nil)
	s.frame()
	s.lint("the inbox")
	s.golden("inbox-12")
}
