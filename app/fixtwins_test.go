package app_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/gh"

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
func fixTwins(s *apptest.Scene) map[*render.AccessNode]*render.AccessNode {
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
	walk(s.UI().AccessTree())
	return out
}

// fixEntry brings a scene to where one "ask to fix" shows; kind is what its explain twin asks about.
type fixEntry struct {
	name  string
	kind  string
	scene func(t *testing.T) *apptest.Scene
}

// fixEntries are every "ask to fix" Oriel offers (test failures, diagnostics and code actions, CI failures on the
// Ship page, merge conflicts, a red terminal command), and a plugin's.
var fixEntries = []fixEntry{
	{"the failure block", "test-failure", func(t *testing.T) *apptest.Scene {
		s, _ := apptest.TestsScene(t)
		s.Run("test.run.file", nil)
		s.RunDone()
		s.EditorKeys("20Gzt")
		s.Frame()
		return s
	}},
	{"code actions on a diagnostic", "diagnostic", func(t *testing.T) *apptest.Scene {
		s, _ := apptest.LSPScene(t, "go", "gopls", "main.go")
		s.CursorAt(12, 26)
		s.Press("<Space>ca")
		s.LSPSettle()
		return s
	}},
	{"a failed terminal command", "command", func(t *testing.T) *apptest.Scene {
		s := apptest.TermScene(t, registry.Args{"cmd": []string{"sh", "-c", rig.ShellMarks}})
		s.WaitTerm("still oops")
		r, ok := s.Host().PaneRect(layout.ViewRef{ID: lang.TerminalView, Instance: "1"})
		if !ok {
			t.Fatal("no terminal pane")
		}
		s.RightClick(apptest.Centre(r))
		return s
	}},
	{"failing CI on the Ship page", "ci-failure", func(t *testing.T) *apptest.Scene {
		s, _, hub, se, _ := apptest.ShipScene(t)
		hub.AddPR("acme/web-dashboard", gh.PR{Title: "feat(web): show the wait", Head: se.Branch, Base: "main"})
		hub.SetChecks("acme/web-dashboard", se.Branch, gh.Check{ID: 22, Name: "unit", Status: "completed", Conclusion: "failure", Actions: true})
		hub.SetLog(22, "--- FAIL: TestUsageBackoff (0.02s)\n")
		s.Run("ship.open", nil)
		s.Run("ship.refresh", nil)
		s.ShipSettle("the checks", func() bool { return strings.Contains(s.Text(), "Fix CI with agent") })
		return s
	}},
	{"conflicts on the Ship page", "conflict", func(t *testing.T) *apptest.Scene {
		s, _, _, se, ups := apptest.ShipScene(t)
		rig.WriteFiles(t, ups["search-api"], map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 2\n"})
		rig.Git(t, ups["search-api"], "commit", "-q", "-am", "routes")
		rig.Git(t, ups["search-api"], "push", "-q", "origin", "main")
		dir := apptest.ProjectDir(se, "search-api")
		rig.WriteFiles(t, dir, map[string]string{"internal/api/usage/routes.go": "package usage\n\nvar Routes = 3\n"})
		rig.Git(t, dir, "commit", "-q", "-am", "ours")
		s.Run("ship.open", nil)
		s.Run("ship.refresh", nil)
		s.ShipSettle("the conflicts", func() bool { return strings.Contains(s.Text(), "Fix conflicts with agent") })
		return s
	}},
	{"a plugin's menu entry", "build", func(t *testing.T) *apptest.Scene {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
		s := apptest.New(t, apptest.Options{WorkDir: dir})
		s.WriteConfig(map[string]string{"init.lua": `oriel.menu.add{ label = "Ask the agent to fix the build", group = "agent",
  when = { view = "editor" }, run = "agent.fix", args = { kind = "build", subject = "the build is red", context = "main.go:3: boom" } }
`})
		s.ReloadLua()
		s.Run("editor.open", registry.Args{"path": "main.go"})
		s.Frame()
		s.Frame()
		for _, v := range s.Host().Layout().Visible() {
			if r, ok := s.Host().PaneRect(v); ok && v.ID == kit.EditorView {
				s.RightClick(apptest.Centre(r))
				return s
			}
		}
		t.Fatal("no editor pane")
		return s
	}},
}

// Each fix entry point has an explain twin: wherever Oriel offers "ask the agent to fix", it also offers "ask to
// explain". The twin shows beside its entry point and asks the explainer (which answers text, and can't change a
// file) about the same thing, not the agent.
func TestEveryFixHasAnExplainTwin(t *testing.T) {
	for _, e := range fixEntries {
		t.Run(e.name, func(t *testing.T) {
			s := e.scene(t)
			f := &apptest.FakeExplainer{}
			s.Registry().Contribute(registry.Explainer{Name: "fake", Explain: f.Explain})
			s.Frame()
			s.Frame()
			twins := fixTwins(s)
			if len(twins) == 0 {
				t.Fatalf("no ask to fix here:\n%s", s.Text())
			}
			var twin *render.AccessNode
			for fix, ex := range twins {
				if ex == nil {
					t.Fatalf("%q has no explain twin beside it:\n%s", fix.Name, s.Text())
				}
				twin = ex
			}
			if twin.Disabled {
				t.Fatalf("%q is off with an explainer to ask", twin.Name)
			}
			s.Click(apptest.Centre(twin.Bounds), 0)
			s.ExplainSettle()
			asked := f.Asked()
			if len(asked) != 1 || asked[0].Kind != e.kind || asked[0].Subject == "" || asked[0].Context == "" {
				t.Fatalf("the twin asked %+v", asked)
			}
			if s.Showing(kit.RequestsView) {
				t.Error("explaining opened a request")
			}
		})
	}
}

// An answer lands where it was asked: under the failure, between the diagnostic's lines, and for a terminal
// command (text, not a file's lines) in a notice.
func TestExplainTwinAnswers(t *testing.T) {
	for _, c := range []struct {
		entry int
		want  string
	}{{0, "EXPLAIN · CHANGES NOTHING"}, {1, "EXPLAIN"}} {
		e := fixEntries[c.entry]
		t.Run(e.name, func(t *testing.T) {
			s := e.scene(t)
			f := &apptest.FakeExplainer{}
			s.Registry().Contribute(registry.Explainer{Name: "fake", Explain: f.Explain})
			// The twins are there once what they explain is: a diagnostic's code actions, say, come from the server.
			s.Until("an Explain beside each fix", func() bool {
				s.Frame()
				twins := fixTwins(s)
				for _, ex := range twins {
					if ex == nil {
						return false
					}
				}
				return len(twins) > 0
			})
			for _, ex := range fixTwins(s) {
				s.Click(apptest.Centre(ex.Bounds), 0)
			}
			s.ExplainSettle()
			s.WantText("the answer", c.want, "Answer 1 (brief)")
		})
	}
	t.Run("a failed terminal command", func(t *testing.T) {
		s := fixEntries[2].scene(t)
		f := &apptest.FakeExplainer{}
		s.Registry().Contribute(registry.Explainer{Name: "fake", Explain: f.Explain})
		s.Frame()
		for _, ex := range fixTwins(s) {
			s.Click(apptest.Centre(ex.Bounds), 0)
		}
		s.ExplainSettle()
		n, _ := apptest.LastNotice(s.Store().State(), "explain")
		if !strings.Contains(n.Title, "`false` exited 1") || n.Text != "Answer 1 (brief)" {
			t.Errorf("the notice: %+v", n)
		}
		if r := f.Asked()[0]; !strings.Contains(r.Context, "$ false") || !strings.Contains(r.Context, "still oops") {
			t.Errorf("asked about %q", r.Context)
		}
	})
}

// A fix entry point goes through agent.fix (askTwins, or a menu item that runs it), never request.new itself,
// so it can't come without its explain twin. It reads every package of the app's: the root, the shell and each
// feature.
func TestFixesGoThroughAgentFix(t *testing.T) {
	allowed := map[string]bool{
		"agentui/requests.go":     true, // request.new itself
		"agentui/requestsview.go": true,
		"explain/explain.go":      true, // Ask a follow-up and request.this: not fixes
		"explain/explaincard.go":  true, // Ask a follow-up
		"explain/explainask.go":   true, // agent.fix
		"explain/walkthrough.go":  true, // Ask about this step: not a fix
	}
	n := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		n++
		if allowed[filepath.ToSlash(path)] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), `"request.new"`) {
			t.Errorf("%s runs request.new: a fix runs registry.FixCommand (askTwins or a MenuItem), which has its explain twin", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 100 {
		t.Fatalf("read only %d files of the app's", n)
	}
}

// Explain runs tool-less: in a session a twin is answered by the agent's fork ($.model.fork: tool-less, outside the
// main conversation), and nothing reaches the agent; its fix twin writes a request with the trouble attached.
func TestFixTwinAnsweredByTheSessionsFork(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	tr := registry.Trouble{Kind: "command", Subject: "`make` exited 2", Context: "$ make\nboom\n(exit status 2)", Fix: "Fix the failing command `make`."}
	if err := r.Do(func() error { return r.Host.Run(context.Background(), registry.ExplainCommand, tr.Args()) }); err != nil {
		t.Fatal(err)
	}
	st := rig.WaitState(t, r.Store, "the fork's answer", func(st core.State) bool {
		n, ok := apptest.LastNotice(st, "explain.answer")
		return ok && strings.HasPrefix(n.Text, "fork says:")
	})
	if n, _ := apptest.LastNotice(st, "explain.answer"); !strings.Contains(n.Title, "`make` exited 2") {
		t.Errorf("the answer: %+v", n)
	}
	a.NoneSent(t)

	if err := r.Do(func() error { return r.Host.Run(context.Background(), registry.FixCommand, tr.Args()) }); err != nil {
		t.Fatal(err)
	}
	r.WaitFrame("the fix request being written", func(s string) bool {
		return apptest.HasAll(s, "Fix the failing command `make`.", "context")
	})
	a.NoneSent(t)
}
