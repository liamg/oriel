package gitui_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/gitui"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/theme"
)

// gitHeaders are git diff's @@ lines for a file against the merge-base with main, without the text after them.
func gitHeaders(t *testing.T, r *gittest.Repo, path string) []string {
	base := strings.TrimSpace(r.Git("merge-base", "HEAD", "main"))
	var out []string
	for _, l := range strings.Split(r.Git("diff", "-U3", "--no-color", base, "--", path), "\n") {
		if strings.HasPrefix(l, "@@") {
			out = append(out, regexp.MustCompile(`^@@ [^@]* @@`).FindString(l))
		}
	}
	return out
}

func darkTheme(t *testing.T) *theme.Resolved {
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	th, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

// glyphs are the runs of text in f that read s.
func glyphs(f headless.Frame, s string) []render.Op {
	var out []render.Op
	for _, op := range f.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == s {
			out = append(out, op)
		}
	}
	return out
}

// onFill says whether a rect filled with col lies under the run.
func onFill(f headless.Frame, run render.Op, col render.Color) bool {
	p := render.Point{X: run.At.X + run.Run.Advance/2, Y: run.At.Y - run.Run.Ascent/2}
	for _, op := range f.Ops {
		if op.Kind == render.OpRect && op.Style.Fill == col && p.X >= op.Rect.Min.X && p.X <= op.Rect.Max.X && p.Y >= op.Rect.Min.Y && p.Y <= op.Rect.Max.Y {
			return true
		}
	}
	return false
}

// wordOn is the first run reading s on a fill of col.
func wordOn(f headless.Frame, s string, col render.Color) (render.Op, bool) {
	for _, g := range glyphs(f, s) {
		if onFill(f, g, col) {
			return g, true
		}
	}
	return render.Op{}, false
}

// openDiff starts the app on the repo and opens round.go's diff from the Explorer's Changed scope.
func openDiff(t *testing.T, r *gittest.Repo, setup func(h plugin.Host)) *apptest.Window {
	t.Helper()
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir, Setup: setup})
	showDiff(app)
	return app
}

// showDiff opens round.go's diff from the explorer.
func showDiff(app *apptest.Window) {
	app.T.Helper()
	app.TypeKeys("<M-1>")
	app.WaitText("CHANGED 2")
	app.ClickText("round.go")
	app.WaitFrame("the diff", func(s string) bool { return apptest.HasAll(s, "CHANGES", "func roundHalfEven", "MARK REVIEWED") })
	app.WaitGit() // the status still coming in builds the diff again, under a click
}

// Opening a changed file opens its branch diff, not the plain file: a split diff with intraline highlights. The diff
// is the branch's against the merge-base, uncommitted work included, in git's hunks; each hunk names its function;
// old and new sit side by side, the words that changed marked, in the editor's highlight colours.
// ref: oracle git
func TestDiffSplitWithIntraline(t *testing.T) {
	r := rig.DiffRepo(t)
	app := openDiff(t, r, nil)
	th := darkTheme(t)
	want := gitHeaders(t, r, "internal/pricing/round.go")
	if len(want) != 2 {
		t.Fatalf("git has %d hunks: %v", len(want), want)
	}
	f := app.Latest("both hunks, named by function", func(f headless.Frame) bool {
		s := f.Text()
		return apptest.HasAll(s, want[0]+" func roundHalfEven", want[1]+" func RoundMinor", "currency.go", "+3", "split", "unified")
	})
	old, okO := wordOn(f, "Round", th.C(gitui.TDiffDelWord))
	new, okN := wordOn(f, "Truncate", th.C(gitui.TDiffAddWord))
	if !okO || !okN {
		t.Fatalf("the changed words aren't marked:\n%s", f.Text())
	}
	if old.At.Y != new.At.Y || old.At.X >= new.At.X {
		t.Errorf("split: the old line at %v and the new at %v aren't side by side", old.At, new.At)
	}
	for _, g := range glyphs(f, "scaled") {
		if onFill(f, g, th.C(gitui.TDiffAddWord)) || onFill(f, g, th.C(gitui.TDiffDelWord)) {
			t.Error("an unchanged word is marked")
		}
	}
	if rs := glyphs(f, "return"); len(rs) == 0 || rs[0].Color == th.C(theme.Fg) {
		t.Error("the code isn't highlighted")
	}
	// The uncommitted change shows too, word-marked on the new side.
	if _, ok := wordOn(f, "Precision", th.C(gitui.TDiffAddWord)); !ok {
		t.Errorf("the uncommitted change isn't in the diff:\n%s", f.Text())
	}
}

// The SPLIT / UNIFIED toggle, remembered: unified puts the old line above the new, and the diff opens that way
// again.
func TestDiffUnifiedIsRemembered(t *testing.T) {
	r := rig.DiffRepo(t)
	app := openDiff(t, r, nil)
	app.ClickText("unified")
	f := app.Latest("unified", func(f headless.Frame) bool {
		o, n := glyphs(f, "Round"), glyphs(f, "Truncate")
		return len(o) > 0 && len(n) > 0 && o[0].At.Y < n[0].At.Y && o[0].At.X == n[0].At.X
	})
	if len(glyphs(f, "-")) == 0 || len(glyphs(f, "+")) == 0 {
		t.Error("unified lines have no - and + signs")
	}
	var v any
	app.Do(func() error { v, _, _ = app.Host.Options().Get("diff.layout", registry.At{}); return nil })
	if v != "unified" {
		t.Errorf("diff.layout is %v", v)
	}
	app.Do(func() error {
		return app.Host.Run(context.Background(), "view.close", registry.Args{"id": gitui.DiffView, "instance": r.Dir})
	})
	app.WaitGone("MARK REVIEWED")
	app.Do(func() error {
		return app.Host.Run(context.Background(), "diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	})
	app.Latest("unified again", func(f headless.Frame) bool {
		o, n := glyphs(f, "Round"), glyphs(f, "Truncate")
		return len(o) > 0 && len(n) > 0 && o[0].At.Y < n[0].At.Y
	})
}

// ]h and [h move between hunks, r marks the selected one
// reviewed (MARK REVIEWED becomes ✓ REVIEWED), and the file's box in CHANGES is half ticked until every hunk is, then
// ticked; a click on the box takes the file's marks off.
func TestDiffHunkKeysAndReviewed(t *testing.T) {
	r := rig.DiffRepo(t)
	app := openDiff(t, r, nil)
	box := "Reviewed: internal/pricing/round.go"
	app.TypeKeys("]h")
	app.TypeKeys("r")
	app.WaitFrame("the first hunk reviewed", func(s string) bool {
		return strings.Count(s, "REVIEWED") == 2 && strings.Contains(s, "MARK REVIEWED") && strings.Contains(s, "1/2 hunks reviewed")
	})
	if n := app.Access(box); n == nil || n.Checked != nil && *n.Checked {
		t.Errorf("a half-reviewed file's box: %+v", n)
	}
	app.TypeKeys("]h")
	app.TypeKeys("r")
	app.WaitFrame("both hunks reviewed", func(s string) bool { return !strings.Contains(s, "MARK REVIEWED") && strings.Contains(s, "REVIEWED") })
	app.WaitAccess("the file's box ticked", func(r *apptest.Window) bool { n := r.Access(box); return n != nil && n.Checked != nil && *n.Checked })
	app.ClickAccess(box)
	app.WaitFrame("the marks off", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 2 })
	app.TypeKeys("[h")
	app.TypeKeys("r")
	app.WaitFrame("the first hunk reviewed again", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 1 })
}

// "?" explains the selected line's code unit through explain.unit, dimmed until it's registered.
func TestDiffExplainRunsExplainUnit(t *testing.T) {
	r := rig.DiffRepo(t)
	got := make(chan registry.Args, 1)
	app := openDiff(t, r, func(h plugin.Host) {
		h.Command(registry.Command{ID: gitui.ExplainUnit, Title: "Explain", Run: func(_ registry.Ctx, a registry.Args) error {
			got <- a
			return nil
		}})
	})
	app.TypeKeys("]h")
	app.TypeKeys("jjjjjj")
	app.TypeKeys("?")
	select {
	case a := <-got:
		if a.String("file") != filepath.Join(r.Dir, "internal/pricing/round.go") || a.Int("line") != 8 {
			t.Errorf("explain.unit got %v, want round.go's line 8", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("? didn't run explain.unit")
	}
}

// In a session, a hunk marked reviewed is a session event the host keeps (core.HunksReviewed), keyed by the
// change itself, so it's still reviewed after another hunk of the file is edited.
func TestDiffReviewedLastsInTheSession(t *testing.T) {
	r, _, _ := apptest.AgentWindow(t)
	dir := r.ProjectDir("w")
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(rig.RoundBase), 0o644)
	rig.Git(t, dir, "add", "-A")
	rig.Git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	rig.Git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	os.WriteFile(path, []byte(rig.RoundWork), 0o644)
	r.Do(func() error { return r.Host.Run(context.Background(), "diff.open", registry.Args{"path": dir}) })
	r.WaitFrame("the session's diff, both hunks against main", func(s string) bool {
		return apptest.HasAll(s, "func roundHalfEven", "func RoundMinor") && strings.Count(s, "MARK REVIEWED") == 2
	})
	r.ClickText("MARK REVIEWED")
	st := r.UntilState("the mark in the session", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && len(se.Snapshot().ReviewedHunks(sessionProject(t, r), "round.go")) > 0
	})
	se, _ := st.Session("w")
	if len(se.Snapshot().Reviews) != 1 {
		t.Errorf("reviews: %+v", se.Snapshot().Reviews)
	}
	r.WaitFrame("one hunk reviewed", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 1 })
	// The agent edits the other hunk; the first stays reviewed.
	os.WriteFile(path, []byte(strings.Replace(rig.RoundWork, "if !ok { p = 2 }", "if !ok { p = fallback }", 1)), 0o644)
	r.Do(func() error { return r.Host.Run(context.Background(), "git.refresh", nil) })
	r.WaitText("fallback")
	r.WaitFrame("the first hunk still reviewed", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 1 && strings.Contains(s, "fallback") })
}

// gc comments: a thread opens on the selected line, between the diff's lines, and reaches the agent anchored
// there.
func TestDiffCommentOpensAThreadBetweenTheLines(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	dir := r.ProjectDir("w")
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(rig.RoundBase), 0o644)
	rig.Git(t, dir, "add", "-A")
	rig.Git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	rig.Git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	os.WriteFile(path, []byte(rig.RoundAgent), 0o644)
	r.Do(func() error { return r.Host.Run(context.Background(), "diff.open", registry.Args{"path": path}) })
	r.WaitText("func roundHalfEven")
	r.WaitGit()
	r.ClickText("Truncate")
	r.WaitAccess("the line selected", func(r *apptest.Window) bool {
		n := r.Access("\tt := scaled.Truncate(0)")
		return n != nil && n.Selected
	})
	r.TypeKeys("gc")
	r.WaitText("New thread")
	r.TypeText("Negative ties?")
	r.WaitText("Negative ties?")
	r.TypeKeys("<CR>")
	in := a.WaitSent(t)
	if !strings.Contains(in.Text, "round.go:8") || !strings.Contains(in.Text, "Negative ties?") {
		t.Errorf("the agent got %q", in.Text)
	}
	r.WaitFrame("the thread between the diff's lines", func(s string) bool { return apptest.HasAll(s, "Thread", "L8", "Negative ties?", "Truncate") })
}

// sessionProject is the name of session w's project.
func sessionProject(t *testing.T, r *apptest.Window) string {
	t.Helper()
	se, ok := r.Store.Session("w")
	if !ok {
		t.Fatal("no session w")
	}
	p, _ := se.Snapshot().Primary()
	return p.Name
}

// The views that call diff.open when it's registered find it registered, and every control of the diff resolves to
// its help.
func TestDiffOpenIsAvailableWithHelp(t *testing.T) {
	r := rig.DiffRepo(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	if !s.Registry().Available("diff.open", registry.Args{"path": r.Dir}) {
		t.Error("diff.open isn't available")
	}
	apptest.PluginOf[*gitui.Git](s, "git").Wait()
	s.Run("diff.open", registry.Args{"path": r.Dir})
	for range 3 {
		s.Frame()
		apptest.PluginOf[*gitui.Diff](s, "diff").Wait()
		s.RunQueued()
	}
	s.Frame()
	if !strings.Contains(s.Text(), "MARK REVIEWED") {
		t.Fatalf("the diff isn't drawn:\n%s", s.Text())
	}
	s.Lint("the diff")
}

// An added line too long for its side is cut at the divider, beside the empty old side, not pushed over it: every
// row's new side starts where the others' do.
func TestDiffSplitLongAddedLineKeepsItsSide(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"round.go": "package p\n", "go.mod": "module x\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"round.go": "package p\n\nfunc a() {\n\tfor ctor, w := range map[string]want{\n\t\t`OpenAiChatModel.builder().apiKey(key)`: {},\n\t}\n}\n"})
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir})
	app.TypeKeys("<M-1>")
	app.WaitText("CHANGED 1")
	app.ClickText("round.go")
	f := app.Latest("the diff", func(f headless.Frame) bool { return strings.Contains(f.Text(), "OpenAi") })
	starts := map[float32]int{}
	for _, op := range f.Ops {
		// A side's clip is a whole row high; the new side's are right of the window's middle.
		if op.Kind == render.OpPushClip && op.Rect.Max.Y-op.Rect.Min.Y == 20 && op.Rect.Min.X > 640 {
			starts[op.Rect.Min.X]++
		}
	}
	if len(starts) != 1 {
		t.Errorf("the new side's rows start at different places: %v", starts)
	}
}
