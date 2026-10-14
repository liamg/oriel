package app

import (
	"context"
	"github.com/liamg/oriel/gh"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

var (
	askFix     = regexp.MustCompile(`(?i)^ask .*\bfix\b|^fix .*\bwith agent$`)
	askExplain = regexp.MustCompile(`(?i)^ask .*\bexplain\b|^explain .*\bwith agent$`)
)

// fixTwins are the frame's "ask … fix" controls, each with the "ask … explain" control beside it (nil if none).
func (s *errScene) fixTwins() map[*render.AccessNode]*render.AccessNode {
	out := map[*render.AccessNode]*render.AccessNode{}
	var walk func(n *render.AccessNode)
	walk = func(n *render.AccessNode) {
		for i, k := range n.Children {
			if !askFix.MatchString(k.Name) {
				continue
			}
			out[k] = nil
			for _, j := range []int{i - 1, i + 1} {
				if j >= 0 && j < len(n.Children) && askExplain.MatchString(n.Children[j].Name) {
					out[k] = n.Children[j]
				}
			}
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(s.ui.AccessTree())
	return out
}

// fixEntry brings a scene to where one "ask to fix" shows; kind is what its explain twin asks about.
type fixEntry struct {
	name  string
	kind  string
	scene func(t *testing.T) *errScene
}

// fixEntries are every "ask to fix" Oriel offers (ide.md, "test failures, diagnostics and code actions, CI failures
// on the Ship page, merge conflicts, a red terminal command"), and a plugin's.
var fixEntries = []fixEntry{
	{"the failure block", "test-failure", func(t *testing.T) *errScene {
		s, _ := testsScene(t)
		s.run("test.run.file", nil)
		s.runDone(t)
		s.keys("20Gzt")
		s.frame()
		return s
	}},
	{"code actions on a diagnostic", "diagnostic", func(t *testing.T) *errScene {
		s, _ := lspScene(t, "go", "gopls", "main.go")
		s.cursorAt(12, 26)
		s.keyCommand(t, "<Space>ca")
		s.lspSettle()
		return s
	}},
	{"a failed terminal command", "command", func(t *testing.T) *errScene {
		s := termScene(t, registry.Args{"cmd": []string{"sh", "-c", shellMarks}})
		s.waitTerm(t, "still oops")
		r, ok := s.host.PaneRect(layout.ViewRef{ID: TerminalView, Instance: "1"})
		if !ok {
			t.Fatal("no terminal pane")
		}
		s.rightClick(centre(r))
		return s
	}},
	{"failing CI on the Ship page", "ci-failure", func(t *testing.T) *errScene {
		s, _, hub, se, _ := shipScene(t)
		hub.AddPR("acme/web-dashboard", gh.PR{Title: "feat(web): show the wait", Head: se.Branch, Base: "main"})
		hub.SetChecks("acme/web-dashboard", se.Branch, gh.Check{ID: 22, Name: "unit", Status: "completed", Conclusion: "failure", Actions: true})
		hub.SetLog(22, "--- FAIL: TestUsageBackoff (0.02s)\n")
		s.run("ship.open", nil)
		s.run("ship.refresh", nil)
		s.shipSettle(t, "the checks", func() bool { return strings.Contains(s.frameText(), "Fix CI with agent") })
		return s
	}},
	{"conflicts on the Ship page", "conflict", func(t *testing.T) *errScene {
		s, _, _, se, ups := shipScene(t)
		writeFiles(t, ups["usage-api"], map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 2\n"})
		git(t, ups["usage-api"], "commit", "-q", "-am", "routes")
		git(t, ups["usage-api"], "push", "-q", "origin", "main")
		dir := shipDir(se, "usage-api")
		writeFiles(t, dir, map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 3\n"})
		git(t, dir, "commit", "-q", "-am", "ours")
		s.run("ship.open", nil)
		s.run("ship.refresh", nil)
		s.shipSettle(t, "the conflicts", func() bool { return strings.Contains(s.frameText(), "Fix conflicts with agent") })
		return s
	}},
	{"a plugin's menu entry", "build", func(t *testing.T) *errScene {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
		s := newScene(t, "", dir, mockupSize)
		s.writeConfig(t, map[string]string{"init.lua": `oriel.menu.add{ label = "Ask the agent to fix the build", group = "agent",
  when = { view = "editor" }, run = "agent.fix", args = { kind = "build", subject = "the build is red", context = "main.go:3: boom" } }
`})
		s.lua().reload(false)
		s.run("editor.open", registry.Args{"path": "main.go"})
		s.frame()
		s.frame()
		for _, v := range s.sh.layout.root.Visible() {
			if r, ok := s.host.PaneRect(v); ok && v.ID == EditorView {
				s.rightClick(centre(r))
				return s
			}
		}
		t.Fatal("no editor pane")
		return s
	}},
}

// TR-6: "each fix entry point has an explain twin; explain runs tool-less". ide.md: "Wherever Oriel offers "ask the
// agent to fix" … it also offers **ask to explain**, so understanding is always one click away and never requires
// handing over the keyboard." Each entry point shows its twin beside it, and the twin asks the explainer (which
// answers text, and can't change a file) about the same thing, not the agent.
func TestEveryFixHasAnExplainTwin(t *testing.T) {
	for _, e := range fixEntries {
		t.Run(e.name, func(t *testing.T) {
			s := e.scene(t)
			f := &fakeExplainer{}
			s.reg.Contribute(registry.Explainer{Name: "fake", Explain: f.explain})
			s.frame()
			s.frame()
			twins := s.fixTwins()
			if len(twins) == 0 {
				t.Fatalf("no ask to fix here:\n%s", s.frameText())
			}
			var twin *render.AccessNode
			for fix, ex := range twins {
				if ex == nil {
					t.Fatalf("%q has no explain twin beside it:\n%s", fix.Name, s.frameText())
				}
				twin = ex
			}
			if twin.Disabled {
				t.Fatalf("%q is off with an explainer to ask", twin.Name)
			}
			s.click(centre(twin.Bounds))
			s.explainSettle()
			asked := f.asked()
			if len(asked) != 1 || asked[0].Kind != e.kind || asked[0].Subject == "" || asked[0].Context == "" {
				t.Fatalf("the twin asked %+v", asked)
			}
			if s.showing(RequestsView) {
				t.Error("explaining opened a request")
			}
		})
	}
}

// TR-6: an answer lands where it was asked: under the failure, between the diagnostic's lines, and for a terminal
// command (text, not a file's lines) in a notice.
func TestExplainTwinAnswers(t *testing.T) {
	for _, c := range []struct {
		entry int
		want  string
	}{{0, "EXPLAIN · CHANGES NOTHING"}, {1, "EXPLAIN"}} {
		e := fixEntries[c.entry]
		t.Run(e.name, func(t *testing.T) {
			s := e.scene(t)
			f := &fakeExplainer{}
			s.reg.Contribute(registry.Explainer{Name: "fake", Explain: f.explain})
			s.frame()
			for _, ex := range s.fixTwins() {
				s.click(centre(ex.Bounds))
			}
			s.explainSettle()
			s.wantText(t, "the answer", c.want, "Answer 1 (brief)")
		})
	}
	t.Run("a failed terminal command", func(t *testing.T) {
		s := fixEntries[2].scene(t)
		f := &fakeExplainer{}
		s.reg.Contribute(registry.Explainer{Name: "fake", Explain: f.explain})
		s.frame()
		for _, ex := range s.fixTwins() {
			s.click(centre(ex.Bounds))
		}
		s.explainSettle()
		n, _ := lastNotice(s.store.State(), "explain")
		if !strings.Contains(n.Title, "`false` exited 1") || n.Text != "Answer 1 (brief)" {
			t.Errorf("the notice: %+v", n)
		}
		if r := f.asked()[0]; !strings.Contains(r.Context, "$ false") || !strings.Contains(r.Context, "still oops") {
			t.Errorf("asked about %q", r.Context)
		}
	})
}

// TR-6: a fix entry point goes through agent.fix (askTwins, or a menu item that runs it), never request.new itself,
// so it can't come without its explain twin.
func TestFixesGoThroughAgentFix(t *testing.T) {
	allowed := map[string]bool{
		"requests.go":     true, // request.new itself
		"requestsview.go": true,
		"explain.go":      true, // Ask a follow-up and request.this: not fixes
		"explainask.go":   true, // agent.fix
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), `"request.new"`) {
			t.Errorf("%s runs request.new: a fix runs registry.FixCommand (askTwins or a MenuItem), which has its explain twin", f)
		}
	}
}

// TR-6: "explain runs tool-less": in a session a twin is answered by the agent's fork (agents.md: "Side questions
// (btw, explain, verify quiz): $.model.fork: tool-less, outside the main conversation"), and nothing reaches the
// agent; its fix twin writes a request with the trouble attached.
func TestFixTwinAnsweredByTheSessionsFork(t *testing.T) {
	r, a, _ := agentSession(t)
	tr := registry.Trouble{Kind: "command", Subject: "`make` exited 2", Context: "$ make\nboom\n(exit status 2)", Fix: "Fix the failing command `make`."}
	if err := r.do(func() error { return r.host.Run(context.Background(), registry.ExplainCommand, tr.Args()) }); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, r.store, "the fork's answer", func(st core.State) bool {
		n, ok := lastNotice(st, "explain.answer")
		return ok && strings.HasPrefix(n.Text, "fork says:")
	})
	if n, _ := lastNotice(st, "explain.answer"); !strings.Contains(n.Title, "`make` exited 2") {
		t.Errorf("the answer: %+v", n)
	}
	a.NoneSent(t)

	if err := r.do(func() error { return r.host.Run(context.Background(), registry.FixCommand, tr.Args()) }); err != nil {
		t.Fatal(err)
	}
	r.waitFrame("the fix request being written", func(s string) bool {
		return containsAll(s, "Fix the failing command `make`.", "context")
	})
	a.NoneSent(t)
}
