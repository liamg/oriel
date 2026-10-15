package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	ab "github.com/liamg/oriel/integ/agentbus"
	"github.com/liamg/oriel/integ/agentbus/bustest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// The agentbus plugin (integ/agentbus/plugin) as an install lays it out, its process built into bin/, granted what
// its manifest declares with net narrowed to the fake bus, and a data directory for its log.
func agentbusScene(t *testing.T) (*errScene, *bustest.Bus, *[]registry.Args) {
	t.Helper()
	dir, m := integPlugin(t, "agentbus", "oriel.toml", "init.lua")
	bus := bustest.New(t)
	bus.DefaultUser = "me@x.io"
	var grants []ext.Grant
	for _, g := range m.Permissions {
		if g.Cap == ext.CapNet {
			g.Scope = []string{strings.TrimPrefix(bus.Server.URL, "http://")}
		}
		grants = append(grants, g)
	}
	gs := ext.NewGrants()
	gs.Grant(m.ID, grants)
	p := ext.Plugin{ID: m.ID, Dir: dir, Guard: gs, Data: filepath.Join(t.TempDir(), "data")}

	s := newScene(t, "", "", mockupSize)
	s.now = time.Now().Truncate(time.Second)
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "pricing-service", Name: "pricing-service", Kind: core.KindWork, Status: core.StatusQuiet}},
		core.SessionFocused{ID: "pricing-service"},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	var started []registry.Args
	s.reg.Command(registry.Command{ID: "session.start", Run: func(_ registry.Ctx, a registry.Args) error {
		started = append(started, a)
		return nil
	}})
	vm := s.pluginVM(t, p)
	src, err := os.ReadFile(filepath.Join(dir, "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: src}); err != nil {
		t.Fatal(err)
	}
	if err := s.reg.Options().SetFrom("plugin.agentbus.url", registry.LayerUser, registry.At{}, bus.URL(), "settings.toml"); err != nil {
		t.Fatal(err)
	}
	s.reg.Options().SetFrom("plugin.agentbus.repos", registry.LayerUser, registry.At{}, []string{"acme/*"}, "settings.toml")
	s.until(t, "the plugin on the bus", func() bool { return len(bus.Online()) == 1 })
	return s, bus, &started
}

func answerOf(t *testing.T, bus *bustest.Bus, id string) ab.AnswerBody {
	t.Helper()
	state, replies := bus.Replies(id)
	if state != ab.StateAnswered || len(replies) != 1 {
		t.Fatalf("on the bus: %s %+v", state, replies)
	}
	var body ab.AnswerBody
	json.Unmarshal(replies[0].Body, &body)
	return body
}

// PL-5 done when: crit-reference.md → agentbus, against a fake bus. A question from someone's agent is a card with
// its options and a countdown to the auto-answer (mockup 12: "AGENTBUS · QUESTION", "1:42", "else your agent
// answers, read-only"); a pick answers it as you. One left past its deadline is answered by your agent, a fork of the
// session in front, labelled as the agent's.
func TestAgentbusQuestions(t *testing.T) {
	s, bus, _ := agentbusScene(t)
	tom, err := ab.Dial(context.Background(), ab.Options{URL: bus.URL(), Instance: ab.Instance{ID: "crit-tom", Label: "pricing-service"},
		Header: bustest.Header("tom@x.io")})
	if err != nil {
		t.Fatal(err)
	}
	defer tom.Close()
	by := s.now.Add(2*time.Minute + 12*time.Second).UTC()
	q1, err := tom.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "Is pricing.RoundMinor safe to call for JPY amounts?",
		Options: []string{"Safe: precision comes from the currency table", "Not yet: lands with rethink/rounding"}, AnswerBy: by.Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	s.until(t, "the question's card", func() bool { return strings.Contains(s.frameText(), "AGENTBUS · QUESTION") })
	s.wantFrame(t, "tom asks · pricing-service", "Is pricing.RoundMinor safe to call for JPY amounts?", "1:42",
		"Safe: precision comes from the currency table", "else your agent answers, read-only", "or type an answer…")
	s.clickText(t, "Safe: precision comes from the currency table")
	s.until(t, "the answer on the bus", func() bool { st, _ := bus.Replies(q1); return st == ab.StateAnswered })
	if a := answerOf(t, bus, q1); a.Option != "Safe: precision comes from the currency table" || a.By != ab.AnsweredByHuman {
		t.Errorf("answer: %+v", a)
	}
	s.until(t, "the card gone", func() bool { return !strings.Contains(s.frameText(), "AGENTBUS · QUESTION") })

	asked := ""
	s.reg.Contribute(registry.Forker{Name: "test", Order: 1, Label: func(string) string { return "claude · fork, no tools" },
		Fork: func(_ context.Context, r registry.ForkRequest) (string, error) {
			asked = r.Session + ": " + r.Prompt
			return "Yes: the precision comes from the currency table.", nil
		}})
	q2, _ := tom.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "Does RoundMinor round half to even?",
		AnswerBy: by.Format(time.RFC3339)})
	s.until(t, "the second card", func() bool { return strings.Contains(s.frameText(), "Does RoundMinor round half to even?") })
	s.now = by.Add(-29 * time.Second)
	s.until(t, "the agent's answer", func() bool { st, _ := bus.Replies(q2); return st == ab.StateAnswered })
	if a := answerOf(t, bus, q2); a.By != ab.AnsweredByAgent || a.Text != ab.AutoAnswerPrefix+"\n\nYes: the precision comes from the currency table." {
		t.Errorf("the agent's answer: %+v", a)
	}
	if !strings.HasPrefix(asked, "pricing-service: Someone asked your human a question through agentbus") ||
		!strings.Contains(asked, "Question: Does RoundMinor round half to even?") {
		t.Errorf("the fork was asked %q", asked)
	}
	s.until(t, "its card gone", func() bool { return !strings.Contains(s.frameText(), "Does RoundMinor round half to even?") })
}

// A review request is a card (mockup 12: "AGENTBUS · REVIEW REQUEST", Ignore, Claim, Claim + Open); Claim + Open
// claims it on the bus and starts a review session on its PR. Ignore keeps it off the stack. The assistant asks for
// a review through the plugin's request_review, an act that asks first, and reads the requests waiting on you
// beside GitHub's.
func TestAgentbusReviewRequests(t *testing.T) {
	s, bus, started := agentbusScene(t)
	priya, err := ab.Dial(context.Background(), ab.Options{URL: bus.URL(), Instance: ab.Instance{ID: "crit-priya"}, Header: bustest.Header("priya@x.io"),
		Accepts: &ab.Accepts{Review: &ab.ReviewAccepts{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer priya.Close()
	r1, _ := priya.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: "acme/api#4418", Note: "pricing"})
	s.until(t, "the review request's card", func() bool { return strings.Contains(s.frameText(), "AGENTBUS · REVIEW REQUEST") })
	s.wantFrame(t, "priya → you: #4418", "“pricing” · acme/api", "Ignore", "Claim + Open", "AI review auto-starts")
	s.clickText(t, "Claim + Open")
	s.until(t, "the review session", func() bool { return len(*started) == 1 })
	if a := (*started)[0]; a.String("kind") != "review" || a.String("pr") != "https://github.com/acme/api/pull/4418" {
		t.Errorf("started %v", a)
	}
	if st, replies := bus.Replies(r1); st != ab.StateClaimed || len(replies) != 1 || replies[0].From != "me@x.io" {
		t.Errorf("on the bus: %s %+v", st, replies)
	}
	s.until(t, "the card gone", func() bool { return !strings.Contains(s.frameText(), "priya → you") })

	r2, _ := priya.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: "acme/api#4419"})
	s.until(t, "the second card", func() bool { return strings.Contains(s.frameText(), "priya → you: #4419") })
	s.clickText(t, "Ignore")
	s.until(t, "ignored", func() bool { return !strings.Contains(s.frameText(), "priya → you: #4419") })
	if st, _ := bus.Replies(r2); st != ab.StatePending {
		t.Errorf("ignoring it replied on the bus: %s", st)
	}

	tools := registry.AgentToolsFor(s.reg, "assistant", true)
	if parts := registry.AgentToolParts(s.reg, "review_requests", "assistant", true); len(parts) != 1 || parts[0].Reads != "agentbus" {
		t.Errorf("review_requests' parts: %+v", parts)
	}
	var request registry.AgentTool
	for _, tl := range tools {
		if tl.Name == "request_review" {
			request = tl
		}
	}
	if request.Confirm == nil {
		t.Fatalf("no request_review act for the assistant: %+v", tools)
	}
	var asked registry.Confirmation
	var did registry.Acted
	s.reg.Contribute(registry.AgentOwner{Agent: "assistant",
		Confirm: func(_ registry.Ctx, _ string, c registry.Confirmation) (bool, error) { asked = c; return true, nil },
		Acted:   func(_ registry.Ctx, _ string, a registry.Acted) { did = a }})
	ctx := registry.WithAgent(context.Background(), "assistant")
	_, callErr := s.callTool(t, ctx, request, map[string]any{"pr": "acme/api#4421", "who": "priya@x.io"})
	if callErr != nil || asked.Title != "Ask for a review" || !strings.Contains(asked.Lead, "priya@x.io to review acme/api#4421") ||
		did.Line != "asked priya@x.io to review acme/api#4421" {
		t.Errorf("request_review: %v, asked %+v, did %+v", callErr, asked, did)
	}

	// GitHub's half is the built-in's, through the worker; here a stand-in, as the part is what's agentbus's.
	base := registry.AgentTool{Name: "review_requests", Reads: "github", Run: func(registry.Ctx, map[string]any) (any, error) { return []string{}, nil }}
	out, callErr := s.callTool(t, ctx, base, nil)
	if err := callErr; err != nil || !strings.Contains(out, `"agentbus"`) || !strings.Contains(out, "https://github.com/acme/api/pull/4419") {
		t.Errorf("review_requests: %s %v", out, err)
	}
}

// Presence: ACTIVE, IDLE, DND at the Inbox panel's foot with the bus's name (mockup 12), and in the HUD's agentbus
// panel with the log, and in mission control's strip; a chip sets it on the bus, which holds questions back while it's do not disturb.
func TestAgentbusPresenceAndLog(t *testing.T) {
	s, bus, _ := agentbusScene(t)
	s.run("inbox.open", nil)
	s.until(t, "the panel's foot", func() bool { return strings.Contains(s.frameText(), "presence") })
	s.until(t, "the bus's name", func() bool { return strings.Contains(s.frameText(), "bus · ori") })
	s.wantFrame(t, "ACTIVE", "IDLE", "DND")
	s.clickText(t, "DND")
	s.until(t, "do not disturb on the bus", func() bool { o := bus.Online(); return len(o) == 1 && o[0].Presence == ab.PresenceDND })

	bob := dial(t, bus, "bob@x.io")
	q, _ := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "lunch?"})
	time.Sleep(200 * time.Millisecond)
	s.frame()
	if strings.Contains(s.frameText(), "lunch?") {
		t.Fatal("a question came while do not disturb")
	}
	s.clickText(t, "ACTIVE")
	s.until(t, "the held question", func() bool { return strings.Contains(s.frameText(), "lunch?") })
	s.typeKeysIn(t, "yes", "or type an answer…")
	s.typeKeys("CR")
	s.until(t, "answered", func() bool { st, _ := bus.Replies(q); return st == ab.StateAnswered })
	if a := answerOf(t, bus, q); a.Text != "yes" || a.By != ab.AnsweredByHuman {
		t.Errorf("answer: %+v", a)
	}

	s.run("hud.toggle", nil)
	s.until(t, "the HUD's agentbus panel", func() bool { return strings.Contains(s.frameText(), "AGENTBUS") })
	s.until(t, "the log", func() bool { return strings.Contains(s.frameText(), "bob · answered: yes") })
	s.wantFrame(t, "bob · came in: lunch?", "0 questions · 0 review requests waiting")

	// ui.md 03: mission control's strip carries agentbus presence too.
	s.run("hud.toggle", nil)
	s.run("mission.toggle", nil)
	s.until(t, "presence in mission control's strip", func() bool { return strings.Contains(s.frameText(), "AGENTBUS\n") })
}

func dial(t *testing.T, bus *bustest.Bus, user string) *ab.Client {
	t.Helper()
	c, err := ab.Dial(context.Background(), ab.Options{URL: bus.URL(), Instance: ab.Instance{ID: "other"}, Header: bustest.Header(user)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
