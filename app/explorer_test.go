package app

import (
	"fmt"
	"github.com/liamg/oriel/render"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

// explorerRepo is a branch off main that commits a change, an add, a delete and a rename, then stages one file,
// leaves another modified and one untracked, with vendor/ ignored.
func explorerRepo(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{
		".gitignore":                       "vendor/\n",
		"go.mod":                           "module example.com/pricing\n",
		"internal/pricing/round.go":        "package pricing\n\nfunc Round() {}\n",
		"internal/pricing/legacy_round.go": "package pricing\n\n// Legacy rounding.\nfunc Legacy() int { return 1 }\n",
		"internal/billing/amount.go":       "package billing\n\n// Amount is money.\ntype Amount int64\n",
	})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "rethink/rounding")
	r.Write(map[string]string{
		"internal/pricing/round.go":    "package pricing\n\nfunc Round() {}\n\nfunc RoundHalfEven() {}\n",
		"internal/pricing/currency.go": "package pricing\n\ntype Currency string\n",
	})
	r.Remove("internal/pricing/legacy_round.go")
	r.Git("mv", "internal/billing/amount.go", "internal/billing/money.go")
	r.Commit("Ada", "rounding")
	r.Write(map[string]string{
		"internal/pricing/round_test.go": "package pricing\n\nimport \"testing\"\n",
		"internal/pricing/round.go":      "package pricing\n\nfunc Round() {}\n\nfunc RoundHalfEven() {}\n\nfunc RoundMinor() {}\n",
		"scratch.sql":                    "select 1;\n",
		"vendor/dep/dep.go":              "package dep\n",
	})
	r.Git("add", "internal/pricing/round_test.go")
	return r
}

func explorerScene(t *testing.T) (*errScene, *gittest.Repo) {
	r := explorerRepo(t)
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.frame()
	return s, r
}

// FG-1: "fixture repos produce the expected markers; ⌥1–3 switch scopes".
// ref: oracle git
func TestExplorerScopes(t *testing.T) {
	s, _ := explorerScene(t)
	if !s.reg.Available("view.toggle", registry.Args{"id": ExplorerView}) {
		t.Fatal("the rail's Explorer entry is still dimmed")
	}
	s.keyCommand(t, "<M-3>")
	s.frame()
	if !s.showing(ExplorerView) {
		t.Fatal("⌥3 didn't open the Explorer")
	}
	all := s.frameText()
	for _, want := range []string{"CHANGED 5", "UNCOMMITTED 3", "ALL 8", "rethink/rounding", "status", "± lines", "diag",
		"money.go", "← amount.go", "legacy_round.go", "currency.go", "round_test.go", "scratch.sql", "vendor", "ignored"} {
		if !strings.Contains(all, want) {
			t.Errorf("All lacks %q:\n%s", want, all)
		}
	}
	s.keyCommand(t, "<M-1>")
	changed := s.frameText()
	for _, want := range []string{"vs main@", "tree", "flat", "pricing", "◉", "●", "○"} {
		if !strings.Contains(changed, want) {
			t.Errorf("Changed lacks %q:\n%s", want, changed)
		}
	}
	if strings.Contains(changed, "scratch.sql") {
		t.Error("Changed lists an untracked file")
	}
	s.keyCommand(t, "<M-2>")
	unc := s.frameText()
	for _, want := range []string{"vs HEAD", "STAGED", "UNSTAGED", "UNTRACKED", "internal/pricing/round_test.go", "s stage · u unstage"} {
		if !strings.Contains(unc, want) {
			t.Errorf("Uncommitted lacks %q:\n%s", want, unc)
		}
	}
}

// The markers in the rows: a modified file's ± lines and letter, a folder's count of changed files under it.
// ref: oracle git
func TestExplorerMarkers(t *testing.T) {
	s, _ := explorerScene(t)
	s.run("explorer.scope", registry.Args{"scope": "all"})
	s.frame()
	s.frame()
	after := func(name string) string {
		lines := strings.Split(s.frameText(), "\n")
		for i, l := range lines {
			if l == name && i+1 < len(lines) {
				return strings.Join(lines[i+1:min(i+4, len(lines))], " ")
			}
		}
		return "(no " + name + ")"
	}
	if got := after("round.go"); !strings.HasPrefix(got, "+4 M") {
		t.Errorf("round.go is followed by %q, want +4 and M", got)
	}
	if got := after("legacy_round.go"); !strings.HasPrefix(got, "-4 D") {
		t.Errorf("legacy_round.go is followed by %q", got)
	}
	if got := after("pricing"); !strings.HasPrefix(got, "● 4") {
		t.Errorf("pricing is followed by %q, want its 4 changed files", got)
	}
	s.click(centre(s.access("± lines").Bounds))
	s.click(centre(s.access("status").Bounds))
	if got := after("round.go"); strings.HasPrefix(got, "+4") || strings.HasPrefix(got, "M") {
		t.Errorf("with ± lines and status off, round.go is followed by %q", got)
	}
}

// ⌥C narrows All to the changed files, and back.
func TestExplorerChangedOnly(t *testing.T) {
	s, _ := explorerScene(t)
	s.run("explorer.scope", registry.Args{"scope": "all"})
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "go.mod") {
		t.Fatal("All doesn't list go.mod")
	}
	s.keyCommand(t, "<M-c>")
	s.frame()
	if strings.Contains(s.frameText(), "go.mod") {
		t.Error("changed-only still lists the unchanged go.mod")
	}
	s.keyCommand(t, "<M-c>")
	s.frame()
	if !strings.Contains(s.frameText(), "go.mod") {
		t.Error("turning changed-only off didn't bring go.mod back")
	}
}

// selectRow focuses the Explorer's tree on the row reading text, by the keyboard from the top row.
func (s *errScene) selectRow(t *testing.T, text string) {
	t.Helper()
	s.keyCommand(t, "g")
	for range 40 {
		if s.explorer().selected != "" && strings.HasSuffix(s.explorer().selected, "\x00"+text) {
			return
		}
		s.keyCommand(t, "j")
	}
	t.Fatalf("j never reached %q (at %q)", text, s.explorer().selected)
}

func (s *errScene) explorer() *explorer {
	for _, e := range s.plugins {
		if x, ok := e.(*explorer); ok {
			return x
		}
	}
	return nil
}

// The fugitive keys in Uncommitted: s stages, s again unstages, u unstages, and x asks before throwing an untracked
// file away.
// ref: oracle git
func TestExplorerStagesFiles(t *testing.T) {
	s, r := explorerScene(t)
	s.run("explorer.scope", registry.Args{"scope": "uncommitted"})
	s.frame()
	s.frame()
	s.click(s.textPoint(t, "STAGED"))
	s.click(s.textPoint(t, "STAGED")) // closed by the first click, open again: the tree has focus
	if m := s.sh.keyModes()[0]; m != ExplorerKeyMode {
		t.Fatalf("the focused Explorer takes keys in %q", m)
	}
	area := func(p string) []gitx.Area {
		var out []gitx.Area
		for _, f := range s.sh.git.state().Files(gitx.Uncommitted) {
			if f.Path == p {
				out = append(out, f.Area)
			}
		}
		return out
	}
	s.selectRow(t, "internal/pricing/round.go")
	s.keyCommand(t, "s")
	s.sh.git.wait()
	if a := area("internal/pricing/round.go"); len(a) != 1 || a[0] != gitx.Staged {
		t.Fatalf("after s, round.go is %v", a)
	}
	s.frame()
	s.selectRow(t, "internal/pricing/round.go")
	s.keyCommand(t, "s")
	s.sh.git.wait()
	if a := area("internal/pricing/round.go"); len(a) != 1 || a[0] != gitx.Unstaged {
		t.Fatalf("s on a staged file didn't unstage it: %v", a)
	}
	s.frame()
	s.selectRow(t, "scratch.sql")
	s.keyCommand(t, "x")
	var ask core.Notification
	for _, n := range s.store.State().Notifications {
		if n.ID == "git.discard" && !n.Dismissed {
			ask = n
		}
	}
	if ask.ID == "" || len(ask.Actions) == 0 {
		t.Fatal("x didn't ask first")
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "scratch.sql")); err != nil {
		t.Fatal("x deleted the file before the answer")
	}
	s.run(ask.Actions[0].Command, registry.Args(ask.Actions[0].Args))
	s.sh.git.wait()
	if _, err := os.Stat(filepath.Join(r.Dir, "scratch.sql")); !os.IsNotExist(err) {
		t.Errorf("Discard left scratch.sql: %v", err)
	}
}

// A click on a file opens it in the editor; in Changed, its branch diff (ui.md 01c, "Opening a file here opens its
// branch diff, not the plain file").
func TestExplorerOpensFiles(t *testing.T) {
	s, r := explorerScene(t)
	s.run("explorer.scope", registry.Args{"scope": "changed"})
	s.frame()
	s.frame()
	s.click(s.textPoint(t, "currency.go"))
	s.frame()
	if st := s.diff().by[r.Dir]; !s.showing(DiffView) || st == nil || st.sel.file != "internal/pricing/currency.go" {
		t.Fatalf("a click on currency.go in Changed didn't open its diff")
	}
	s.run("explorer.scope", registry.Args{"scope": "all"})
	s.frame()
	s.frame()
	s.click(s.textPoint(t, "go.mod"))
	s.frame()
	p, ok := s.editor().e.Pane(s.editor().e.WindowID())
	if !ok || filepath.Join(r.Dir, p.Name()) != filepath.Join(r.Dir, "go.mod") {
		t.Fatalf("a click on go.mod opened %+v", p.Name())
	}
}

// FG-1 (from SH-5): the files picker ranks files changed on the branch above others that match as well, and marks
// them with their status letter.
func TestFilesRankChangedFirst(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a/same.go": "package a\n", "b/same.go": "package b\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "work")
	r.Write(map[string]string{"b/same.go": "package b\n\nvar X = 1\n"})
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	src := mustSource(t, s, "files")
	g, ok := rankSource(s.h(), src, "same", pickerMax)
	if !ok || len(g.hits) != 2 {
		t.Fatalf("hits: %+v", g)
	}
	if first := g.hits[0].item; first.Label != "b/same.go" || !strings.Contains(first.Note, "M") {
		t.Errorf("first %q (note %q), want the changed b/same.go marked M", first.Label, first.Note)
	}
}

// ui.md 01c: "The same markers appear on editor tabs": a changed file's tab carries its status letter.
func TestEditorTabsCarryGitMarks(t *testing.T) {
	s, r := explorerScene(t)
	s.run("editor.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	s.frame()
	s.frame()
	lines := strings.Split(s.frameText(), "\n")
	for i, l := range lines {
		if l == "round.go" && i+1 < len(lines) && lines[i+1] == "M" {
			return
		}
	}
	t.Errorf("the round.go tab isn't marked M:\n%s", s.frameText())
}

// ui.md 01c: "Stage or unstage whole files or single hunks": selecting a file in Uncommitted lists its hunks, and s
// on one stages just it, as git apply --cached would.
// ref: oracle git
func TestExplorerStagesHunks(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"f.txt": gittest.Lines(30)})
	r.Commit("Ada", "base")
	work := strings.Replace(strings.Replace(gittest.Lines(30), "line 3\n", "three\n", 1), "line 25\n", "twenty-five\n", 1)
	r.Write(map[string]string{"f.txt": work})
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.run("explorer.scope", registry.Args{"scope": "uncommitted"})
	s.frame()
	s.frame()
	s.click(s.textPoint(t, "UNSTAGED"))
	s.click(s.textPoint(t, "UNSTAGED"))
	s.selectRow(t, "f.txt")
	s.frame()
	s.explorer().wait()
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "HUNKS IN F.TXT") {
		t.Fatalf("no hunks for f.txt:\n%s", s.frameText())
	}
	for range 10 {
		if strings.HasPrefix(s.explorer().selected, "hunk\x00") {
			break
		}
		s.keyCommand(t, "j")
	}
	s.keyCommand(t, "s")
	s.sh.git.wait()
	want := strings.Replace(gittest.Lines(30), "line 3\n", "three\n", 1)
	if got := r.Git("show", ":f.txt"); got != want {
		t.Errorf("after s on the first hunk the index is:\n%s", got)
	}
}

// The explorer drawn before the repo's status has loaded (a resumed session's first frames) says so, and isn't
// turned off for a panic.
func TestExplorerBeforeTheStatusLoads(t *testing.T) {
	s, r := explorerScene(t)
	s.sh.git.status = gitx.NewStatus(gitx.Repo{Dir: r.Dir})
	s.keyCommand(t, "<M-3>")
	s.frame()
	if off := s.reg.TurnedOff(); len(off) > 0 {
		t.Fatalf("turned off: %+v", off)
	}
	if text := s.frameText(); !strings.Contains(text, "Reading the work tree") {
		t.Errorf("no loading note:\n%s", text)
	}
}

// The explorer scrolls: the wheel over a long tree shows what's below, and the selection doesn't pull it back.
func TestExplorerScrolls(t *testing.T) {
	r := gittest.New(t)
	files := map[string]string{}
	for i := range 300 {
		files[fmt.Sprintf("f%03d.go", i)] = "package p\n"
	}
	r.Write(files)
	r.Commit("Ada", "x")
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.keyCommand(t, "<M-3>")
	s.frame()
	at := render.Point{X: 150, Y: 500}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: 400}})
	s.frame()
	s.frame()
	if text := s.frameText(); strings.Contains(text, "f000.go") || !strings.Contains(text, "f040.go") {
		t.Errorf("the wheel didn't scroll the tree:\n%s", text)
	}
}
