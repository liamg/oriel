package gitui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/gitui"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

func explorerRepo(t *testing.T) *gittest.Repo { return apptest.ExplorerRepo(t) }

func explorerScene(t *testing.T) (*apptest.Scene, *gittest.Repo) { return apptest.ExplorerScene(t) }

func gitOf(s *apptest.Scene) *gitui.Git   { return apptest.PluginOf[*gitui.Git](s, "git") }
func diffOf(s *apptest.Scene) *gitui.Diff { return apptest.PluginOf[*gitui.Diff](s, "diff") }
func explorerOf(s *apptest.Scene) *gitui.Explorer {
	return apptest.PluginOf[*gitui.Explorer](s, "explorer")
}

// Fixture repos produce the expected markers; ⌥1–3 switch scopes.
// ref: oracle git
func TestExplorerScopes(t *testing.T) {
	s, _ := explorerScene(t)
	if !s.Registry().Available("view.toggle", registry.Args{"id": kit.ExplorerView}) {
		t.Fatal("the rail's Explorer entry is still dimmed")
	}
	s.Press("<M-3>")
	s.Frame()
	if !s.Showing(kit.ExplorerView) {
		t.Fatal("⌥3 didn't open the Explorer")
	}
	all := s.Text()
	for _, want := range []string{"CHANGED 5", "UNCOMMITTED 3", "ALL 8", "rethink/rounding",
		"money.go", "← amount.go", "legacy_round.go", "currency.go", "round_test.go", "scratch.sql", "vendor", "ignored"} {
		if !strings.Contains(all, want) {
			t.Errorf("All lacks %q:\n%s", want, all)
		}
	}
	s.Press("<M-1>")
	changed := s.Text()
	for _, want := range []string{"vs main@", "tree", "flat", "pricing", "◉", "●", "○"} {
		if !strings.Contains(changed, want) {
			t.Errorf("Changed lacks %q:\n%s", want, changed)
		}
	}
	if strings.Contains(changed, "scratch.sql") {
		t.Error("Changed lists an untracked file")
	}
	s.Press("<M-2>")
	unc := s.Text()
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
	s.Run("explorer.scope", registry.Args{"scope": "all"})
	s.Frame()
	s.Frame()
	after := func(name string) string {
		lines := strings.Split(s.Text(), "\n")
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
	s.Click(apptest.Centre(s.Node("Explorer options").Bounds), 0)
	s.Click(apptest.Centre(s.Node("± lines").Bounds), 0)
	s.Click(apptest.Centre(s.Node("Status").Bounds), 0)
	if got := after("round.go"); strings.HasPrefix(got, "+4") || strings.HasPrefix(got, "M") {
		t.Errorf("with ± lines and status off, round.go is followed by %q", got)
	}
}

// ⌥C narrows All to the changed files, and back.
func TestExplorerChangedOnly(t *testing.T) {
	s, _ := explorerScene(t)
	s.Run("explorer.scope", registry.Args{"scope": "all"})
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "go.mod") {
		t.Fatal("All doesn't list go.mod")
	}
	s.Press("<M-c>")
	s.Frame()
	if strings.Contains(s.Text(), "go.mod") {
		t.Error("changed-only still lists the unchanged go.mod")
	}
	s.Press("<M-c>")
	s.Frame()
	if !strings.Contains(s.Text(), "go.mod") {
		t.Error("turning changed-only off didn't bring go.mod back")
	}
}

// All leaves out dot files that haven't changed until the options menu's Hidden files shows them.
func TestExplorerHiddenFiles(t *testing.T) {
	r := explorerRepo(t)
	r.Write(map[string]string{".env.example": "KEY=\n"})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	gitOf(s).Wait()
	s.Run("explorer.scope", registry.Args{"scope": "all"})
	s.Frame()
	s.Frame()
	all := s.Text()
	if strings.Contains(all, ".gitignore") {
		t.Errorf("All lists the unchanged .gitignore before Hidden files is on:\n%s", all)
	}
	if !strings.Contains(all, ".env.example") {
		t.Errorf("All hides the untracked .env.example:\n%s", all)
	}
	s.Click(apptest.Centre(s.Node("Explorer options").Bounds), 0)
	s.Click(apptest.Centre(s.Node("Hidden files").Bounds), 0)
	s.Frame()
	if !strings.Contains(s.Text(), ".gitignore") {
		t.Error("Hidden files didn't bring .gitignore in")
	}
}

// selectRow focuses the Explorer's tree on the row reading text, by the keyboard from the top row.
func selectRow(t *testing.T, s *apptest.Scene, text string) {
	t.Helper()
	s.Press("g")
	for range 40 {
		if explorerOf(s).Selected() != "" && strings.HasSuffix(explorerOf(s).Selected(), "\x00"+text) {
			return
		}
		s.Press("j")
	}
	t.Fatalf("j never reached %q (at %q)", text, explorerOf(s).Selected())
}

// The fugitive keys in Uncommitted: s stages, s again unstages, u unstages, and x asks before throwing an untracked
// file away.
// ref: oracle git
func TestExplorerStagesFiles(t *testing.T) {
	s, r := explorerScene(t)
	s.Run("explorer.scope", registry.Args{"scope": "uncommitted"})
	s.Frame()
	s.Frame()
	s.Click(s.TextPoint("STAGED"), 0)
	s.Click(s.TextPoint("STAGED"), 0) // closed by the first click, open again: the tree has focus
	if m := s.KeyModes()[0]; m != gitui.ExplorerKeyMode {
		t.Fatalf("the focused Explorer takes keys in %q", m)
	}
	area := func(p string) []gitx.Area {
		var out []gitx.Area
		for _, f := range gitOf(s).State().Files(gitx.Uncommitted) {
			if f.Path == p {
				out = append(out, f.Area)
			}
		}
		return out
	}
	selectRow(t, s, "internal/pricing/round.go")
	s.Press("s")
	gitOf(s).Wait()
	if a := area("internal/pricing/round.go"); len(a) != 1 || a[0] != gitx.Staged {
		t.Fatalf("after s, round.go is %v", a)
	}
	s.Frame()
	selectRow(t, s, "internal/pricing/round.go")
	s.Press("s")
	gitOf(s).Wait()
	if a := area("internal/pricing/round.go"); len(a) != 1 || a[0] != gitx.Unstaged {
		t.Fatalf("s on a staged file didn't unstage it: %v", a)
	}
	s.Frame()
	selectRow(t, s, "scratch.sql")
	s.Press("x")
	var ask core.Notification
	for _, n := range s.Store().State().Notifications {
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
	s.Run(ask.Actions[0].Command, registry.Args(ask.Actions[0].Args))
	gitOf(s).Wait()
	if _, err := os.Stat(filepath.Join(r.Dir, "scratch.sql")); !os.IsNotExist(err) {
		t.Errorf("Discard left scratch.sql: %v", err)
	}
}

// A click on a file opens it in the editor; in Changed, its branch diff, not the plain file.
func TestExplorerOpensFiles(t *testing.T) {
	s, r := explorerScene(t)
	s.Run("explorer.scope", registry.Args{"scope": "changed"})
	s.Frame()
	s.Frame()
	s.Click(s.TextPoint("currency.go"), 0)
	s.Frame()
	if file, ok := diffOf(s).SelectedFile(r.Dir); !s.Showing(gitui.DiffView) || !ok || file != "internal/pricing/currency.go" {
		t.Fatalf("a click on currency.go in Changed didn't open its diff")
	}
	s.Run("explorer.scope", registry.Args{"scope": "all"})
	s.Frame()
	s.Frame()
	s.Click(s.TextPoint("go.mod"), 0)
	s.Frame()
	eds, _ := registry.EditorsOf(s.Registry())
	b, ok := eds.Current()
	if !ok || b.Path != filepath.Join(r.Dir, "go.mod") {
		t.Fatalf("a click on go.mod opened %+v", b.Path)
	}
}

// The explorer's markers appear on editor tabs too: a changed file's tab carries its status letter.
func TestEditorTabsCarryGitMarks(t *testing.T) {
	s, r := explorerScene(t)
	s.Run("editor.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	s.Frame()
	s.Frame()
	lines := strings.Split(s.Text(), "\n")
	for i, l := range lines {
		if l == "round.go" && i+1 < len(lines) && lines[i+1] == "M" {
			return
		}
	}
	t.Errorf("the round.go tab isn't marked M:\n%s", s.Text())
}

// Stage or unstage whole files or single hunks: selecting a file in Uncommitted lists its hunks, and s
// on one stages just it, as git apply --cached would.
// ref: oracle git
func TestExplorerStagesHunks(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"f.txt": gittest.Lines(30)})
	r.Commit("Ada", "base")
	work := strings.Replace(strings.Replace(gittest.Lines(30), "line 3\n", "three\n", 1), "line 25\n", "twenty-five\n", 1)
	r.Write(map[string]string{"f.txt": work})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	gitOf(s).Wait()
	s.Run("explorer.scope", registry.Args{"scope": "uncommitted"})
	s.Frame()
	s.Frame()
	s.Click(s.TextPoint("UNSTAGED"), 0)
	s.Click(s.TextPoint("UNSTAGED"), 0)
	selectRow(t, s, "f.txt")
	s.Frame()
	explorerOf(s).Wait()
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "HUNKS IN F.TXT") {
		t.Fatalf("no hunks for f.txt:\n%s", s.Text())
	}
	for range 10 {
		if strings.HasPrefix(explorerOf(s).Selected(), "hunk\x00") {
			break
		}
		s.Press("j")
	}
	s.Press("s")
	gitOf(s).Wait()
	want := strings.Replace(gittest.Lines(30), "line 3\n", "three\n", 1)
	if got := r.Git("show", ":f.txt"); got != want {
		t.Errorf("after s on the first hunk the index is:\n%s", got)
	}
}

// Selecting another file in Uncommitted lists that file's hunks, read again, not the last file's.
func TestExplorerHunksFollowTheSelection(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": gittest.Lines(30), "b.txt": gittest.Lines(30)})
	r.Commit("Ada", "base")
	r.Write(map[string]string{
		"a.txt": strings.Replace(gittest.Lines(30), "line 3\n", "three\n", 1),
		"b.txt": strings.Replace(gittest.Lines(30), "line 25\n", "twenty-five\n", 1),
	})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	gitOf(s).Wait()
	s.Run("explorer.scope", registry.Args{"scope": "uncommitted"})
	s.Frame()
	s.Frame()
	s.Click(s.TextPoint("UNSTAGED"), 0)
	s.Click(s.TextPoint("UNSTAGED"), 0)
	shown := func(name string) []gitx.Hunk {
		selectRow(t, s, name)
		for range 3 {
			s.Frame()
			explorerOf(s).Wait()
		}
		path, loaded, unstaged := explorerOf(s).Hunks()
		if path != name || !loaded {
			t.Fatalf("selecting %s: hunks of %q, loaded %v", name, path, loaded)
		}
		return unstaged
	}
	if hs := shown("a.txt"); len(hs) != 1 || hs[0].NewStart != 3 {
		t.Fatalf("a.txt's hunks: %+v", hs)
	}
	if hs := shown("b.txt"); len(hs) != 1 || hs[0].NewStart != 25 {
		t.Errorf("b.txt's hunks: %+v", hs)
	}
}

// The explorer drawn before the repo's status has loaded (a resumed session's first frames) says so, and isn't
// turned off for a panic.
func TestExplorerBeforeTheStatusLoads(t *testing.T) {
	s, r := explorerScene(t)
	gitOf(s).Unread(r.Dir)
	s.Press("<M-3>")
	s.Frame()
	if off := s.Registry().TurnedOff(); len(off) > 0 {
		t.Fatalf("turned off: %+v", off)
	}
	s.Now = s.Now.Add(time.Second) // past the loading line's delay
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "Reading the work tree…") {
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
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir})
	gitOf(s).Wait()
	s.Press("<M-3>")
	s.Frame()
	at := render.Point{X: 150, Y: 500}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: 400}})
	s.Frame()
	s.Frame()
	if text := s.Text(); strings.Contains(text, "f000.go") || !strings.Contains(text, "f040.go") {
		t.Errorf("the wheel didn't scroll the tree:\n%s", text)
	}
}
