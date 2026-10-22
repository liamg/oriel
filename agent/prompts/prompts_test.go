package prompts

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/packs"
)

// The Prompts page has editable prompts, including Coding style (also written into the generated CLAUDE.md), and
// stores only overrides. Oriel's are the coding style, the side requests (explain, the verify quiz,
// redrafts), each pack and each part packs include.
var placeholder = regexp.MustCompile(`\{\{(\w+)\}\}`)

func TestEveryPromptIsOnThePage(t *testing.T) {
	defs := Defs()
	sides := []string{"explain.request", "explain.walkthrough", "verify.questions", "verify.grade", "drafts.commit.again", "drafts.pr.again"}
	want := 1 + len(sides) + len(packs.Builtins()) + len(packs.Parts())
	if len(defs) != want {
		t.Fatalf("%d prompts, want %d (the style, %d side requests, %d packs, %d parts)", len(defs), want, len(sides), len(packs.Builtins()), len(packs.Parts()))
	}
	for _, id := range sides {
		d, ok := Lookup(id)
		if !ok {
			t.Errorf("no prompt %s", id)
			continue
		}
		for _, m := range placeholder.FindAllStringSubmatch(d.Default, -1) {
			if m[1] != "style" && !slices.ContainsFunc(d.Vars, func(v Var) bool { return v.Name == m[1] }) {
				t.Errorf("%s: {{%s}} isn't among its vars", id, m[1])
			}
		}
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if seen[d.ID] {
			t.Errorf("%s: two prompts have the id", d.ID)
		}
		seen[d.ID] = true
		if d.Group == "" || d.Label == "" || d.Help == "" || strings.TrimSpace(d.Default) == "" || d.File == "" {
			t.Errorf("%s: group %q, label %q, help %q, file %q, default %d bytes", d.ID, d.Group, d.Label, d.Help, d.File, len(d.Default))
		}
	}
	if defs[0].ID != StyleID || defs[0].Label != "Coding style" {
		t.Errorf("the page opens on %s (%s), want the coding style", defs[0].ID, defs[0].Label)
	}
	work, ok := Lookup(PackID("work"))
	if !ok || work.Label != "Work" || work.File != filepath.Join("agents", "work.md") {
		t.Errorf("work's pack: %+v", work)
	}
	if src, _ := packs.Source("work"); work.Default != src {
		t.Error("a pack's default is its source, include lines and all")
	}
	drafts, _ := Lookup(PartID("drafts"))
	if !strings.Contains(drafts.Help, "work") || drafts.File != filepath.Join("agents", "parts", "drafts.md") {
		t.Errorf("the drafts part: %+v", drafts)
	}
}

// crit's internal/prompts/defaults/01-style.code.md, its text verbatim.
func TestCodingStyleIsCrits(t *testing.T) {
	d, _ := Lookup(StyleID)
	want := "Please consider the following:\n\n" +
		"- No \"essay\"-style comments. Keep comments short and to the point. We generally don't want 10-line comments before 3-line functions.\n" +
		"- No comments about what happened before, a bug that was fixed etc. - that's what git history is for. Comments just reflect the \"now\"."
	if d.Default != want {
		t.Errorf("the coding style is\n%s\nwant\n%s", d.Default, want)
	}
}

// Only what the user changed is kept: saving the default, or nothing, takes the file away.
func TestOnlyOverridesAreStored(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if got := s.Load(); len(got) != 0 {
		t.Fatalf("a fresh config has overrides: %v", got)
	}
	if err := s.Set(StyleID, "Tabs, never spaces.\n"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "prompts", "style.code.md")
	if b, err := os.ReadFile(path); err != nil || string(b) != "Tabs, never spaces.\n" {
		t.Fatalf("the style's file: %q, %v", b, err)
	}
	o := s.Load()
	if !o.Custom(StyleID) || o.Style() != "Tabs, never spaces." || len(o) != 1 {
		t.Errorf("loaded %v", o)
	}
	if o.Custom(PackID("work")) {
		t.Error("a pack left alone reads as changed")
	}
	d, _ := Lookup(StyleID)
	if err := s.Set(StyleID, "\n"+d.Default+"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("saving the default kept a file")
	}
	if err := s.Set(PackID("work"), "---\nname: work\n---\n# Mine\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(PackID("work")); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); len(got) != 0 {
		t.Errorf("after the resets: %v", got)
	}
	if err := s.Set(PackID("work"), "<!-- include: nosuchpart -->\n"); err == nil {
		t.Error("a pack that doesn't build was kept")
	}
	if err := s.Set("nope", "x"); err == nil {
		t.Error("an unknown prompt was kept")
	}
}

// A hand-written agents/<kind>.md is the same override the page writes.
func TestAPackFileIsItsOverride(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(s.Dir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "agents", "plan.md"), []byte("---\nname: plan\n---\n# My plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if o := s.Load(); !o.Custom(PackID("plan")) {
		t.Errorf("the plan pack's file isn't its override: %v", o)
	}
}

func TestPacksTakeTheUsersVersions(t *testing.T) {
	builtin, _ := packs.Get("work")
	var none Overrides
	if p, _ := none.Pack("work", nil); p.Text != builtin.Text {
		t.Error("with no overrides, a pack is the built-in one")
	}
	o := Overrides{PartID("drafts"): "Leave the drafts to me.\n"}
	p, ok := o.Pack("work", nil)
	if !ok || !strings.Contains(p.Text, "Leave the drafts to me.") || !slices.Equal(p.Tools, builtin.Tools) {
		t.Errorf("the user's drafts part isn't in the work pack: %v", ok)
	}
	o[PackID("work")] = "---\nname: work\ntools: reply\n---\n# Mine\n\n{{style}}\n\n<!-- include: drafts -->\n"
	o[StyleID] = "Short names."
	p, _ = o.Pack("work", nil)
	if !strings.Contains(p.Text, "# Mine\n\nShort names.\n\nLeave the drafts to me.") || !slices.Equal(p.Tools, []string{"reply"}) {
		t.Errorf("the user's work pack: %q %v", p.Text, p.Tools)
	}
	base := func(name string) (agent.Pack, bool) {
		return agent.Pack{Name: name, Text: "a plugin's, {{style}}"}, name == "hotfix"
	}
	if p, ok := o.Pack("hotfix", base); !ok || p.Text != "a plugin's, Short names." {
		t.Errorf("a pack that isn't built in comes from base: %q %v", p.Text, ok)
	}
}

func TestRender(t *testing.T) {
	o := Overrides{StyleID: "Be brief."}
	greet, err := Prepare(Def{ID: "test.greet", Label: "Greet", Help: "a test's", Default: "Hello {{name}}, {{nmae}}. {{style}}"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(Def{ID: "test.greet", Default: "x"}, greet); err == nil {
		t.Error("a second prompt took the same id")
	}
	if got := o.Render("test.greet", map[string]string{"name": "Ada"}, greet); got != "Hello Ada, {{nmae}}. Be brief." {
		t.Errorf("rendered %q", got)
	}
	d, _ := Lookup("test.greet", greet)
	if d.Group != "Plugins" || d.File != filepath.Join("prompts", "test.greet.md") {
		t.Errorf("a registered prompt: %+v", d)
	}
	if _, ok := Lookup("test.greet"); ok {
		t.Error("a prompt not registered is on the page")
	}
}

func TestParseDef(t *testing.T) {
	d, err := parseDef("x", "---\ngroup: G\nlabel: L\nhelp: H\nvars:\n  project: the project\n---\nBody {{project}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if d.Group != "G" || d.Label != "L" || d.Help != "H" || len(d.Vars) != 1 || d.Vars[0] != (Var{"project", "the project"}) || d.Default != "Body {{project}}" {
		t.Errorf("%+v", d)
	}
	for _, bad := range []string{"no frontmatter", "---\ngroup: G\n", "---\nwho: x\n---\nbody", "---\ngroup: G\n---\n"} {
		if _, err := parseDef("x", bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if idFromFile("defaults/01-style.code.md") != "style.code" {
		t.Error(idFromFile("defaults/01-style.code.md"))
	}
}
