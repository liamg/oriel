package agentbus_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	ab "github.com/liamg/oriel/integ/agentbus"
	"github.com/liamg/oriel/integ/agentbus/bustest"
)

// crit-reference.md, agentbus: "Question cards with an auto-answer at the deadline", "Review-request cards … with the
// result reported back to the asker", "presence (active, idle, DND), and a log", reproduced against a fake bus
// (bustest): crit's own internal/agentbus tests, on Oriel's port.

// start runs a Bus for me@x.io and waits until it's connected.
func start(t *testing.T, bus *bustest.Bus, s ab.Settings, cfg ab.Config) *ab.Bus {
	t.Helper()
	cfg.Instance = ab.Instance{ID: "oriel-test", Harness: "oriel"}
	cfg.URL, cfg.Header = bus.URL(), bustest.Header("me@x.io")
	b := ab.New(cfg)
	b.SetSettings(context.Background(), s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go b.Run(ctx)
	waitFor(t, func() bool { return b.Status().Connected })
	return b
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 250 {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// dial connects someone else, who may take reviews.
func dial(t *testing.T, bus *bustest.Bus, user string, accepts *ab.Accepts) *ab.Client {
	t.Helper()
	c, err := ab.Dial(context.Background(), ab.Options{URL: bus.URL(), Instance: ab.Instance{ID: "other"}, Header: bustest.Header(user),
		Accepts: accepts})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAskAnswerAndDeadline(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{})
	bob := dial(t, bus, "bob@x.io", nil)

	by := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	id, err := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0,
		ab.AskBody{Question: "safe?", Options: []string{"yes", "no"}, AnswerBy: by.Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.Items()) == 1 })
	it := b.Items()[0]
	if it.ID != id || it.Ask.Question != "safe?" || it.From.User != "bob@x.io" {
		t.Fatalf("item: %+v", it)
	}
	if it.Deadline == nil || !it.Deadline.Equal(by.Add(-ab.AutoAnswerMargin)) {
		t.Fatalf("deadline: %v", it.Deadline)
	}
	if got := b.Due(time.Now()); len(got) != 0 {
		t.Fatalf("due early: %v", got)
	}
	if err := b.Answer(context.Background(), id, ab.AnswerBody{Option: "yes", By: ab.AnsweredByHuman}); err != nil {
		t.Fatal(err)
	}
	if len(b.Items()) != 0 {
		t.Fatal("answered item kept")
	}
	st, _ := bob.Get(context.Background(), id)
	if st.State != ab.StateAnswered {
		t.Fatalf("state: %s", st.State)
	}
}

func TestDueHandsOutOnce(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{})
	bob := dial(t, bus, "bob@x.io", nil)
	by := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	if _, err := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "q", AnswerBy: by}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.Items()) == 1 })
	later := time.Now().Add(time.Minute)
	if got := b.Due(later); len(got) != 1 {
		t.Fatalf("due: %d", len(got))
	}
	if got := b.Due(later); len(got) != 0 {
		t.Fatalf("due again: %d", len(got))
	}
}

func TestCancelRemoves(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{})
	bob := dial(t, bus, "bob@x.io", nil)
	id, _ := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "q"})
	waitFor(t, func() bool { return len(b.Items()) == 1 })
	if err := bob.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.Items()) == 0 })
}

func TestLaterThenShow(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{})
	bob := dial(t, bus, "bob@x.io", nil)
	id, _ := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "q"})
	waitFor(t, func() bool { return len(b.Items()) == 1 })
	b.Hide(id, ab.EventLater)
	if !b.Items()[0].Hidden {
		t.Fatal("not hidden")
	}
	if !b.Show(id) || b.Items()[0].Hidden {
		t.Fatal("not shown again")
	}
	if b.Show("nope") {
		t.Fatal("showed an unknown item")
	}
}

func TestReviewClaimAndSettingsOff(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true, Reviews: true, Repos: []string{"infracost/infra"}}, ab.Config{})
	bob := dial(t, bus, "bob@x.io", nil)
	if _, err := bob.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: "infracost/other#1"}); err != nil {
		t.Fatal(err)
	}
	id, err := bob.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: "infracost/infra#7", Note: "billing"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.Items()) == 1 })
	it := b.Items()[0]
	if it.ID != id || it.Review.Repo != "infracost/infra" || it.Review.Note != "billing" {
		t.Fatalf("review: %+v", it)
	}
	if err := b.Claim(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if it, _ := b.Item(id); !it.Claimed || !it.Hidden {
		t.Fatalf("claimed: %+v", it)
	}

	// Asks off: the bus stops offering them.
	b.SetSettings(context.Background(), ab.Settings{Asks: false, Reviews: true, Repos: []string{"infracost/infra"}})
	if _, err := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "q"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	for _, it := range b.Items() {
		if it.Kind == "ask" {
			t.Fatal("ask offered with asks off")
		}
	}
	if it, ok := b.Item(id); !ok || !it.Claimed {
		t.Fatal("claimed review dropped")
	}
}

func TestDeadlineFromTTL(t *testing.T) {
	created := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	d, ok := ab.Deadline("", created.Format(time.RFC3339), "4h0m0s")
	if !ok || !d.Equal(created.Add(4*time.Hour-ab.AutoAnswerMargin)) {
		t.Fatalf("got %v %v", d, ok)
	}
	if _, ok := ab.Deadline("", "", ""); ok {
		t.Fatal("want no deadline")
	}
}

func TestDeadlineShortAnswerByUsesTTL(t *testing.T) {
	created := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	by := created.Add(30 * time.Second).Format(time.RFC3339)
	d, ok := ab.Deadline(by, created.Format(time.RFC3339), "4h0m0s")
	if !ok || !d.Equal(created.Add(4*time.Hour-ab.AutoAnswerMargin)) {
		t.Fatalf("got %v %v", d, ok)
	}
	if _, ok := ab.Deadline(by, created.Format(time.RFC3339), ""); ok {
		t.Fatal("want no deadline")
	}
	if _, ok := ab.Deadline(by, created.Format(time.RFC3339), "30s"); ok {
		t.Fatal("want no deadline for a short TTL")
	}
	by = created.Add(time.Minute).Format(time.RFC3339)
	d, ok = ab.Deadline(by, created.Format(time.RFC3339), "4h0m0s")
	if !ok || !d.Equal(created.Add(time.Minute-ab.AutoAnswerMargin)) {
		t.Fatalf("got %v %v", d, ok)
	}
}

func TestReposAddedAfterConnect(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true, Reviews: true}, ab.Config{})
	b.SetSettings(context.Background(), ab.Settings{Asks: true, Reviews: true, Repos: []string{"infracost/infra"}})
	bob := dial(t, bus, "bob@x.io", nil)
	if _, err := bob.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: "infracost/infra#1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.Items()) == 1 })
}

func TestLogKeepsAskAndAnswer(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{LogPath: filepath.Join(t.TempDir(), "log.jsonl")})
	bob := dial(t, bus, "bob@x.io", nil)
	id, err := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "safe?"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.Items()) == 1 })
	if err := b.Answer(context.Background(), id, ab.AnswerBody{Text: "yes", By: ab.AnsweredByHuman}); err != nil {
		t.Fatal(err)
	}
	got := b.Log(10)
	if len(got) != 2 {
		t.Fatalf("log: %+v", got)
	}
	if got[0].Event != ab.EventAnswered || got[0].Text != "yes" || got[0].Detail != ab.AnsweredByHuman || got[0].From.User != "bob@x.io" {
		t.Fatalf("answer entry: %+v", got[0])
	}
	if got[1].Event != ab.EventReceived || got[1].Text != "safe?" || got[1].ID != id {
		t.Fatalf("received entry: %+v", got[1])
	}
}

// Without a file the log is kept for as long as the process runs.
func TestLogInMemory(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{})
	bob := dial(t, bus, "bob@x.io", nil)
	bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "safe?"})
	waitFor(t, func() bool { return len(b.Log(10)) == 1 })
	if e := b.Log(10)[0]; e.Event != ab.EventReceived || e.Text != "safe?" {
		t.Fatalf("entry: %+v", e)
	}
}

func TestPresenceSent(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{})
	if got := b.Status().Presence; got != ab.PresenceActive {
		t.Fatalf("default presence = %q", got)
	}
	b.SetSettings(context.Background(), ab.Settings{Asks: true, Presence: ab.PresenceDND})
	bob := dial(t, bus, "bob@x.io", nil)
	waitFor(t, func() bool {
		rows, err := bob.Who(context.Background(), &ab.WhoFilter{User: "me@x.io"})
		return err == nil && len(rows) == 1 && rows[0].Presence == ab.PresenceDND
	})
	// Do not disturb: the bus holds questions back until it ends.
	bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "now?"})
	time.Sleep(200 * time.Millisecond)
	if len(b.Items()) != 0 {
		t.Fatal("offered a question while do not disturb")
	}
	b.SetSettings(context.Background(), ab.Settings{Asks: true, Presence: ab.PresenceIdle})
	waitFor(t, func() bool { return len(b.Items()) == 1 })
}

func TestRequestReviewAndState(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{})
	alice := dial(t, bus, "alice@x.io", &ab.Accepts{Review: &ab.ReviewAccepts{Repos: []string{"infracost/*"}}})

	id, err := b.RequestReview(context.Background(), "https://github.com/infracost/infra/pull/9", "", "")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := b.RequestState(context.Background(), id)
	if st.State != ab.StatePending || st.ClaimBy != "" {
		t.Fatalf("pending: %+v", st)
	}
	waitFor(t, func() bool { return alice.ClaimReview(context.Background(), id) == nil })
	if err := alice.ReviewResult(context.Background(), id, ab.ReviewResultBody{State: ab.ReviewApproved, Summary: "lgtm"}); err != nil {
		t.Fatal(err)
	}
	st, _ = b.RequestState(context.Background(), id)
	if st.ClaimBy != "alice@x.io" || st.Result != ab.ReviewApproved || st.Summary != "lgtm" {
		t.Fatalf("done: %+v", st)
	}
}

// A review this person asked for shows in their log as sent, claimed and reviewed, each once.
func TestRequestReviewIsLogged(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Asks: true}, ab.Config{LogPath: filepath.Join(t.TempDir(), "log.jsonl"), Watch: 50 * time.Millisecond})
	alice := dial(t, bus, "alice@x.io", &ab.Accepts{Review: &ab.ReviewAccepts{Repos: []string{"infracost/*"}}})

	pr := "https://github.com/infracost/infra/pull/9"
	id, err := b.RequestReview(context.Background(), pr, "small one", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return alice.ClaimReview(context.Background(), id) == nil })
	if err := alice.ReviewResult(context.Background(), id, ab.ReviewResultBody{State: ab.ReviewApproved, Summary: "lgtm"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(b.Log(10)) == 3 })
	time.Sleep(150 * time.Millisecond)
	got := b.Log(10)
	if len(got) != 3 {
		t.Fatalf("log: %+v", got)
	}
	if e := got[2]; e.Event != ab.EventRequested || e.ID != id || e.Text != pr || e.Detail != "small one" {
		t.Errorf("requested: %+v", e)
	}
	if e := got[1]; e.Event != ab.EventRequestClaimed || e.From.User != "alice@x.io" || e.Text != pr {
		t.Errorf("claimed: %+v", e)
	}
	if e := got[0]; e.Event != ab.EventRequestReviewed || e.From.User != "alice@x.io" || e.Detail != ab.ReviewApproved+" · lgtm" {
		t.Errorf("reviewed: %+v", e)
	}
}

func TestVetHoldsThenReveals(t *testing.T) {
	bus := bustest.New(t)
	release := make(chan bool)
	b := start(t, bus, ab.Settings{Reviews: true, Repos: []string{"infracost/infra"}},
		ab.Config{Vet: func(context.Context, ab.Item) bool { return <-release }})
	bob := dial(t, bus, "bob@x.io", nil)

	for i, handled := range []bool{false, true} {
		id, err := bob.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: fmt.Sprintf("infracost/infra#%d", i+1)})
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { _, ok := b.Item(id); return ok })
		if it, _ := b.Item(id); !it.Checking {
			t.Fatalf("not held while vetting: %+v", it)
		}
		release <- handled
		if handled {
			time.Sleep(100 * time.Millisecond)
			if it, _ := b.Item(id); !it.Checking {
				t.Fatal("handled request shown")
			}
			continue
		}
		waitFor(t, func() bool { it, _ := b.Item(id); return !it.Checking })
	}
}

// Taken by someone else, a review request goes from this inbox.
func TestTakenElsewhereIsDropped(t *testing.T) {
	bus := bustest.New(t)
	b := start(t, bus, ab.Settings{Reviews: true, Repos: []string{"acme/*"}}, ab.Config{})
	carol := dial(t, bus, "carol@x.io", &ab.Accepts{Review: &ab.ReviewAccepts{Repos: []string{"acme/*"}}})
	bob := dial(t, bus, "bob@x.io", nil)
	id, _ := bob.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: "acme/api#3"})
	waitFor(t, func() bool { return len(b.Items()) == 1 })
	waitFor(t, func() bool { return carol.ClaimReview(context.Background(), id) == nil })
	waitFor(t, func() bool { return len(b.Items()) == 0 })
	if err := b.Claim(context.Background(), id); err == nil {
		t.Error("claimed a review someone else had")
	}
}
