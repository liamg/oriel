package packs

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

var kinds = []core.Kind{
	core.KindWork, core.KindPlan, core.KindReview,
	core.KindJob, core.KindNewRepo, core.KindDirect,
}

// builtins is how many packs are built in: a kind's each, and ai-review, which the AI review's reviewer runs on.
var builtins = len(kinds) + 1

func TestEveryKindHasAPack(t *testing.T) {
	for _, k := range kinds {
		p, ok := Get(string(k))
		if !ok {
			t.Errorf("no pack for %q", k)
			continue
		}
		if p.Name != string(k) || p.Skill != string(k) {
			t.Errorf("%q: name %q, skill %q", k, p.Name, p.Skill)
		}
		if strings.TrimSpace(p.Text) == "" || len(p.Tools) == 0 {
			t.Errorf("%q: empty text or tools", k)
		}
	}
	if got := len((&Set{}).All()); got != builtins {
		t.Errorf("(&Set{}).All() has %d packs, want %d", got, builtins)
	}
}

func TestFrontmatterParses(t *testing.T) {
	for _, p := range (&Set{}).All() {
		m, body, err := Parse(p.Text)
		if err != nil {
			t.Errorf("%s: %v", p.Name, err)
			continue
		}
		if m.Name != p.Name || m.Description == "" {
			t.Errorf("%s: frontmatter name %q, description %q", p.Name, m.Name, m.Description)
		}
		if !slices.Equal(m.Tools, p.Tools) {
			t.Errorf("%s: frontmatter tools %v, pack tools %v", p.Name, m.Tools, p.Tools)
		}
		if strings.HasPrefix(body, "---") || !strings.HasPrefix(strings.TrimSpace(body), "# ") {
			t.Errorf("%s: body should start at the title, got %.40q", p.Name, body)
		}
		if strings.Contains(p.Text, "<!-- include:") {
			t.Errorf("%s: unexpanded include", p.Name)
		}
	}
}

func TestPacksNameOnlyTheirTools(t *testing.T) {
	for _, p := range (&Set{}).All() {
		for _, tool := range p.Tools {
			if !slices.Contains(Tools, tool) {
				t.Errorf("%s: Tools lists %q, which Oriel doesn't offer", p.Name, tool)
			}
		}
		for _, tool := range mentions(p.Text) {
			if !slices.Contains(p.Tools, tool) {
				t.Errorf("%s: text names mcp__oriel__%s, missing from its Tools", p.Name, tool)
			}
		}
		for _, m := range regexp.MustCompile(`mcp__oriel-gw__([A-Za-z0-9_]+)`).FindAllStringSubmatch(p.Text, -1) {
			if !slices.Contains(GatewayTools, m[1]) {
				t.Errorf("%s: text names gateway tool %q, not in GatewayTools", p.Name, m[1])
			}
		}
	}
}

func TestReviewChangesNothing(t *testing.T) {
	for _, name := range []string{"review"} {
		p, _ := Get(name)
		for _, tool := range []string{"commit_draft", "pr_draft", "test_run"} {
			if slices.Contains(p.Tools, tool) {
				t.Errorf("%s offers %s", name, tool)
			}
		}
	}
	p, _ := Get("review")
	if !slices.Contains(p.Tools, "comment") || !strings.Contains(p.Text, "severity") {
		t.Error("review should post findings with a severity through comment")
	}
}

func TestDirectWarns(t *testing.T) {
	p, _ := Get("direct")
	if !strings.Contains(p.Text, "actual checkout") || !strings.Contains(p.Text, "no worktree") {
		t.Error("direct pack doesn't warn that it edits the user's real checkout")
	}
}

func TestNoCrit(t *testing.T) {
	word := regexp.MustCompile(`(?i)\bcrit\b`)
	for _, p := range (&Set{}).All() {
		if loc := word.FindStringIndex(p.Text); loc != nil {
			t.Errorf("%s mentions crit: %q", p.Name, p.Text[max(0, loc[0]-30):min(len(p.Text), loc[1]+30)])
		}
	}
}

func TestRegisterShadows(t *testing.T) {
	set := &Set{}
	builtin, _ := set.Get("work")
	un1 := set.Register(agent.Pack{Name: "work", Text: "one", Tools: []string{"reply"}})
	un2 := set.Register(agent.Pack{Name: "work", Text: "two"})
	if p, _ := set.Get("work"); p.Text != "two" || p.Skill != "work" {
		t.Fatalf("got %q / %q, want the latest registration", p.Text, p.Skill)
	}
	un2()
	if p, _ := set.Get("work"); p.Text != "one" {
		t.Fatalf("after unregistering the latest, got %q", p.Text)
	}
	un1()
	un1()
	if p, _ := set.Get("work"); p.Text != builtin.Text {
		t.Fatal("unregistering every override didn't restore the built-in")
	}

	un := set.Register(agent.Pack{Name: "incident", Skill: "incident", Text: "x"})
	if _, ok := set.Get("incident"); !ok || len(set.All()) != builtins+1 {
		t.Fatal("a plugin's new kind isn't listed")
	}
	un()
	if _, ok := set.Get("incident"); ok {
		t.Fatal("unregistered kind still listed")
	}
}

func TestGetReturnsACopy(t *testing.T) {
	p, _ := Get("work")
	p.Tools[0] = "changed"
	if q, _ := Get("work"); q.Tools[0] == "changed" {
		t.Fatal("Get shares the pack's Tools slice")
	}
}

func TestLoadDirOverrides(t *testing.T) {
	set := &Set{}
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("work.md", "---\nname: work\ndescription: mine\n---\n\n# Mine\n\nUse `mcp__oriel__reply`.\n\n<!-- include: tasks -->\n")
	write("incident.md", "---\ndescription: an incident\ntools: reply, resolve\n---\n\n# Incident\n")
	write("notes.txt", "ignored")

	un, err := set.LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := set.Get("work")
	if !strings.Contains(p.Text, "# Mine") || !strings.Contains(p.Text, "mcp__oriel__task_add") {
		t.Errorf("override not loaded or include not expanded: %q", p.Text)
	}
	if want := []string{"reply", "task_add", "task_update", "tasks"}; !slices.Equal(p.Tools, want) {
		t.Errorf("tools from mentions: got %v, want %v", p.Tools, want)
	}
	inc, ok := set.Get("incident")
	if !ok || inc.Skill != "incident" || !slices.Equal(inc.Tools, []string{"reply", "resolve"}) {
		t.Errorf("incident: %+v", inc)
	}
	un()
	if p, _ := set.Get("work"); strings.Contains(p.Text, "# Mine") {
		t.Error("unregister didn't restore the built-in")
	}
	if _, ok := set.Get("incident"); ok {
		t.Error("incident still registered")
	}
}

func TestLoadDirMissingIsEmpty(t *testing.T) {
	set := &Set{}
	un, err := set.LoadDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	un()
}

func TestLoadDirRejectsBadFiles(t *testing.T) {
	set := &Set{}
	for name, src := range map[string]string{
		"unclosed": "---\nname: x\n# x\n",
		"include":  "# x\n<!-- include: nope -->\n",
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "good.md"), []byte("# good\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "x.md"), []byte(src), 0o644)
		if _, err := set.LoadDir(dir); err == nil {
			t.Errorf("%s: no error", name)
		}
		if _, ok := set.Get("good"); ok {
			t.Errorf("%s: a failed load registered packs", name)
		}
	}
}

// A pack's source keeps its include lines, and building it with a part of the user's puts theirs in place of the
// built-in one.
func TestSourcesAndPartOverrides(t *testing.T) {
	if got := len(Builtins()); got != builtins {
		t.Fatalf("Builtins() has %d, want %d", got, builtins)
	}
	src, ok := Source("work")
	if !ok || !strings.Contains(src, "<!-- include: drafts -->") {
		t.Fatalf("work's source should keep its include lines: %.80q", src)
	}
	if !slices.Contains(Parts(), "drafts") {
		t.Fatalf("Parts() = %v, want drafts among them", Parts())
	}
	built, _ := Get("work")
	p, err := FromTextWith(src, "work", func(name string) (string, bool) {
		if name == "drafts" {
			return "MY DRAFTS RULES\n", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	drafts, _ := Part("drafts")
	if !strings.Contains(p.Text, "MY DRAFTS RULES") || strings.Contains(p.Text, strings.TrimSpace(drafts)) {
		t.Errorf("the user's part didn't replace the built-in one")
	}
	if p.Text == built.Text || !slices.Equal(p.Tools, built.Tools) {
		t.Errorf("only the part should change: tools %v, want %v", p.Tools, built.Tools)
	}
	same, _ := FromTextWith(src, "work", nil)
	if same.Text != built.Text {
		t.Error("with no parts of the user's, the source builds the built-in pack")
	}
}
