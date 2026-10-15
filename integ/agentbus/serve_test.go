package agentbus_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	ab "github.com/liamg/oriel/integ/agentbus"
	"github.com/liamg/oriel/integ/agentbus/bustest"
)

// The plugin's process as its Lua drives it: configured, telling it of every change, handing out the asks due an
// answer from the agent, and doing the GitHub half of review requests.

type served struct {
	s    *ab.Server
	mu   sync.Mutex
	last ab.Snapshot
	n    int
}

func (v *served) snap() ab.Snapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.last
}

func serve(t *testing.T, bus *bustest.Bus, g ab.GitHub, cfg ab.Configure) *served {
	t.Helper()
	bus.DefaultUser = "me@x.io"
	v := &served{}
	v.s = &ab.Server{GitHub: g, Every: time.Hour, Notify: func(method string, params any) {
		if method == "changed" {
			v.mu.Lock()
			v.last, v.n = params.(ab.Snapshot), v.n+1
			v.mu.Unlock()
		}
	}}
	t.Cleanup(v.s.Close)
	cfg.URL = bus.URL()
	call(t, v.s, "configure", cfg)
	waitFor(t, func() bool { return v.snap().Status.Connected })
	return v
}

func call(t *testing.T, s *ab.Server, method string, params any) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(params)
	out, err := s.Handle(context.Background(), method, raw)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	b, _ := json.Marshal(out)
	return b
}

// An ask unanswered at its deadline is handed to the agent once, with crit's prompt; its answer goes back labelled
// as the agent's, after crit's line saying the human was away.
func TestAutoAnswerAtTheDeadline(t *testing.T) {
	bus := bustest.New(t)
	v := serve(t, bus, nil, ab.Configure{Asks: true})
	if got := v.snap().Status; got.User != "me@x.io" || got.Instance != "me@x.io/oriel-"+strings.TrimPrefix(got.Instance, "me@x.io/oriel-") {
		t.Fatalf("status: %+v", got)
	}
	bob := dial(t, bus, "bob@x.io", nil)
	by := time.Now().Add(2 * time.Minute).UTC().Truncate(time.Second)
	id, err := bob.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0,
		ab.AskBody{Question: "Is RoundMinor safe for JPY?", Options: []string{"yes", "not yet"}, AnswerBy: by.Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(v.snap().Items) == 1 })
	it := v.snap().Items[0]
	if it.Ask.Question != "Is RoundMinor safe for JPY?" || it.DeadlineAt != float64(by.Add(-ab.AutoAnswerMargin).Unix()) || it.CreatedAt == 0 {
		t.Fatalf("item: %+v", it)
	}
	if e := v.snap().Log[0]; e.Event != ab.EventReceived || e.AtS == 0 {
		t.Fatalf("log: %+v", e)
	}

	var due struct{ Items []ab.Due }
	json.Unmarshal(call(t, v.s, "due", map[string]any{"now": float64(time.Now().Unix())}), &due)
	if len(due.Items) != 0 {
		t.Fatalf("due before the deadline: %+v", due)
	}
	json.Unmarshal(call(t, v.s, "due", map[string]any{"now": it.DeadlineAt}), &due)
	if len(due.Items) != 1 || due.Items[0].ID != id || !strings.Contains(due.Items[0].Prompt, "Question: Is RoundMinor safe for JPY?") ||
		!strings.Contains(due.Items[0].Prompt, "Options: yes / not yet") || !strings.Contains(due.Items[0].Prompt, "never guess") {
		t.Fatalf("due: %+v", due)
	}
	json.Unmarshal(call(t, v.s, "due", map[string]any{"now": it.DeadlineAt + 10}), &due)
	if len(due.Items) != 0 {
		t.Fatal("handed out twice")
	}
	waitFor(t, func() bool { return len(v.snap().Items) == 0 })

	call(t, v.s, "auto_answer", map[string]any{"id": id, "text": "Yes: the precision comes from the currency table."})
	state, replies := bus.Replies(id)
	if state != ab.StateAnswered || len(replies) != 1 {
		t.Fatalf("on the bus: %s %+v", state, replies)
	}
	var body ab.AnswerBody
	json.Unmarshal(replies[0].Body, &body)
	if body.By != ab.AnsweredByAgent || body.Text != ab.AutoAnswerPrefix+"\n\nYes: the precision comes from the currency table." {
		t.Fatalf("answer: %+v", body)
	}
	waitFor(t, func() bool { l := v.snap().Log; return len(l) > 0 && l[0].Event == ab.EventAnswered })
	if e := v.snap().Log[0]; e.Detail != ab.AnsweredByAgent || e.From.User != "bob@x.io" {
		t.Fatalf("logged: %+v", e)
	}
	raw, _ := json.Marshal(map[string]any{"id": id, "text": "again"})
	if _, err := v.s.Handle(context.Background(), "auto_answer", raw); err == nil {
		t.Error("answered for the agent twice")
	}
}

// Your own asks (another of your agents asking you) aren't answered for you.
func TestOwnAsksAreNotAutoAnswered(t *testing.T) {
	bus := bustest.New(t)
	v := serve(t, bus, nil, ab.Configure{Asks: true})
	other, err := ab.Dial(context.Background(), ab.Options{URL: bus.URL(), Instance: ab.Instance{ID: "laptop"}, Header: bustest.Header("me@x.io")})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	by := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	other.Ask(context.Background(), ab.Target{User: "me@x.io"}, 0, ab.AskBody{Question: "q", AnswerBy: by})
	waitFor(t, func() bool { return len(v.snap().Items) == 1 })
	var due struct{ Items []ab.Due }
	json.Unmarshal(call(t, v.s, "due", map[string]any{"now": v.snap().Items[0].DeadlineAt}), &due)
	if len(due.Items) != 0 {
		t.Fatalf("your own ask was handed to the agent: %+v", due)
	}
}

// A review request's card carries its PR's size; claimed, then reviewed on GitHub itself, the review goes back to
// the asker as its result (crit's ghreviewed.go). A review from before the request doesn't count.
func TestGitHubReviewIsReportedBack(t *testing.T) {
	bus := bustest.New(t)
	fake := ghtest.New(t)
	fake.SetLogin("me-gh")
	pr := fake.AddPR("acme/api", gh.PR{Title: "Add tiered discounts", Base: "main"})
	fake.SetPRSize("acme/api", pr.Number, ghtest.PRSize{Author: "priya", HeadSHA: "h1", Additions: 212, Deletions: 40,
		Files: make([]gh.PRFile, 9)})
	fake.SetNow(time.Now().Add(-time.Hour))
	fake.AddReview("acme/api", pr.Number, gh.Review{User: "me-gh", State: gh.ReviewCommented, Body: "earlier"})
	v := serve(t, bus, fake.Client().(gh.API), ab.Configure{Reviews: true, Repos: []string{"acme/*"}})
	priya := dial(t, bus, "priya@x.io", nil)
	id, err := priya.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: pr.URL, Note: "pricing"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s := v.snap(); return len(s.Items) == 1 && s.Items[0].Review.PR.Title != "" })
	it := v.snap().Items[0]
	if want := (ab.PRInfo{Title: "Add tiered discounts", Author: "priya", Base: "main", Additions: 212, Deletions: 40, Files: 9}); it.Review.PR != want ||
		it.Review.Repo != "acme/api" || it.Review.Note != "pricing" || it.Checking {
		t.Fatalf("item: %+v", it)
	}
	call(t, v.s, "claim", map[string]any{"id": id})
	call(t, v.s, "check_github", nil)
	if state, _ := bus.Replies(id); state != ab.StateClaimed {
		t.Fatalf("a review from before the request counted: %s", state)
	}

	fake.SetNow(time.Now().Add(time.Minute))
	rv := fake.AddReview("acme/api", pr.Number, gh.Review{User: "me-gh", State: gh.ReviewChangesRequested, Body: "Two things."})
	call(t, v.s, "check_github", nil)
	state, replies := bus.Replies(id)
	if state != ab.StateDone || len(replies) != 2 {
		t.Fatalf("on the bus: %s %+v", state, replies)
	}
	var res ab.ReviewResultBody
	json.Unmarshal(replies[1].Body, &res)
	if res != (ab.ReviewResultBody{State: ab.ReviewChangesRequested, Summary: "Two things.", URL: rv.URL}) {
		t.Fatalf("result: %+v", res)
	}
	waitFor(t, func() bool { return len(v.snap().Items) == 0 })
}

// A request nobody claimed that's reviewed on GitHub is claimed, then reported.
func TestGitHubReviewClaimsFirst(t *testing.T) {
	bus := bustest.New(t)
	fake := ghtest.New(t)
	pr := fake.AddPR("acme/api", gh.PR{Title: "x"})
	v := serve(t, bus, fake.Client().(gh.API), ab.Configure{Reviews: true, Repos: []string{"acme/api"}})
	priya := dial(t, bus, "priya@x.io", nil)
	id, _ := priya.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: pr.URL})
	waitFor(t, func() bool { return len(v.snap().Items) == 1 })
	fake.SetNow(time.Now().Add(time.Minute))
	fake.AddReview("acme/api", pr.Number, gh.Review{User: "you", State: gh.ReviewApproved})
	call(t, v.s, "check_github", nil)
	state, replies := bus.Replies(id)
	if state != ab.StateDone || len(replies) != 2 || replies[0].Type != ab.TypeReviewClaim {
		t.Fatalf("on the bus: %s %+v", state, replies)
	}
	var res ab.ReviewResultBody
	json.Unmarshal(replies[1].Body, &res)
	if res.State != ab.ReviewApproved || res.Summary != "Reviewed on GitHub." {
		t.Fatalf("result: %+v", res)
	}
}

// A request whose PR passes an auto-approve rule is approved on GitHub at the head that was read, its result sent,
// and logged, without ever showing as a card; one that passes none shows.
func TestAutoApprove(t *testing.T) {
	bus := bustest.New(t)
	fake := ghtest.New(t)
	deps := fake.AddPR("acme/api", gh.PR{Title: "bump deps"})
	fake.SetPRSize("acme/api", deps.Number, ghtest.PRSize{Author: "renovate", HeadSHA: "d1", Additions: 3, Deletions: 3,
		Files: []gh.PRFile{{Path: "go.mod"}, {Path: "go.sum"}}})
	big := fake.AddPR("acme/api", gh.PR{Title: "rewrite"})
	fake.SetPRSize("acme/api", big.Number, ghtest.PRSize{Author: "renovate", HeadSHA: "b1", Additions: 900, Files: []gh.PRFile{{Path: "main.go"}}})
	rules := `[{"name": "deps", "enabled": true, "when": {"all": [{"repo": ["acme/.*"]}, {"paths": ["go.mod", "go.sum"]}]}}]`
	v := serve(t, bus, fake.Client().(gh.API), ab.Configure{Reviews: true, Repos: []string{"acme/*"}, Rules: rules})
	if v.snap().RulesError != "" {
		t.Fatal(v.snap().RulesError)
	}
	priya := dial(t, bus, "priya@x.io", nil)
	id, _ := priya.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: deps.URL})
	waitFor(t, func() bool { return len(v.snap().Auto) == 1 })
	if a := v.snap().Auto[0]; a.Rule != "deps" || a.PR != deps.URL || a.Repo != "acme/api" || a.From.User != "priya@x.io" {
		t.Fatalf("auto: %+v", a)
	}
	if rs := fake.Reviews("acme/api", deps.Number); len(rs) != 1 || rs[0].State != gh.ReviewApproved {
		t.Fatalf("on GitHub: %+v", rs)
	}
	if state, _ := bus.Replies(id); state != ab.StateDone {
		t.Fatalf("on the bus: %s", state)
	}
	waitFor(t, func() bool { l := v.snap().Log; return len(l) > 0 && l[0].Event == ab.EventAutoApproved })
	for _, it := range v.snap().Items {
		if it.ID == id && !it.Checking {
			t.Fatal("the auto-approved request showed as a card")
		}
	}

	id2, _ := priya.RequestReview(context.Background(), ab.Target{Anyone: true}, 0, ab.ReviewRequestBody{PR: big.URL})
	waitFor(t, func() bool {
		for _, it := range v.snap().Items {
			if it.ID == id2 && !it.Checking {
				return true
			}
		}
		return false
	})
	if rs := fake.Reviews("acme/api", big.Number); len(rs) != 0 {
		t.Fatalf("approved a PR no rule passes: %+v", rs)
	}

	call(t, v.s, "configure", ab.Configure{URL: bus.URL(), Reviews: true, Repos: []string{"acme/*"}, Rules: `[{"name": "loose", "enabled": true, "when": {"repo": ["acme/.*"]}}]`})
	waitFor(t, func() bool { return strings.Contains(v.snap().RulesError, "paths or maxLines") })
}

// Presence goes to the bus as it's set, and the bus holds questions back while it's do not disturb.
func TestPresenceFromSettings(t *testing.T) {
	bus := bustest.New(t)
	v := serve(t, bus, nil, ab.Configure{Asks: true, Presence: ab.PresenceDND})
	waitFor(t, func() bool { o := bus.Online(); return len(o) == 1 && o[0].Presence == ab.PresenceDND })
	if v.snap().Status.Presence != ab.PresenceDND {
		t.Fatalf("status: %+v", v.snap().Status)
	}
	call(t, v.s, "configure", ab.Configure{URL: bus.URL(), Asks: true, Presence: ab.PresenceIdle})
	waitFor(t, func() bool { o := bus.Online(); return len(o) == 1 && o[0].Presence == ab.PresenceIdle })
}

// GitHub through the host: http.request to api.github.com with the GITHUB_TOKEN secret named, never given.
func TestHostTransport(t *testing.T) {
	pr, pw := io.Pipe()
	hr, hw := io.Pipe()
	var got map[string]any
	host := jsonrpc.NewConn(pr, hw, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		json.Unmarshal(r.Params, &got)
		if r.Method != "http.request" {
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: r.Method})
			return
		}
		if strings.HasSuffix(got["url"].(string), "/user") {
			r.Reply(map[string]any{"status": http.StatusOK, "body": `{"login": "me-gh"}`}, nil)
			return
		}
		r.Reply(map[string]any{"status": http.StatusNotFound, "body": `{"message": "Not Found"}`}, nil)
	}}, jsonrpc.Options{Version: "2.0"})
	defer host.Close()
	proc := jsonrpc.NewConn(hr, pw, jsonrpc.Handlers{}, jsonrpc.Options{Version: "2.0"})
	defer proc.Close()
	api := gh.API{T: ab.HostTransport{Conn: proc}}
	me, err := api.Me(context.Background())
	if err != nil || me != "me-gh" {
		t.Fatalf("Me: %q %v", me, err)
	}
	if got["url"] != "https://api.github.com/user" || got["auth"] != ab.GitHubSecret || got["method"] != "GET" {
		t.Errorf("request: %+v", got)
	}
	_, err = api.PRDetails(context.Background(), gh.Repo{Owner: "acme", Name: "api"}, 1)
	if !gh.NotFound(err) {
		t.Errorf("a 404 came back as %v", err)
	}
}
