package agentui_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// fakeBus is an inbox source as the agentbus plugin is: other people's agents' questions, with a countdown to the
// auto-answer, and review requests, which go once they're acted on.
type fakeBus struct {
	cards     []uireg.InboxCard
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

func (b *fakeBus) source() uireg.InboxSource {
	return uireg.InboxSource{Name: "bus", Source: "test", Cards: func() []uireg.InboxCard {
		out := make([]uireg.InboxCard, len(b.cards))
		copy(out, b.cards)
		for i := range out {
			id := out[i].ID
			out[i].Dismissed = func() { b.dismissed = append(b.dismissed, id) }
		}
		return out
	}}
}

func busScene(t *testing.T) (*apptest.Scene, *fakeBus) {
	t.Helper()
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 30, 0)
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "search-service", Name: "search-service", Kind: core.KindWork, Status: core.StatusQuiet}},
		core.SessionFocused{ID: "search-service"},
	} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	b := &fakeBus{}
	answer := func(id, text string) func() {
		return func() { b.ran = append(b.ran, id+": "+text); b.drop(id) }
	}
	b.cards = []uireg.InboxCard{
		{ID: "q1", Type: "question", Group: uireg.GroupBus, Session: "search-service", Icon: "question", Tone: "status.needs_you",
			Kicker: "agentbus · question", Title: "tom's agent asks about RoundMinor", Sub: "agentbus · search-service",
			Detail: "Is pricing.RoundMinor safe to call for JPY amounts?", Interrupt: true, Created: s.Now.Add(-18 * time.Second),
			Expires: s.Now.Add(102 * time.Second),
			Actions: []uireg.InboxAction{{Label: "Safe: precision comes from the currency table", Key: "1", Run: answer("q1", "safe")},
				{Label: "Not yet", Key: "2", Run: answer("q1", "not yet")}}},
		{ID: "r1", Type: "review-request", Group: uireg.GroupBus, Mine: true, Session: "search-service", Icon: "review", Tone: "violet",
			Kicker: "agentbus · review request", Title: "priya → you: #4418 Add saved searches", Sub: "agentbus · acme/api · +212 -40",
			Interrupt: true, Created: s.Now.Add(-2 * time.Minute),
			Actions: []uireg.InboxAction{{Label: "Ignore", Command: "bus.review", Args: registry.Args{"id": "r1", "do": "ignore"}},
				{Label: "Claim", Command: "bus.review", Args: registry.Args{"id": "r1", "do": "claim"}},
				{Label: "Claim + Open", Primary: true, Command: "bus.review", Args: registry.Args{"id": "r1", "do": "open"}}}},
	}
	s.Registry().Command(registry.Command{ID: "bus.review", Run: func(_ registry.Ctx, a registry.Args) error {
		b.ran = append(b.ran, a.String("id")+": "+a.String("do"))
		b.drop(a.String("id"))
		return nil
	}})
	s.Registry().Contribute(b.source())
	s.Frame()
	return s, b
}

// inPanel says whether the Inbox panel lists a card whose title starts with title.
func inPanel(s *apptest.Scene, title string) bool {
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
	list := find(s.Access(), "Cards")
	return list != nil && find(list, title) != nil
}

// A card type a plugin adds (agentbus's, faked) stacks over the window and acts; its ✕ dismisses it
// to the Inbox panel, where it still acts; the session's tab counts what waits.
func TestInboxCardsFromASourceActAndDismiss(t *testing.T) {
	s, b := busScene(t)
	s.WantFrame("AGENTBUS · QUESTION", "tom's agent asks about RoundMinor", "1:42", "AGENTBUS · REVIEW REQUEST", "Claim + Open")
	s.Node("2 waiting on you") // the tab's badge, and the title bar's
	s.Node("Inbox: 2 waiting")

	s.ClickTop("Claim")
	if len(b.ran) != 1 || b.ran[0] != "r1: claim" {
		t.Fatalf("Claim ran %v", b.ran)
	}
	s.Frame()
	if strings.Contains(s.Text(), "priya → you") {
		t.Error("the review request stayed once it was claimed")
	}
	s.Node("1 waiting on you")

	s.ClickNode("Dismiss")
	if strings.Contains(s.Text(), "tom's agent asks about RoundMinor") {
		t.Error("the dismissed question is still stacked")
	}
	if len(b.dismissed) != 1 || b.dismissed[0] != "q1" {
		t.Errorf("the source heard of dismissals %v", b.dismissed)
	}
	s.Node("1 waiting on you") // dismissed, it still waits

	s.Run("inbox.open", nil)
	s.Frame()
	s.WantFrame("INBOX", "1 WAITING", "tom's agent asks about RoundMinor", "priya → you: #4418 Add saved searches")
	s.Node("priya → you: #4418 Add saved searches (answered)")
	s.ClickNode("tom's agent asks about RoundMinor")
	s.ClickTop("Not yet")
	if len(b.ran) != 2 || b.ran[1] != "q1: not yet" {
		t.Fatalf("Not yet in the panel ran %v", b.ran)
	}
	s.Frame()
	s.Node("tom's agent asks about RoundMinor (answered)")
	if strings.Contains(s.FullText(), "waiting on you") {
		t.Error("a badge still counts a card once everything is answered")
	}
}

// Escape on a stacked card dismisses it, and the filters keep to their group.
func TestInboxEscapeDismissesAndFilters(t *testing.T) {
	s, _ := busScene(t)
	s.ClickTop("tom's agent asks about RoundMinor") // focuses its card
	s.Type("Esc")
	s.Frame()
	if strings.Contains(s.Text(), "tom's agent asks about RoundMinor") {
		t.Fatal("Escape didn't dismiss the focused card")
	}
	s.Run("inbox.open", nil)
	s.Frame()
	s.ClickTop("Sandbox")
	if inPanel(s, "priya → you") || inPanel(s, "tom's agent") {
		t.Error("the sandbox filter shows bus cards")
	}
	s.ClickTop("Me")
	if !inPanel(s, "priya → you") || inPanel(s, "tom's agent") {
		t.Error("Me doesn't show only the review request for you")
	}
}

// Each ask's card acts where it's drawn and answers the ask through ask.answer: the security key's at the top of
// the window, the sandbox's (network, then gateway) as prompts in the middle one at a time, the question in the
// stack; a card's ✕ dismisses it while the tab still counts it.
func TestAskCardsActAndDismiss(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 30, 0)
	se := core.SessionState{ID: "flaky-e2e", Name: "flaky-e2e", Kind: core.KindWork, Status: core.StatusPermission,
		Agent: core.AgentState{Provider: "claude", Running: true},
		Asks: []core.Ask{
			{ID: "k1", Kind: core.AskKey, Summary: "git push", Detail: "e2e"},
			{ID: "n1", Kind: core.AskNetwork, Host: "proxy.golang.org:443", Detail: "requested by go mod download"},
			{ID: "g1", Kind: core.AskGateway, Tool: "oriel__push", Summary: "git push origin rethink/rounding"},
			{ID: "q1", Kind: core.AskQuestion, Questions: []core.Question{{Question: "Which colour?", Options: []string{"blue", "green"}}}},
		}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	s.Registry().Command(registry.Command{ID: "ask.answer", Run: func(_ registry.Ctx, a registry.Args) error {
		ans := a.String("decision")
		if m, ok := a["answers"].(map[string]string); ok {
			ans = m["Which colour?"]
		}
		got = append(got, a.String("ask")+"="+ans+a.String("scope"))
		return s.Store().Apply(core.AskClosed{Session: se.ID, ID: a.String("ask"), Decision: a.String("decision")})
	}})
	s.Frame()
	s.Frame()
	s.Node("4 waiting on you")
	// The key's touch at the top; the first of the sandbox's asks in the middle, the gateway's after it.
	for _, w := range []string{"Touch your security key", "git push · e2e · flaky-e2e", "Network: proxy.golang.org:443"} {
		if !strings.Contains(s.FullText(), w) {
			t.Errorf("the frame lacks %q:\n%s", w, s.FullText())
		}
	}
	if strings.Contains(s.FullText(), "git push origin rethink/rounding") {
		t.Errorf("the gateway's prompt is up beside the network's:\n%s", s.FullText())
	}
	s.Now = s.Now.Add(time.Second) // past the moment a prompt ignores input as it comes up
	s.ClickTop("Once")
	s.Until("the gateway's prompt, next", func() bool { return strings.Contains(s.FullText(), "git push origin rethink/rounding") })
	s.Now = s.Now.Add(time.Second)
	s.ClickTop("Allow")
	s.Until("the prompts done", func() bool { return !strings.Contains(s.FullText(), "git push origin rethink/rounding") })
	s.ClickTop("Cancel") // the key's, at the top
	s.Until("the question", func() bool { return strings.Contains(s.FullText(), "green") })
	s.ClickTop("green")
	want := []string{"n1=allow", "g1=allow", "k1=deny", "q1=green"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the cards answered %v, want %v", got, want)
	}
}

// A commit whose signing waits shows "Touch your security key" over the window,
// on its own at the top; its Cancel stops the commit and the card goes.
func TestSecurityKeyCardCancelsAWaitingCommit(t *testing.T) {
	r, _, _ := apptest.AgentWindowOn(t, rig.NewHostWith(t, host.Options{KeyWait: 100 * time.Millisecond}))
	st := r.Store.State()
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
	if err := r.Do(func() error {
		return r.Host.Run(context.Background(), "ship.commit", registry.Args{"project": p.Name, "message": "two", "force": true})
	}); err != nil {
		t.Fatal(err)
	}
	r.WaitFrame("the key's card", func(s string) bool { return apptest.HasAll(s, "Touch your security key", "Cancel") })
	r.ClickText("Cancel")
	r.WaitGone("Touch your security key")
	if out, _ := exec.Command("git", "-C", p.Dir, "log", "--format=%s").CombinedOutput(); strings.Contains(string(out), "two") {
		t.Errorf("the cancelled commit was made:\n%s", out)
	}
}

// A Lua plugin adds its own card type with oriel.inbox.add: it stacks, an action runs its command and answers
// it, and oriel.inbox.remove takes another away.
func TestLuaInboxCards(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	var deployed []string
	s.Registry().Command(registry.Command{ID: "deploy.approve", Run: func(_ registry.Ctx, a registry.Args) error {
		deployed = append(deployed, a.String("env"))
		return nil
	}})
	s.Now = time.Now() // oriel.inbox's expires counts from the real clock
	s.Lua(`L.inbox.add({ id = "d1", type = "deploy", group = "github", title = "Deploy api to prod?", kicker = "deploys · approval",
		interrupt = true, expires = 90, actions = { { label = "Approve", key = "y", primary = true, command = "deploy.approve", args = { env = "prod" } },
		{ label = "Hold" } } })`)
	s.Lua(`L.inbox.add({ id = "d2", type = "deploy", title = "Deploy web to prod?", interrupt = true })`)
	s.Frame()
	s.WantFrame("DEPLOYS · APPROVAL", "Deploy api to prod?", "Deploy web to prod?", "1:30")
	s.ClickTop("Approve")
	s.Frame()
	if len(deployed) != 1 || deployed[0] != "prod" {
		t.Fatalf("Approve ran deploy.approve with %v", deployed)
	}
	if strings.Contains(s.Text(), "Deploy api to prod?") {
		t.Error("the approved card is still stacked")
	}
	s.Lua(`L.inbox.remove("d2")`)
	s.Frame()
	if strings.Contains(s.Text(), "Deploy web to prod?") {
		t.Error("oriel.inbox.remove left its card")
	}
	s.Run("inbox.open", nil)
	s.Frame()
	s.Node("Deploy api to prod? (answered)")
}

// A Lua plugin's card with prompt comes up in the middle of the window, as an agent's permission does, and its action
// answers it.
func TestLuaPromptCards(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	var deployed []string
	s.Registry().Command(registry.Command{ID: "deploy.approve", Run: func(_ registry.Ctx, a registry.Args) error {
		deployed = append(deployed, a.String("env"))
		return nil
	}})
	s.Lua(`L.inbox.add({ id = "d1", type = "deploy", title = "Deploy api to prod?", prompt = true,
		actions = { { label = "Approve", key = "y", primary = true, command = "deploy.approve", args = { env = "prod" } } } })`)
	s.Frame()
	s.Frame()
	s.NodeRole(render.RoleDialog, "Deploy api to prod?")
	s.ClickTop("Approve")
	s.Frame()
	if len(deployed) != 1 || deployed[0] != "prod" {
		t.Fatalf("Approve ran deploy.approve with %v", deployed)
	}
	if s.FindRole(render.RoleDialog, "Deploy api to prod?") != nil {
		t.Error("the answered prompt is still up")
	}
}

// A card with somewhere it's answered (open) takes you there when its row is chosen in the Inbox panel, by a click or
// ⏎, rather than opening under the row; it waits on until it's answered.
func TestAnInboxCardOpensWhereItsAnswered(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	var opened []string
	s.Registry().Command(registry.Command{ID: "deploy.show", Run: func(_ registry.Ctx, a registry.Args) error {
		opened = append(opened, a.String("env"))
		return nil
	}})
	s.Lua(`L.inbox.add({ id = "d1", type = "deploy", title = "Deploy api to prod?", detail = "approve it on its page",
		open = { command = "deploy.show", args = { env = "prod" } } })`)
	s.Run("inbox.open", nil)
	s.Frame()
	s.ClickText("Deploy api to prod?")
	s.Frame()
	if len(opened) != 1 || opened[0] != "prod" {
		t.Fatalf("choosing the row ran deploy.show with %v", opened)
	}
	if strings.Contains(s.Text(), "approve it on its page") {
		t.Error("the card opened under its row as well")
	}
	s.Press("<CR>")
	s.Frame()
	if len(opened) != 2 {
		t.Errorf("⏎ on the chosen row ran deploy.show %d times in all, want 2", len(opened))
	}
	s.Node("Deploy api to prod?")
}

// A notice that asks something (a question) is an inbox card, whoever sent it: stacked while it
// waits, answered by its actions, then history. One that only tells stays a toast.
func TestNoticesThatAskAreInboxCards(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	var started []string
	s.Registry().Command(registry.Command{ID: "reviews.start-background", Run: func(_ registry.Ctx, a registry.Args) error {
		started = append(started, a.String("url"))
		return nil
	}})
	s.Host().Notify(core.Notice{ID: "reviews.asked:u1", Severity: core.SevQuestion, Source: "plugin:reviews", Title: "Review requested",
		Text: "mkhan asked you to review api#812", Actions: []core.Action{{Title: "Start review", Command: "reviews.start-background", Args: map[string]any{"url": "u1"}}}})
	s.Host().Notify(core.Notice{ID: "saved", Severity: core.SevInfo, Source: "plugin:x", Text: "Saved the thing"})
	s.Frame()
	s.Frame()
	var titles []string
	for _, c := range uireg.InboxCards(s.Registry()) {
		titles = append(titles, c.Title)
	}
	if !slices.Contains(titles, "mkhan asked you to review api#812") || slices.Contains(titles, "Saved the thing") {
		t.Fatalf("the inbox's cards: %q", titles)
	}
	s.WantFrame("REVIEW REQUESTED")
	if n := s.Count("mkhan asked you to review api#812"); n != 1 {
		t.Errorf("the review request shows %d times, not once as a stacked card", n)
	}
	s.ClickTop("Start review")
	s.Frame()
	if len(started) != 1 || started[0] != "u1" {
		t.Fatalf("Start review ran the command with %v", started)
	}
	if strings.Contains(s.Text(), "mkhan asked you") {
		t.Error("the answered card is still stacked")
	}
	s.Run("inbox.open", nil)
	s.Frame()
	s.Node("mkhan asked you to review api#812 (answered)")
}

// Sessions' tabs with their badges (a session behind with a permission calling for you), a security-key touch at the
// top, the stack of a network and a gateway approval and agentbus's review request and question (its countdown ring),
// and the Inbox panel at the right with what waits and what's been answered.
func TestInboxSnapshot(t *testing.T) {
	s, b := busScene(t)
	ago := func(d time.Duration) time.Time { return s.Now.Add(-d) }
	for _, se := range []core.SessionState{
		{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusNeedsYou, Agent: core.AgentState{Provider: "claude", Running: true},
			Asks: []core.Ask{
				{ID: "k1", Kind: core.AskKey, Summary: "git push", Detail: "pricing", Created: ago(10 * time.Second)},
				{ID: "g1", Kind: core.AskGateway, Tool: "oriel__push", Summary: "git push origin rethink/rounding", Detail: "3 commits · +184 -22",
					Rule: "oriel__push", Created: ago(3 * time.Minute)}}},
		{ID: "search-api", Name: "ENG-2231 search api", Kind: core.KindWork, Status: core.StatusPermission, Agent: core.AgentState{Provider: "claude", Running: true},
			Asks: []core.Ask{{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: "make e2e", Created: ago(4 * time.Minute)}}},
		{ID: "flaky-e2e", Name: "flaky e2e sweep", Kind: core.KindJob, Status: core.StatusPermission, Agent: core.AgentState{Provider: "claude", Running: true, Sandboxed: true},
			Asks: []core.Ask{{ID: "n1", Kind: core.AskNetwork, Host: "proxy.golang.org:443", Detail: "requested by go mod download · CONNECT",
				Created: ago(2 * time.Minute)}}},
	} {
		for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
			if err := s.Store().Apply(ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.Store().Apply(core.SessionFocused{ID: "pricing-rounding"})
	s.Registry().Contribute(uireg.InboxSource{Name: "github", Cards: func() []uireg.InboxCard {
		return []uireg.InboxCard{
			{ID: "ci", Group: uireg.GroupGitHub, Icon: "tests", Title: "CI passed · #4417 dash-filters", Sub: "github · 12 checks", Done: true, Created: ago(11 * time.Minute)},
			{ID: "c1", Group: uireg.GroupGitHub, Icon: "comment", Title: "sam commented on #4417", Sub: "github · review · mirrored", Done: true, Created: ago(24 * time.Minute)},
		}
	}})
	_ = b
	s.Run("inbox.open", nil)
	s.Frame()
	s.Lint("the inbox")
	s.Golden("inbox-12")
}

// A notice that confirms what you just did is a dialog in the middle of the window, over the rest, not a card in the
// stack at the bottom right. Its action answers it; Escape cancels it.
func TestConfirmNoticesAreCentredDialogs(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	var discarded int
	s.Registry().Command(registry.Command{ID: "test.discard", Run: func(registry.Ctx, registry.Args) error { discarded++; return nil }})
	ask := func() {
		s.Host().Notify(core.Notice{ID: "test.discard", Severity: core.SevQuestion, Confirm: true, Source: "test", Title: "Discard changes?",
			Text: "Throw away the unstaged changes to a.go?", Actions: []core.Action{{Title: "Discard", Command: "test.discard"},
				{Title: "Keep", Command: "notice.dismiss", Args: map[string]any{"id": "test.discard"}}}})
		s.Frame()
		s.Frame()
	}
	ask()
	d := s.NodeRole(render.RoleDialog, "Discard changes?")
	if !d.Modal {
		t.Error("the dialog doesn't hold focus")
	}
	win, box := apptest.Centre(render.Rect{Max: apptest.MockupSize}), apptest.Centre(d.Bounds)
	if dx, dy := box.X-win.X, box.Y-win.Y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		t.Errorf("the dialog's centre is %v, the window's %v", box, win)
	}
	if n := s.Count("Throw away the unstaged changes to a.go?"); n != 1 {
		t.Errorf("the question shows %d times, not once in its dialog", n)
	}

	s.ClickTop("Discard")
	s.Frame()
	if discarded != 1 {
		t.Fatalf("Discard ran %d times", discarded)
	}
	if s.FindRole(render.RoleDialog, "Discard changes?") != nil {
		t.Error("the answered dialog stayed")
	}

	ask()
	s.Type("Esc")
	s.Frame()
	if s.FindRole(render.RoleDialog, "Discard changes?") != nil {
		t.Error("Escape didn't close the dialog")
	}
	if discarded != 1 {
		t.Error("Escape discarded")
	}
	for _, n := range s.Store().State().Notifications {
		if n.ID == "test.discard" && !n.Dismissed {
			t.Error("Escape left the question waiting")
		}
	}
}
