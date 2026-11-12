package agentui_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
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
func noQuestionOverTheWindow(t *testing.T, r *apptest.Window, q string) {
	t.Helper()
	r.WaitAccess("the inbox counting the question", func(r *apptest.Window) bool { return r.Access("Inbox: 1 waiting") != nil })
	f := r.Latest("the window settled", func(headless.Frame) bool { return true })
	if strings.Contains(f.Text(), q) || strings.Contains(f.Text(), "W · QUESTION") {
		t.Errorf("the question shows over the window:\n%s", f.Text())
	}
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
	r.ClickText("away from zero")
	if out := apptest.Recv(t, "the ask tool's answer", got); !strings.Contains(out, "Which rounding? → away from zero") {
		t.Errorf("the agent was answered %q", out)
	}
	r.WaitGone("banker's")
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
	r.ClickText("Keep Floor")
	if d := waitDecision(t, ch); d.Answers["Use math.Round?"] != "Keep Floor" {
		t.Errorf("answered %+v", d)
	}
	r.WaitGone("Use math.Round?")
}
