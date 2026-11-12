package agentui_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// asked waits for session w to have n asks, and returns them.
func asked(r *apptest.Window, n int) []core.Ask {
	st := r.UntilState("the question", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && len(se.Snapshot().Asks) == n
	})
	se, _ := st.Session("w")
	return se.Snapshot().Asks
}

// noQuestionOverTheWindow waits for the inbox to count the question, then says it isn't drawn: no dialog or card
// stacked over the window.
func noQuestionOverTheWindow(t *testing.T, r *apptest.Window, qs ...string) {
	t.Helper()
	count := fmt.Sprintf("Inbox: %d waiting", len(qs))
	r.WaitAccess("the inbox counting the questions", func(r *apptest.Window) bool { return r.Access(count) != nil })
	f := r.Latest("the window settled", func(headless.Frame) bool { return true })
	for _, q := range qs {
		if strings.Contains(f.Text(), q) || strings.Contains(f.Text(), "W · QUESTION") {
			t.Errorf("the question %q shows over the window:\n%s", q, f.Text())
		}
	}
}

// inThread says whether control label is drawn inside the node named within: a thread's card between the lines
// ("Thread …"), or Requests' "Conversation".
func inThread(r *apptest.Window, within, label string) bool {
	var find func(n *render.AccessNode, in bool) bool
	find = func(n *render.AccessNode, in bool) bool {
		in = in || n.Name == within
		if in && n.Name == label {
			return true
		}
		for _, k := range n.Children {
			if find(k, in) {
				return true
			}
		}
		return false
	}
	tr := r.Win.Access()
	return tr != nil && find(tr, false)
}

// pickInThread clicks option label of a question drawn inside within, once it's there.
func pickInThread(t *testing.T, r *apptest.Window, within, label string) {
	t.Helper()
	r.WaitAccess(label+" in "+within, func(r *apptest.Window) bool { return inThread(r, within, label) })
	r.ClickAccessIn(within, label)
}

// An agent asks in a thread it names: the question isn't stacked over the window, the inbox lists it, and choosing
// it there opens the thread in Requests with the question in it; an option clicked there answers the agent.
func TestAQuestionIsAnsweredInItsThread(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	r.Run("request.comment", registry.Args{"body": "Round half to even?"})
	a.WaitSent(t)
	th := threadWith(t, r, "Round half to even?")
	got := make(chan string, 1)
	go func() {
		out, err := a.CallErr("ask", map[string]any{"thread": string(th.ID),
			"questions": []map[string]any{{"question": "Which rounding?", "options": []string{"banker's", "away from zero"}}}})
		if err != nil {
			out = "error: " + err.Error()
		}
		got <- out
	}()
	if asks := asked(r, 1); asks[0].Thread != th.ID {
		t.Errorf("the question is in thread %q, want %q", asks[0].Thread, th.ID)
	}
	noQuestionOverTheWindow(t, r, "Which rounding?")

	r.Run("inbox.open", nil)
	r.WaitText("Fake asks: Which rounding?")
	r.ClickText("Fake asks: Which rounding?")
	r.WaitFrame("the thread in Requests with its question", func(s string) bool {
		return apptest.HasAll(s, "Round half to even?", "Which rounding?", "banker's", "away from zero")
	})
	pickInThread(t, r, "Conversation", "away from zero")
	if out := apptest.Recv(t, "the ask tool's answer", got); !strings.Contains(out, "Which rounding? → away from zero") {
		t.Errorf("the agent was answered %q", out)
	}
	r.WaitGone("banker's")
}

// askIn calls the agent's ask tool for one question in thread tid under ctx, as the agent would, and returns where
// its result lands.
func askIn(ctx context.Context, a *agenttest.FakeSession, tid core.ThreadID, q string, opts ...string) chan string {
	got := make(chan string, 1)
	args, _ := json.Marshal(map[string]any{"thread": string(tid), "questions": []map[string]any{{"question": q, "options": opts}}})
	go func() {
		for _, tool := range a.Spec.Tools {
			if tool.Name == "ask" {
				out, err := tool.Call(ctx, args)
				if err != nil {
					out = "error: " + err.Error()
				}
				got <- out
				return
			}
		}
		got <- "error: no ask tool"
	}()
	return got
}

// A question whose thread is resolved while it waits stays in it, between the editor's lines (where a resolved thread
// otherwise isn't drawn) and in Requests, until it's answered; an answer of your own typed into its field and sent
// with ⏎ answers it.
func TestAQuestionStaysInItsResolvedThread(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	path := filepath.Join(projectDir(t, r), "round.go")
	os.WriteFile(path, []byte("package pricing\n\nfunc roundHalfEven() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	r.Run("request.comment", registry.Args{"file": "round.go", "line": 4, "body": "Floor goes the wrong way."})
	a.WaitSent(t)
	th := threadWith(t, r, "Floor goes the wrong way.")
	got := askIn(context.Background(), a, th.ID, "Which way should it go?", "up", "down")
	asked(r, 1)
	r.Run("request.resolve", registry.Args{"thread": string(th.ID)})
	r.UntilState("the thread resolved", func(st core.State) bool {
		se, _ := st.Session("w")
		t, _ := se.Snapshot().Thread(th.ID)
		return t.Status == core.ThreadResolved
	})
	noQuestionOverTheWindow(t, r, "Which way should it go?")

	r.Do(func() error { return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.WaitFrame("the question in its resolved thread between the lines", func(s string) bool {
		return apptest.HasAll(s, "floor := scaled.Floor()", "Floor goes the wrong way.", "Which way should it go?", "down")
	})
	pickInThread(t, r, "Thread Floor goes the wrong way.", "Your own answer")
	r.WaitFocused("Your own answer")
	r.TypeText("half to even")
	r.WaitText("half to even")
	r.TypeKeys("<CR>")
	if out := apptest.Recv(t, "the ask tool's answer", got); !strings.Contains(out, "Which way should it go? → half to even") {
		t.Errorf("the agent was answered %q", out)
	}
	r.WaitGone("Which way should it go?")
}

// Several questions wait in one thread at once, each with its options. One answered elsewhere (the console's or the
// inbox's card, through ask.answer) leaves the thread; the other is still answered there.
func TestSeveralQuestionsWaitInAThread(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	r.Run("request.comment", registry.Args{"body": "Round half to even?"})
	a.WaitSent(t)
	th := threadWith(t, r, "Round half to even?")
	first := askIn(context.Background(), a, th.ID, "Which rounding?", "banker's", "away from zero")
	asked(r, 1)
	second := askIn(context.Background(), a, th.ID, "Which currency first?", "JPY", "EUR")
	asks := asked(r, 2)
	noQuestionOverTheWindow(t, r, "Which rounding?", "Which currency first?")
	r.Run("request.open", registry.Args{"thread": string(th.ID)})
	for _, opt := range []string{"banker's", "away from zero", "JPY", "EUR"} {
		r.WaitAccess(opt+" in the thread", func(r *apptest.Window) bool { return inThread(r, "Conversation", opt) })
	}
	var rounding core.Ask
	for _, q := range asks {
		if q.Questions[0].Question == "Which rounding?" {
			rounding = q
		}
	}
	r.Run("ask.answer", registry.Args{"id": "w", "ask": rounding.ID, "answers": map[string]string{"Which rounding?": "banker's"}})
	if out := apptest.Recv(t, "the first answer", first); !strings.Contains(out, "banker's") {
		t.Errorf("the first question was answered %q", out)
	}
	r.WaitFrame("only the second question in the thread", func(s string) bool {
		return !strings.Contains(s, "Which rounding?") && apptest.HasAll(s, "Which currency first?", "JPY")
	})
	pickInThread(t, r, "Conversation", "EUR")
	if out := apptest.Recv(t, "the second answer", second); !strings.Contains(out, "Which currency first? → EUR") {
		t.Errorf("the second question was answered %q", out)
	}
	r.WaitGone("Which currency first?")
}

// A question the agent withdraws (its call cancelled, as an interrupt does) leaves its thread and the inbox.
func TestAWithdrawnQuestionLeavesItsThread(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	r.Run("request.comment", registry.Args{"body": "Round half to even?"})
	a.WaitSent(t)
	th := threadWith(t, r, "Round half to even?")
	ctx, cancel := context.WithCancel(context.Background())
	got := askIn(ctx, a, th.ID, "Which rounding?", "banker's", "away from zero")
	asked(r, 1)
	noQuestionOverTheWindow(t, r, "Which rounding?")
	r.Run("request.open", registry.Args{"thread": string(th.ID)})
	r.WaitAccess("the question in the thread", func(r *apptest.Window) bool { return inThread(r, "Conversation", "away from zero") })
	cancel()
	if out := apptest.Recv(t, "the withdrawn call", got); !strings.HasPrefix(out, "error: ") {
		t.Errorf("the withdrawn call returned %q", out)
	}
	asked(r, 0)
	r.WaitGone("away from zero")
	r.WaitAccess("the inbox with nothing waiting", func(r *apptest.Window) bool { return r.Access("Inbox") != nil })
}

// Claude's own question tool names no thread: its question lands in the thread the agent is on, drawn between the
// editor's lines under the line the thread is about, and answered there.
func TestAQuestionWithNoThreadLandsInTheActiveThread(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	path := filepath.Join(projectDir(t, r), "round.go")
	os.WriteFile(path, []byte("package pricing\n\nfunc roundHalfEven() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	r.Run("request.comment", registry.Args{"file": "round.go", "line": 4, "body": "Floor goes the wrong way."})
	a.WaitSent(t)
	th := threadWith(t, r, "Floor goes the wrong way.")
	ch := a.Expect("q1")
	a.Emit(agent.Question{ID: "q1", Questions: []core.Question{{Question: "Use math.Round?", Options: []string{"Yes, math.Round", "Keep Floor"}}}})
	if asks := asked(r, 1); asks[0].Thread != th.ID {
		t.Errorf("the question is in thread %q, want the active %q", asks[0].Thread, th.ID)
	}
	noQuestionOverTheWindow(t, r, "Use math.Round?")

	r.Do(func() error { return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.WaitFrame("the question in the thread between the lines", func(s string) bool {
		return apptest.HasAll(s, "floor := scaled.Floor()", "Floor goes the wrong way.", "Use math.Round?", "Keep Floor")
	})
	pickInThread(t, r, "Thread Floor goes the wrong way.", "Keep Floor")
	if d := waitDecision(t, ch); d.Answers["Use math.Round?"] != "Keep Floor" {
		t.Errorf("answered %+v", d)
	}
	r.WaitGone("Use math.Round?")
}

// A question's options are reached from its field by keyboard: ⇧⇥ moves back onto the last option, and Space picks it.
func TestAQuestionsOptionsAnswerByKeyboard(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	r.Run("request.comment", registry.Args{"body": "Round half to even?"})
	a.WaitSent(t)
	th := threadWith(t, r, "Round half to even?")
	got := askIn(context.Background(), a, th.ID, "Which rounding?", "banker's", "away from zero")
	asked(r, 1)
	noQuestionOverTheWindow(t, r, "Which rounding?")
	r.Run("request.open", registry.Args{"thread": string(th.ID)})
	pickInThread(t, r, "Conversation", "Your own answer")
	r.WaitFocused("Your own answer")
	r.TypeKeys("<S-Tab>")
	r.WaitFocused("away from zero")
	r.TypeKeys("<Space>")
	if out := apptest.Recv(t, "the ask tool's answer", got); !strings.Contains(out, "Which rounding? → away from zero") {
		t.Errorf("the agent was answered %q", out)
	}
	r.WaitGone("banker's")
}

// A question in a session behind the one in front isn't stacked over the window: its tab counts it, the inbox lists
// it, and choosing it there brings its session to the front on the thread.
func TestAQuestionInASessionBehind(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = apptest.At(14, 30, 0)
	behind := core.SessionState{ID: "flaky-e2e", Name: "flaky-e2e", Kind: core.KindWork, Status: core.StatusNeedsYou,
		Agent: core.AgentState{Provider: "claude", Running: true},
		Threads: []core.Thread{{ID: "t1", Title: "Retry the flaky test", Status: core.ThreadOpen,
			Comments: []core.Comment{{ID: "c1", Author: core.AuthorYou, Body: "Retry the flaky test"}}}},
		Asks: []core.Ask{{ID: "q1", Kind: core.AskQuestion, Thread: "t1", Questions: []core.Question{{Question: "How many retries?", Options: []string{"1", "3"}}}}}}
	front := core.SessionState{ID: "docs", Name: "docs", Kind: core.KindWork, Agent: core.AgentState{Provider: "claude", Running: true}}
	for _, ev := range []core.Event{core.SessionSynced{Session: behind}, core.SessionSynced{Session: front},
		core.TabOpened{ID: behind.ID}, core.TabOpened{ID: front.ID}, core.SessionFocused{ID: front.ID}} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	var opened registry.Args
	s.Registry().Command(registry.Command{ID: "session.open", Run: func(_ registry.Ctx, a registry.Args) error {
		opened = a
		return nil
	}})
	s.Frame()
	if strings.Contains(s.FullText(), "How many retries?") || strings.Contains(s.FullText(), "· QUESTION") {
		t.Errorf("the question shows over the window:\n%s", s.FullText())
	}
	if n := s.Count("1 waiting on you"); n != 2 { // the session's tab, and the inbox in the title bar
		t.Errorf("%d counts of the question, want its tab's and the inbox's", n)
	}
	s.Run("inbox.open", nil)
	s.Frame()
	s.ClickText("Claude asks: How many retries?")
	s.Frame()
	if opened.String("id") != "flaky-e2e" {
		t.Fatalf("choosing the question opened %v, want session flaky-e2e", opened)
	}
	then, _ := opened["then"].([]any)
	if len(then) != 1 || fmt.Sprint(then[0]) != fmt.Sprint(map[string]any{"command": "request.open", "args": registry.Args{"thread": "t1"}}) {
		t.Errorf("once on it, the session opens %v, want its thread t1 in Requests", then)
	}
}

// A question shows in its thread between the diff's lines too, where a resolved thread otherwise folds to a line,
// and is answered there.
func TestAQuestionShowsInItsThreadInTheDiff(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	dir := r.ProjectDir("w")
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(rig.RoundBase), 0o644)
	rig.Git(t, dir, "add", "-A")
	rig.Git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	rig.Git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	os.WriteFile(path, []byte(rig.RoundAgent), 0o644)
	r.Run("request.comment", registry.Args{"file": "round.go", "line": 8, "body": "Negative ties?"})
	a.WaitSent(t)
	th := threadWith(t, r, "Negative ties?")
	got := askIn(context.Background(), a, th.ID, "Toward zero or away?", "toward zero", "away")
	asked(r, 1)
	r.Run("request.resolve", registry.Args{"thread": string(th.ID)})
	noQuestionOverTheWindow(t, r, "Toward zero or away?")
	r.Do(func() error { return r.Host.Run(context.Background(), "diff.open", registry.Args{"path": path}) })
	r.WaitFrame("the question in its thread between the diff's lines", func(s string) bool {
		return apptest.HasAll(s, "@@ -5,8 +5,12 @@", "Negative ties?", "Toward zero or away?", "toward zero")
	})
	pickInThread(t, r, "Thread Negative ties?", "toward zero")
	if out := apptest.Recv(t, "the ask tool's answer", got); !strings.Contains(out, "Toward zero or away? → toward zero") {
		t.Errorf("the agent was answered %q", out)
	}
	r.WaitGone("Toward zero or away?")
}
