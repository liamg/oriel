package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/tools/perf"
	"github.com/liamg/oriel/ui"
)

// overviewNow is the fixed clock the overview scenes are drawn at.
var overviewNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// gitRepo makes a repo at dir with files, and commits by authors on fixed days before overviewNow.
func gitRepo(t testing.TB, dir string, files map[string]string, commits int) {
	t.Helper()
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}, env...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(dir, 0o755)
	run(nil, "init", "-q", "-b", "fix/stale-prices")
	for name, body := range files {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	run(nil, "add", ".")
	authors := []string{"Liam Galvin", "Claude", "Alex Rivera", "Sam Kim", "Jo Park"}
	for i := range commits {
		who := authors[(i*i+i/3)%len(authors)]
		at := overviewNow.Add(-time.Duration((i*37)%360) * 24 * time.Hour).Add(-time.Duration(i) * time.Hour).Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_NAME=" + who, "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=" + who, "GIT_COMMITTER_EMAIL=a@x",
			"GIT_AUTHOR_DATE=" + at, "GIT_COMMITTER_DATE=" + at}, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("Change %d", commits-i))
	}
}

func lines(n int, line string) string { return strings.Repeat(line+"\n", n) }

// overviewScene is the window with the directory make makes in home open, and the overview's panels all computed.
func overviewScene(t *testing.T, make func(home string) string) *errScene {
	t.Helper()
	s := newScene(t, "", "", mockupSize)
	s.overviewOff.Dispose()
	s.sh.cfg.WorkDir = make(os.Getenv("HOME"))
	for _, e := range s.plugins {
		if o, ok := e.(*overview); ok {
			o.now = func() time.Time { return overviewNow }
		}
	}
	s.reg.Language(registry.Language{Name: "Go", Extensions: []string{".go"}})
	s.reg.Language(registry.Language{Name: "SQL", Extensions: []string{".sql"}})
	s.reg.Language(registry.Language{Name: "YAML", Extensions: []string{".yaml"}})
	s.reg.Language(registry.Language{Name: "Markdown", Extensions: []string{".md"}})
	deadline := time.Now().Add(20 * time.Second)
	for {
		s.sh.runTodo()
		s.frame()
		if s.overviewDone() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the overview never finished:\n%s", s.frameText())
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.frame()
	return s
}

func (s *errScene) overviewDone() bool {
	for _, e := range s.plugins {
		if o, ok := e.(*overview); ok {
			for _, cd := range o.cards {
				if cd.git && cd.header == nil {
					return false
				}
				for _, p := range cd.panels {
					if !p.done {
						return false
					}
				}
			}
			return len(o.cards) > 0
		}
	}
	return false
}

// FG-7: a repo's card against 40-overview: languages, commit activity, contributors and the branch.
func TestOverviewSnapshot(t *testing.T) {
	s := overviewScene(t, func(home string) string {
		dir := filepath.Join(home, "src", "usage")
		gitRepo(t, dir, map[string]string{
			"internal/usage/usage.go": lines(610, "\tx := price(plan)"),
			"internal/usage/cache.go": lines(300, "\treturn c.get(k)"),
			"db/schema.sql":           lines(170, "create table t (id int);"),
			"deploy/app.yaml":         lines(90, "key: value"),
			"README.md":               lines(70, "Some words."),
			"Makefile":                lines(60, "all:"),
		}, 60)
		return dir
	})
	text := s.frameText()
	for _, want := range []string{"usage", "~/src/usage", "fix/stale-prices", "1,300", "Go", "70%", "SQL", "commits in the last year",
		"Change 1", "Liam Galvin", "+ 1 more · 5 people"} {
		if !strings.Contains(text, want) {
			t.Errorf("the overview doesn't show %q:\n%s", want, text)
		}
	}
	s.golden("overview")
	img := raster.Render(s.list, s.size, 1, s.ui.Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../_design/mockups/40-overview.png")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := snapshot.Compare(mock, img, 0.1)
	t.Logf("overview against 40-overview: %s", d)
	if d.Mean > 0.06 {
		t.Errorf("the overview is %s off 40-overview", d)
	}
}

// FG-7: a plain directory's card has languages, size and its largest files, and no git panels.
func TestOverviewPlainDirectory(t *testing.T) {
	s := overviewScene(t, func(home string) string {
		dir := filepath.Join(home, "notes")
		os.MkdirAll(filepath.Join(dir, "2026"), 0o755)
		os.WriteFile(filepath.Join(dir, "todo.md"), []byte(lines(40, "- a thing")), 0o644)
		os.WriteFile(filepath.Join(dir, "2026", "oct.md"), []byte(lines(12, "words")), 0o644)
		return dir
	})
	text := s.frameText()
	for _, want := range []string{"LANGUAGES", "52", "Markdown", "SIZE", "LARGEST FILES", "todo.md"} {
		if !strings.Contains(text, want) {
			t.Errorf("the overview doesn't show %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"COMMIT ACTIVITY", "CONTRIBUTORS"} {
		if strings.Contains(text, not) {
			t.Errorf("a plain directory shows %q", not)
		}
	}
	s.golden("overview-plain")
}

// FG-7: a plugin's panel computes off the loop and shows on the card; closing the last tab brings the overview back.
func TestOverviewPluginPanelAndReturn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	s := newScene(t, "", dir, mockupSize)
	s.overviewOff.Dispose()
	s.reg.Contribute(registry.OverviewPanel{Name: "todos", Title: "TODOs", Order: 90, Dirs: true, Repos: true,
		Compute: func(registry.Ctx, string) (ui.Node, error) { return ui.TextNode("3 todos"), nil }})
	s.run("editor.open", registry.Args{"path": "a.txt"})
	s.frame()
	if strings.Contains(s.frameText(), "TODOS") {
		t.Fatal("the overview shows over an open file")
	}
	s.run("view.close", nil)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.frameText(), "3 todos") {
		if time.Now().After(deadline) {
			t.Fatalf("no plugin panel:\n%s", s.frameText())
		}
		s.sh.runTodo()
		s.frame()
		time.Sleep(5 * time.Millisecond)
	}
}

// FG-7: on a 100k-file repo the overview shows at once, and frames stay under a frame's budget while it counts.
func TestOverviewBigRepoBudget(t *testing.T) {
	perf.Timed(t)
	if testing.Short() {
		t.Skip("makes 100,000 files")
	}
	dir := filepath.Join(t.TempDir(), "big")
	files := map[string]string{}
	for i := range 100_000 {
		files[fmt.Sprintf("pkg%03d/f%05d.go", i%500, i)] = "package p\n\nfunc f() {}\n"
	}
	gitRepo(t, dir, files, 3)
	s := newScene(t, "", dir, mockupSize)
	s.overviewOff.Dispose()
	var times []time.Duration
	deadline := time.Now().Add(panelBudget + 5*time.Second)
	for !s.overviewDone() {
		start := time.Now()
		s.sh.runTodo()
		s.frame()
		times = append(times, time.Since(start))
		if time.Now().After(deadline) {
			t.Fatalf("not done within the panels' budget:\n%s", s.frameText())
		}
		time.Sleep(2 * time.Millisecond)
	}
	slices.Sort(times)
	p95, worst := times[len(times)*95/100], times[len(times)-1]
	t.Logf("frames while counting: %d, p95 %v, slowest %v", len(times), p95, worst)
	// The counting runs beside the loop on every core, so one frame the scheduler held up is noise; counting on
	// the loop would hold up most of them, and one for far longer.
	if p95 > 16*time.Millisecond || worst > 50*time.Millisecond {
		t.Errorf("frames took p95 %v, slowest %v, while the overview counted; the budget is 16 ms", p95, worst)
	}
	if !strings.Contains(s.frameText(), "100,000") {
		t.Errorf("doesn't count 100,000 files:\n%s", s.frameText())
	}
}

// FG-7: a Lua panel's rows show on the card.
func TestOverviewLuaPanel(t *testing.T) {
	s := overviewScene(t, func(home string) string {
		dir := filepath.Join(home, "proj")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
		return dir
	})
	s.writeConfig(t, map[string]string{"init.lua": `oriel.overview.add{ name = "todo", title = "To do", order = 90,
  compute = function(dir) return { { label = "open", value = 3 } } end }`})
	s.lua().reload(false)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.frameText(), "open\n  3") {
		if time.Now().After(deadline) {
			t.Fatalf("no Lua panel:\n%s", s.frameText())
		}
		s.sh.runTodo()
		s.frame()
		time.Sleep(5 * time.Millisecond)
	}
}
