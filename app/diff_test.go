package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
)

const roundBase = `package pricing

import "github.com/shopspring/decimal"

// roundHalfEven rounds v to p places, ties to even.
func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {
	scaled := v.Shift(p)
	r := scaled.Round(0)
	return r.Shift(-p)
}

// Money is an amount in a currency.
type Money struct {
	Currency string
	V        decimal.Decimal
}

var precision = map[string]int32{"USD": 2, "JPY": 0}

// RoundMinor rounds an amount to its currency's minor unit.
func RoundMinor(amt Money) Money {
	p := precision[amt.Currency]
	return Money{amt.Currency, roundHalfEven(amt.V, p)}
}
`

// roundAgent is round.go after the agent's commit: roundHalfEven truncates and keeps the even neighbour on a tie.
var roundAgent = strings.Replace(roundBase, "\tr := scaled.Round(0)\n\treturn r.Shift(-p)\n",
	"\tt := scaled.Truncate(0)\n\tdiff := scaled.Sub(t).Abs()\n\tif diff.Equal(half) && t.IntPart()%2 == 0 {\n\t\treturn t.Shift(-p)\n\t}\n\treturn scaled.Round(0).Shift(-p)\n", 1)

// roundWork is round.go in the work tree: RoundMinor reads the precision with a fallback, not committed yet.
var roundWork = strings.Replace(roundAgent, "\tp := precision[amt.Currency]\n", "\tp, ok := currency.Precision(amt.Currency)\n\tif !ok { p = 2 }\n", 1)

// diffRepo is a branch off main whose agent commit changes roundHalfEven and adds currency.go, with RoundMinor
// changed on top and not committed: the review mockup's change.
func diffRepo(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{"internal/pricing/round.go": roundBase, "go.mod": "module example.com/pricing\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "pricing-rounding")
	r.Write(map[string]string{"internal/pricing/round.go": roundAgent, "internal/pricing/currency.go": "package pricing\n\nvar half = 1\n"})
	r.Commit("Claude", "round half to even")
	r.Write(map[string]string{"internal/pricing/round.go": roundWork})
	return r
}

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
func openDiff(t *testing.T, r *gittest.Repo, setup func(h plugin.Host)) *running {
	t.Helper()
	app := startApp(t, t.TempDir(), r.Dir, setup)
	app.typeKeys("<M-1>")
	app.waitText("CHANGED 2")
	app.clickText("round.go")
	app.waitFrame("the diff", func(s string) bool { return containsAll(s, "CHANGES", "func roundHalfEven", "MARK REVIEWED") })
	return app
}

// RV-1: ui.md 01c, "Opening a file here opens its branch diff, not the plain file"; ui.md 06, "Split diff with
// intraline highlights". The diff is the branch's against the merge-base, uncommitted work included, in git's hunks;
// each hunk names its function; old and new sit side by side, the words that changed marked, in the editor's
// highlight colours.
// ref: oracle git
func TestDiffSplitWithIntraline(t *testing.T) {
	r := diffRepo(t)
	app := openDiff(t, r, nil)
	th := darkTheme(t)
	want := gitHeaders(t, r, "internal/pricing/round.go")
	if len(want) != 2 {
		t.Fatalf("git has %d hunks: %v", len(want), want)
	}
	f := app.latest("both hunks, named by function", func(f headless.Frame) bool {
		s := f.Text()
		return containsAll(s, want[0]+" func roundHalfEven", want[1]+" func RoundMinor", "currency.go", "+3", "split", "unified")
	})
	old, okO := wordOn(f, "Round", th.C(tDiffDelWord))
	new, okN := wordOn(f, "Truncate", th.C(tDiffAddWord))
	if !okO || !okN {
		t.Fatalf("the changed words aren't marked:\n%s", f.Text())
	}
	if old.At.Y != new.At.Y || old.At.X >= new.At.X {
		t.Errorf("split: the old line at %v and the new at %v aren't side by side", old.At, new.At)
	}
	for _, g := range glyphs(f, "scaled") {
		if onFill(f, g, th.C(tDiffAddWord)) || onFill(f, g, th.C(tDiffDelWord)) {
			t.Error("an unchanged word is marked")
		}
	}
	if rs := glyphs(f, "return"); len(rs) == 0 || rs[0].Color == th.C(theme.Fg) {
		t.Error("the code isn't highlighted")
	}
	// The uncommitted change shows too, word-marked on the new side.
	if _, ok := wordOn(f, "Precision", th.C(tDiffAddWord)); !ok {
		t.Errorf("the uncommitted change isn't in the diff:\n%s", f.Text())
	}
}

// RV-1: the SPLIT / UNIFIED toggle, remembered: unified puts the old line above the new, and the diff opens that way
// again.
func TestDiffUnifiedIsRemembered(t *testing.T) {
	r := diffRepo(t)
	app := openDiff(t, r, nil)
	app.clickText("unified")
	f := app.latest("unified", func(f headless.Frame) bool {
		o, n := glyphs(f, "Round"), glyphs(f, "Truncate")
		return len(o) > 0 && len(n) > 0 && o[0].At.Y < n[0].At.Y && o[0].At.X == n[0].At.X
	})
	if len(glyphs(f, "-")) == 0 || len(glyphs(f, "+")) == 0 {
		t.Error("unified lines have no - and + signs")
	}
	var v any
	app.do(func() error { v, _, _ = app.host.Options().Get("diff.layout", registry.At{}); return nil })
	if v != "unified" {
		t.Errorf("diff.layout is %v", v)
	}
	app.do(func() error {
		return app.host.Run(context.Background(), "view.close", registry.Args{"id": DiffView, "instance": r.Dir})
	})
	app.waitGone("MARK REVIEWED")
	app.do(func() error {
		return app.host.Run(context.Background(), "diff.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	})
	app.latest("unified again", func(f headless.Frame) bool {
		o, n := glyphs(f, "Round"), glyphs(f, "Truncate")
		return len(o) > 0 && len(n) > 0 && o[0].At.Y < n[0].At.Y
	})
}

// access is the node of the accessibility tree on screen named label, or nil.
func (r *running) access(label string) *render.AccessNode {
	var found *render.AccessNode
	var find func(n *render.AccessNode)
	find = func(n *render.AccessNode) {
		if found == nil && n.Name == label {
			found = n
		}
		for _, k := range n.Children {
			find(k)
		}
	}
	if tr := r.win.Access(); tr != nil {
		find(tr)
	}
	return found
}

func (r *running) waitAccess(what string, ok func(r *running) bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok(r) {
		if time.Now().After(deadline) {
			r.t.Fatalf("never: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// RV-1: the mockup's keys, "]h next hunk · r mark read": ]h and [h move between hunks, r marks the selected one
// reviewed (MARK REVIEWED becomes ✓ REVIEWED), and the file's box in CHANGES is half ticked until every hunk is, then
// ticked; a click on the box takes the file's marks off.
func TestDiffHunkKeysAndReviewed(t *testing.T) {
	r := diffRepo(t)
	app := openDiff(t, r, nil)
	box := "Reviewed: internal/pricing/round.go"
	app.typeKeys("]h")
	app.typeKeys("r")
	app.waitFrame("the first hunk reviewed", func(s string) bool {
		return strings.Count(s, "REVIEWED") == 2 && strings.Contains(s, "MARK REVIEWED") && strings.Contains(s, "1/2 hunks reviewed")
	})
	if n := app.access(box); n == nil || n.Selected {
		t.Errorf("a half-reviewed file's box: %+v", n)
	}
	app.typeKeys("]h")
	app.typeKeys("r")
	app.waitFrame("both hunks reviewed", func(s string) bool { return !strings.Contains(s, "MARK REVIEWED") && strings.Contains(s, "reviewed") })
	app.waitAccess("the file's box ticked", func(r *running) bool { n := r.access(box); return n != nil && n.Selected })
	app.clickAccess(box)
	app.waitFrame("the marks off", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 2 })
	app.typeKeys("[h")
	app.typeKeys("r")
	app.waitFrame("the first hunk reviewed again", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 1 })
}

// RV-1: "?" explains the selected line's code unit through explain.unit (RV-5's), dimmed until it's registered.
func TestDiffExplainRunsExplainUnit(t *testing.T) {
	r := diffRepo(t)
	got := make(chan registry.Args, 1)
	app := openDiff(t, r, func(h plugin.Host) {
		h.Command(registry.Command{ID: ExplainUnit, Title: "Explain", Run: func(_ registry.Ctx, a registry.Args) error {
			got <- a
			return nil
		}})
	})
	app.typeKeys("]h")
	app.typeKeys("jjjjjj")
	app.typeKeys("?")
	select {
	case a := <-got:
		if a.String("file") != filepath.Join(r.Dir, "internal/pricing/round.go") || a.Int("line") != 8 {
			t.Errorf("explain.unit got %v, want round.go's line 8", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("? didn't run explain.unit")
	}
}

// RV-1: ui.md 06, "Authorship breakdown: Claude, you, gofmt": who wrote the lines the change adds, by blame, with
// those not committed yet counted apart.
// ref: oracle git
func TestDiffAuthorship(t *testing.T) {
	r := diffRepo(t)
	app := openDiff(t, r, nil)
	base := strings.TrimSpace(r.Git("merge-base", "HEAD", "main"))
	// The agent's lines are the ones its commit adds that the work tree still has; the rest of the added lines
	// aren't committed.
	agent, work := 0, 0
	for _, l := range strings.Split(r.Git("blame", "--line-porcelain", "--", "internal/pricing/round.go"), "\n") {
		if strings.HasPrefix(l, "author ") {
			if l == "author Claude" {
				agent++
			}
		}
	}
	for _, l := range strings.Split(r.Git("diff", "--numstat", base), "\n") {
		if strings.HasSuffix(l, "internal/pricing/round.go") {
			var add, del int
			if _, err := fmt.Sscan(l, &add, &del); err == nil {
				work = add - agent
			}
		}
	}
	agent += 3 // currency.go, all the agent's
	app.waitFrame("the authorship box", func(s string) bool {
		return containsAll(s, "AUTHORSHIP", "Agent", fmt.Sprintf("%d lines", agent), "uncommitted", fmt.Sprintf("%d lines", work))
	})
}

// RV-1 in a session: a hunk marked reviewed is a session event the worker keeps (core.HunksReviewed), keyed by the
// change itself, so it's still reviewed after another hunk of the file is edited.
func TestDiffReviewedLastsInTheSession(t *testing.T) {
	r, _, _ := agentSession(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(roundBase), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	os.WriteFile(path, []byte(roundWork), 0o644)
	r.do(func() error { return r.host.Run(context.Background(), "diff.open", registry.Args{"path": dir}) })
	r.waitFrame("the session's diff", func(s string) bool { return containsAll(s, "func roundHalfEven", "func RoundMinor") })
	r.clickText("MARK REVIEWED")
	st := waitState(t, r.store, "the mark in the session", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && len(se.Snapshot().ReviewedHunks(sessionProject(t, r), "round.go")) > 0
	})
	se, _ := st.Session("w")
	if len(se.Snapshot().Reviews) != 1 {
		t.Errorf("reviews: %+v", se.Snapshot().Reviews)
	}
	r.waitFrame("one hunk reviewed", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 1 })
	// The agent edits the other hunk; the first stays reviewed.
	os.WriteFile(path, []byte(strings.Replace(roundWork, "if !ok { p = 2 }", "if !ok { p = fallback }", 1)), 0o644)
	r.do(func() error { return r.host.Run(context.Background(), "git.refresh", nil) })
	r.waitText("fallback")
	r.waitFrame("the first hunk still reviewed", func(s string) bool { return strings.Count(s, "MARK REVIEWED") == 1 && strings.Contains(s, "fallback") })
}

// RV-1: "gc comment": a thread opens on the selected line, between the diff's lines, and reaches the agent anchored
// there.
func TestDiffCommentOpensAThreadBetweenTheLines(t *testing.T) {
	r, a, _ := agentSession(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(roundBase), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	os.WriteFile(path, []byte(roundAgent), 0o644)
	r.do(func() error { return r.host.Run(context.Background(), "diff.open", registry.Args{"path": path}) })
	r.waitText("func roundHalfEven")
	r.clickText("Truncate")
	r.waitAccess("the line selected", func(r *running) bool { n := r.access("\tt := scaled.Truncate(0)"); return n != nil && n.Selected })
	r.typeKeys("gc")
	r.waitText("New thread")
	r.typeText("Negative ties?")
	r.waitText("Negative ties?")
	r.typeKeys("<CR>")
	in := a.WaitSent(t)
	if !strings.Contains(in.Text, "round.go:8") || !strings.Contains(in.Text, "Negative ties?") {
		t.Errorf("the agent got %q", in.Text)
	}
	r.waitFrame("the thread between the diff's lines", func(s string) bool { return containsAll(s, "Thread", "L8", "Negative ties?", "Truncate") })
}

// sessionProject is the name of session w's project.
func sessionProject(t *testing.T, r *running) string {
	t.Helper()
	se, ok := r.store.Session("w")
	if !ok {
		t.Fatal("no session w")
	}
	p, _ := se.Snapshot().Primary()
	return p.Name
}

// RV-1: the views that call diff.open when it's registered find it registered, and every control of the diff
// resolves to its help (help.md's lint).
func TestDiffOpenIsAvailableWithHelp(t *testing.T) {
	r := diffRepo(t)
	s := newScene(t, "", r.Dir, mockupSize)
	if !s.reg.Available("diff.open", registry.Args{"path": s.sh.cfg.WorkDir}) {
		t.Error("diff.open isn't available")
	}
	s.sh.git.wait()
	s.run("diff.open", registry.Args{"path": r.Dir})
	for range 3 {
		s.frame()
		s.diff().wait()
		s.sh.runTodo()
	}
	s.frame()
	if !strings.Contains(s.frameText(), "MARK REVIEWED") {
		t.Fatalf("the diff isn't drawn:\n%s", s.frameText())
	}
	s.lint("the diff")
}

func (s *errScene) diff() *diffPlugin {
	for _, e := range s.plugins {
		if d, ok := e.(*diffPlugin); ok {
			return d
		}
	}
	return nil
}

// The diff's own buffer of a file's new side is edited to each new version, so an anchor in it (a thread's) stays
// on its line when lines above it change.
func TestDiffBufferFollowsEdits(t *testing.T) {
	x := &diffPlugin{}
	st := &diffState{bufs: map[string]diffBuf{}}
	one := &diffFile{newText: []byte("a\nb\nc\nd\n")}
	buf := x.newBuffer(st, "f", one)
	an := buf.Anchor(buf.Snapshot().LineStart(3), text.Left)
	two := &diffFile{newText: []byte("a\nnew\nb\nc\nd\n")}
	if x.newBuffer(st, "f", two) != buf {
		t.Fatal("a new version made a new buffer")
	}
	if got := string(buf.Snapshot().Bytes()); got != string(two.newText) {
		t.Fatalf("the buffer reads %q", got)
	}
	off, _ := buf.Resolve(an)
	if line := buf.Snapshot().PosAt(off).Line; line != 4 {
		t.Errorf("the anchor on d is on line %d", line)
	}
}
