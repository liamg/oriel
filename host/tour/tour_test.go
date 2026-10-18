package tour

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

const mainGo = "package main\n\nfunc main() {\n\tprintln(\"one\")\n\tprintln(\"two\")\n\tprintln(\"three\")\n}\n"

// tourWith is a tour on one repo, api (internal/main.go committed, then changed in the worktree), with steps.
func tourWith(t *testing.T, steps map[string]string) (string, Info, *gittest.Repo) {
	t.Helper()
	r := gittest.New(t)
	r.Write(map[string]string{"internal/main.go": strings.Replace(mainGo, "\"three\"", "\"old\"", 1)})
	r.Commit("Ada", "c1")
	r.Write(map[string]string{"internal/main.go": mainGo})
	r.Commit("Ada", "c2")
	root := t.TempDir()
	for name, body := range steps {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	return root, Info{Name: "how-it-works", Title: "How does it work?", Kind: "walkthrough",
		Repos: []Repo{{Name: "api", Repo: r.Dir, Dir: r.Dir, Base: "main", Commit: head}}}, r
}

func load(t *testing.T, root string, info Info) Tour {
	t.Helper()
	got, err := NewLoader().Load(context.Background(), root, info)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// crit's internal/tour, "a quote with an empty body. The code is read from the project's worktree here, never typed
// by whoever wrote the step, so it is genuinely the code and the line numbers are genuinely its line numbers."
func TestQuoteReadsRealLines(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-start.md": "# Where it starts\n\nThe entry point:\n\n```go repo=api path=internal/main.go lines=3-5 mark=4\n```\n\n```note 4\nThis one matters.\n```\n",
	})
	got := load(t, root, info)
	if len(got.Steps) != 1 {
		t.Fatalf("steps = %d", len(got.Steps))
	}
	s := got.Steps[0]
	if s.Title != "Where it starts" || len(s.Items) != 2 || s.Items[0].Text != "The entry point:" {
		t.Fatalf("step = %+v", s)
	}
	q := s.Items[1].Quote
	if q.Err != "" || q.Start != 3 || q.End != 5 || len(q.Lines) != 3 || q.Lines[1] != "\tprintln(\"one\")" || !q.Marked[4] || s.Items[1].Lang != "go" {
		t.Fatalf("quote = %+v", q)
	}
	if n := q.NotesAt(4); len(n) != 1 || n[0].Text != "This one matters." {
		t.Errorf("notes at 4 = %+v", n)
	}
	if got.Quotes() != 1 {
		t.Errorf("quotes = %d", got.Quotes())
	}
}

// A note names a line, so it sits under that line; one naming a line the quote doesn't carry still shows.
func TestNotesSitUnderTheirLine(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "# T\n\n```go repo=api path=internal/main.go lines=3-6\n```\n\n```note 4\nunder four\n```\n\n```note 6\nunder six\n```\n\n```note 99\nstray\n```\n",
		"02-b.md": "```note\nan aside, with no quote above it\n```\n",
	})
	got := load(t, root, info)
	q := got.Steps[0].Items[0].Quote
	if len(q.NotesAt(4)) != 1 || len(q.NotesAt(6)) != 1 || len(q.NotesAt(5)) != 0 || len(q.Loose) != 1 || q.Loose[0].Text != "stray" {
		t.Errorf("notes %+v, loose %+v", q.Notes, q.Loose)
	}
	if it := got.Steps[1].Items; len(it) != 1 || it[0].Kind != Note || got.Steps[1].Title != "B" {
		t.Errorf("an aside: %+v (%q)", it, got.Steps[1].Title)
	}
	html := string(HTML(got, HTMLOptions{}))
	if a, b, c := strings.Index(html, "under four"), strings.Index(html, `data-n="5"`), strings.Index(html, "under six"); a < 0 || a > b || b > c {
		t.Errorf("notes out of line order in the export: %d %d %d", a, b, c)
	}
}

// A quote reads the pinned worktree, or rev= another revision (an archaeology tour's old code beside the new).
func TestQuoteAtARevision(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "```go repo=api path=internal/main.go lines=6 rev=HEAD~1\n```\n\n```go repo=api path=internal/main.go lines=6\n```\n",
	})
	got := load(t, root, info)
	old, now := got.Steps[0].Items[0].Quote, got.Steps[0].Items[1].Quote
	if old.Err != "" || old.Lines[0] != "\tprintln(\"old\")" || old.Rev != "HEAD~1" {
		t.Errorf("at HEAD~1: %+v", old)
	}
	if now.Lines[0] != "\tprintln(\"three\")" {
		t.Errorf("in the worktree: %+v", now)
	}
}

func TestQuoteFailuresExplainThemselves(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "```go repo=nope path=internal/main.go lines=1-2\n```\n",
		"02-b.md": "```go repo=api path=internal/gone.go lines=1-2\n```\n",
		"03-c.md": "```go repo=api path=../outside.go lines=1-2\n```\n",
		"04-d.md": "```go repo=api path=internal/main.go lines=40-50\n```\n",
	})
	got := load(t, root, info)
	for i, want := range []string{"unknown repo", "not in api at", "leaves the repository", "has only 7 lines"} {
		if q := got.Steps[i].Items[0].Quote; !strings.Contains(q.Err, want) {
			t.Errorf("step %d: %q, want it to say %q", i+1, q.Err, want)
		}
	}
	if !strings.Contains(string(HTML(got, HTMLOptions{})), `tour-quote missing`) {
		t.Error("a quote that can't be read has no frame in the export")
	}
}

// A long quote with marks hides the stretches nobody marked, except under a note.
func TestLongQuotesCollapse(t *testing.T) {
	var src strings.Builder
	for i := range 100 {
		src.WriteString("line " + string(rune('a'+i%26)) + "\n")
	}
	root, info, r := tourWith(t, map[string]string{
		"01-a.md": "```txt repo=api path=long.txt lines=1-100 mark=50\n```\n\n```note 90\nfar away\n```\n",
	})
	r.Write(map[string]string{"long.txt": src.String()})
	q := load(t, root, info).Steps[0].Items[0].Quote
	if !q.Collapsed() || !q.Visible(50) || !q.Visible(46) || q.Visible(45) || !q.Visible(90) || q.Visible(1) {
		t.Errorf("collapsed %v; visible 50 %v, 46 %v, 45 %v, 90 %v", q.Collapsed(), q.Visible(50), q.Visible(46), q.Visible(45), q.Visible(90))
	}
	if q.Hidden() != 100-9-1 {
		t.Errorf("hidden = %d", q.Hidden())
	}
}

func TestExamplesDiagramsAndRuns(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "# Bits\n\n```go\nx := 1\n```\n\n```mermaid caption=\"the flow\"\nflowchart LR\n  a-->b\n```\n\n" +
			"```run cwd=harness cmd=\"go run ./x\" exit=2 at=2026-08-31T10:00:00Z\nboom\n```\n",
	})
	it := load(t, root, info).Steps[0].Items
	if len(it) != 3 || it[0].Kind != Example || it[0].Text != "x := 1" || it[1].Kind != Diagram || it[1].Caption != "the flow" ||
		it[1].Text != "flowchart LR\n  a-->b" || it[2].Kind != Run || it[2].Run.Cmd != "go run ./x" || it[2].Run.Exit != 2 || it[2].Run.Cwd != "harness" ||
		it[2].Text != "boom" || it[2].Run.At.Year() != 2026 {
		t.Errorf("items = %+v", it)
	}
}

// crit's tour skill, "Annotated screenshots": "Every number is a percentage of the image… Pins and arrows are
// numbered in the order you write them… A line that cannot be read is skipped".
func TestAnnotatedScreenshots(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "# Shots\n\n```shot src=assets/dash.png caption=\"where it starts\"\npin 62,18 New tour\nbox 4,52 46,14 the tours you have\n" +
			"arrow 62,22 -> 24,58 it lands here\nnonsense line that should be skipped\npin 5,5\n```\n",
	})
	os.MkdirAll(filepath.Join(root, AssetsDir), 0o755)
	f, _ := os.Create(filepath.Join(root, AssetsDir, "dash.png"))
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 800, 500)))
	f.Close()
	got := load(t, root, info)
	sh := got.Steps[0].Items[0].Shot
	if sh.W != 800 || sh.H != 500 || sh.Path == "" || len(sh.Mark) != 4 {
		t.Fatalf("shot = %+v", sh)
	}
	m := sh.Mark
	if m[0].Kind != Pin || m[0].N != 1 || m[0].Label != "New tour" || m[1].Kind != Box || m[1].N != 0 || m[1].X2 != 46 ||
		m[2].Kind != Arrow || m[2].N != 2 || m[2].X2 != 24 || m[2].Label != "it lands here" || m[3].N != 3 || !m[3].LabelBelow || !m[3].AtLeftEdge {
		t.Errorf("marks = %+v", m)
	}
	html := string(HTML(got, HTMLOptions{}))
	for _, want := range []string{`class="tour-shot"`, `src="data:image/png;base64,`, `class="ts-mark pin"`, "New tour", `class="ts-box"`,
		`viewBox="0 0 800 500"`, "<polygon", "where it starts", `<span class="ts-n">2</span>`} {
		if !strings.Contains(html, want) {
			t.Errorf("the export's shot lacks %q", want)
		}
	}
	if strings.Contains(html, "nonsense") {
		t.Error("an unreadable annotation was drawn")
	}
}

func TestExportIsSelfContained(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "# One\n\nprose with ![a picture](assets/x.png)\n\n```go repo=api path=internal/main.go lines=1-3\n```\n",
		"02-b.md": "# Two\n\nmore prose\n",
	})
	os.MkdirAll(filepath.Join(root, AssetsDir), 0o755)
	os.WriteFile(filepath.Join(root, AssetsDir, "x.png"), []byte("\x89PNG"), 0o644)
	got := load(t, root, info)
	html := string(HTML(got, HTMLOptions{Highlight: func(path, lang string, src []byte, start, end int) [][]Span {
		if !strings.HasSuffix(path, "internal/main.go") || start != 1 || end != 3 || lang != "go" {
			t.Errorf("highlight asked for %s %s %d-%d", path, lang, start, end)
		}
		return [][]Span{{{Text: "package", Class: "hl-keyword"}, {Text: " main"}}}
	}}))
	for _, want := range []string{"<!doctype html>", "How does it work?", "Step 1 of 2", "Step 2 of 2", `<span class="hl-keyword">package</span> main`,
		"tx-next", "Present", `data:image/png;base64,`, "api", "main " + info.Repos[0].Commit[:7]} {
		if !strings.Contains(html, want) {
			t.Errorf("the export lacks %q", want)
		}
	}
	for _, bad := range []string{`src="/`, `href="/`, `src="http`, "//cdn.", "<script src", "<link"} {
		if strings.Contains(html, bad) {
			t.Errorf("the export refers to something outside it (%q)", bad)
		}
	}
}

// crit's export: "No data-theme on the root: the page follows the reader's own preference, and whatever host it is
// published on may stamp data-theme itself to switch it."
func TestExportFollowsTheReadersTheme(t *testing.T) {
	p := Palette(Colors{Vars: map[string]string{"bg": "#000"}, Code: map[string]string{"hl-keyword": "color: #f00"}},
		Colors{Vars: map[string]string{"bg": "#fff"}, Code: map[string]string{"hl-keyword": "color: #900"}})
	root, info, _ := tourWith(t, map[string]string{"01-a.md": "# One\n\nprose\n"})
	html := string(HTML(load(t, root, info), HTMLOptions{Palette: p}))
	if strings.Contains(html, `<html lang="en" data-theme=`) {
		t.Error("the page pins a theme")
	}
	for _, want := range []string{"@media (prefers-color-scheme: light)", `:root[data-theme="dark"] {`, `:root[data-theme="light"] {`,
		`:root[data-theme="dark"] .hl-keyword { color: #f00 }`, `:root[data-theme="light"] .hl-keyword { color: #900 }`, "--bg: #000;"} {
		if !strings.Contains(html, want) {
			t.Errorf("the palette lacks %q", want)
		}
	}
	if strings.Index(html, "@media (prefers-color-scheme: light)") > strings.Index(html, `:root[data-theme="light"] {`) {
		t.Error("the data-theme rules come before the media query, so the host's switch can't win")
	}
}

func TestExportMarkdownResolvesQuotes(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "# One\n\n## A detail\n\n```go repo=api path=internal/main.go lines=1-2\n```\n\n```note 1\nthe package\n```\n\n" +
			"```shot src=assets/x.png caption=\"look\"\npin 5,5 here\nbox 1,1 2,2 that\n```\n",
		"02-b.md": "# Two\n",
	})
	md := string(Markdown(load(t, root, info)))
	for _, want := range []string{"# How does it work?\n", "_walkthrough tour · api @ main ", "## One\n", "### A detail", "**api · `internal/main.go` · 1-2**",
		"```go\npackage main\n\n```", "> **line 1**: the package", "![look](assets/x.png)", "1. here", "- that", "---\n\n## Two"} {
		if !strings.Contains(md, want) {
			t.Errorf("the markdown lacks %q:\n%s", want, md)
		}
	}
}

func TestProseIsEscaped(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{"01-a.md": "# Safe\n\n<script>alert(1)</script> and [a](javascript:alert(2))\n"})
	html := string(HTML(load(t, root, info), HTMLOptions{}))
	if strings.Contains(html, "<script>alert") || strings.Contains(html, "javascript:") {
		t.Error("raw HTML or a javascript: link reached the page")
	}
}

// crit's markdown: "A wrapped bullet is still one bullet", and the numbering of a list after prose comes from the
// source.
func TestWrappedProseAndBullets(t *testing.T) {
	root, info, _ := tourWith(t, map[string]string{
		"01-a.md": "# T\n\nOne sentence that runs\nover two lines.\n\n- a bullet that runs\n  over two lines\n- a second bullet\n\nThen *emphasis* and **strong**.\n\n3. third\n",
	})
	html := string(HTML(load(t, root, info), HTMLOptions{}))
	for _, want := range []string{"over two lines</li>", "<em>emphasis</em>", "<strong>strong</strong>", `<ol start="3">`} {
		if !strings.Contains(html, want) {
			t.Errorf("lacks %q", want)
		}
	}
	if strings.Count(html, "<li>") != 3 || strings.Contains(html, "runs<br>") {
		t.Error("a wrapped bullet or paragraph broke")
	}
}

func TestRunStaysInsideTheTour(t *testing.T) {
	root, _, _ := tourWith(t, nil)
	ctx := context.Background()
	if _, err := RunCommand(ctx, root, "../..", []string{"echo", "hi"}, 0); err == nil {
		t.Error("a cwd outside the tour ran")
	}
	res, err := RunCommand(ctx, root, "", []string{"sh", "-c", "echo out; echo $ORIEL_TOUR_ROOT; exit 3"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 3 || !strings.Contains(res.Output, "out\n"+root) {
		t.Errorf("run = %+v", res)
	}
	fence := res.Fence()
	if !strings.HasPrefix(fence, "```run cmd=\"sh -c echo out; echo $ORIEL_TOUR_ROOT; exit 3\" exit=3 at=") || !strings.Contains(fence, "out\n") {
		t.Errorf("fence = %q", fence)
	}
	blocks := Parse(fence)
	if len(blocks) != 1 || blocks[0].Kind != Run || blocks[0].intAttr("exit") != 3 {
		t.Errorf("the fence reads back as %+v", blocks)
	}
	if _, err := RunCommand(ctx, root, "", []string{"no-such-binary-anywhere"}, 0); err == nil {
		t.Error("a command that can't start isn't an error")
	}
}

// Output with a fence in it can't close the run block early.
func TestRunFenceHoldsBackticks(t *testing.T) {
	r := RunResult{Cmd: "cat x.md", Output: "a\n```\nb\n"}
	b := Parse(r.Fence())
	if len(b) != 1 || b[0].Body != "a\n```\nb" {
		t.Errorf("read back as %+v", b)
	}
}

func TestAppendToStepByNumber(t *testing.T) {
	root, _, _ := tourWith(t, map[string]string{"03-c.md": "# Three\n"})
	if err := Append(root, "3", "```run cmd=x\nout\n```\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "03-c.md"))
	if string(b) != "# Three\n\n```run cmd=x\nout\n```\n" {
		t.Errorf("step = %q", b)
	}
	if err := Append(root, "9", "x"); err == nil {
		t.Error("appended to a step that isn't there")
	}
}

func TestStepFilesAreOrderedAndFiltered(t *testing.T) {
	root, _, _ := tourWith(t, map[string]string{"02-b.md": "", "01-a.md": "", "10-j.md": "", "NOTES.md": "", "assets.md": "", "tour.md": ""})
	got, _ := StepFiles(root)
	if strings.Join(got, ",") != "01-a.md,02-b.md,10-j.md" {
		t.Errorf("steps = %v", got)
	}
	if MatchStep(root, "01") != "01-a.md" || MatchStep(root, "10-j.md") != "10-j.md" || MatchStep(root, "4") != "" {
		t.Error("MatchStep")
	}
}

func TestAssetPathStaysInside(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, AssetsDir), 0o755)
	os.WriteFile(filepath.Join(root, AssetsDir, "a.png"), nil, 0o644)
	if _, err := AssetPath(root, "assets/a.png"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", "/etc/passwd", "../x.png", "assets/../../x.png", "assets"} {
		if _, err := AssetPath(root, bad); err == nil {
			t.Errorf("%q resolved", bad)
		}
	}
}

// crit's tourkinds test: every kind in the menu has guidance, and every guidance file is a kind.
func TestEveryKindHasGuidance(t *testing.T) {
	if len(Kinds) != 7 || Kinds[0].ID != DefaultKind {
		t.Fatalf("kinds = %+v", Kinds)
	}
	for _, k := range Kinds {
		if Guidance(k.ID) == "" || !ValidKind(k.ID) {
			t.Errorf("%s has no guidance", k.ID)
		}
	}
	ents, _ := guidance.ReadDir("kinds")
	for _, e := range ents {
		if !ValidKind(strings.TrimSuffix(e.Name(), ".md")) {
			t.Errorf("%s isn't a kind", e.Name())
		}
	}
	if ValidKind("nonsense") || Guidance("nonsense") != "" {
		t.Error("nonsense is a kind")
	}
}
