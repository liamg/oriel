package autoapprove

import (
	"strings"
	"testing"
)

// The rules as crit's internal/autoapprove applies them (crit-reference.md, agentbus: "Auto-approve rules").

type C = Cond

func lines(n int) *int { return &n }

func TestGlob(t *testing.T) {
	cases := []struct {
		pat, file string
		want      bool
	}{
		{"go.mod", "go.mod", true},
		{"go.mod", "tools/go.mod", true},
		{"*.md", "README.md", true},
		{"*.md", "docs/a/b.md", true},
		{"*.md", "main.go", false},
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "docs", true},
		{"docs/**", "src/docs/a.md", false},
		{"**/testdata/*", "a/b/testdata/x.json", true},
		{"**/testdata/*", "testdata/x.json", true},
		{"internal/*.go", "internal/a/b.go", false},
		{"/go.sum", "go.sum", true},
	}
	for _, c := range cases {
		if got := Glob(c.pat, c.file); got != c.want {
			t.Errorf("Glob(%q, %q) = %v, want %v", c.pat, c.file, got, c.want)
		}
	}
}

func TestMatch(t *testing.T) {
	deps := Rule{Name: "deps", Enabled: true, When: C{All: []C{
		{Repo: []string{`infracost/(runner|cli)`}},
		{Author: []string{"liamg", "aliscott"}},
		{Any: []C{
			{Paths: []string{"go.mod", "go.sum"}},
			{MaxLines: lines(20)},
		}},
	}}}
	base := PR{Repo: "Infracost/runner", Author: "LiamG", Additions: 100, Deletions: 50,
		Files: []File{{Path: "go.mod"}, {Path: "go.sum"}}}
	cases := []struct {
		name string
		mod  func(*PR)
		want bool
	}{
		{"deps files", func(*PR) {}, true},
		{"small change", func(p *PR) { p.Files = []File{{Path: "main.go"}}; p.Additions, p.Deletions = 10, 10 }, true},
		{"big non-deps", func(p *PR) { p.Files = append(p.Files, File{Path: "main.go"}) }, false},
		{"other repo", func(p *PR) { p.Repo = "infracost/runner-x" }, false},
		{"other author", func(p *PR) { p.Author = "someone" }, false},
		{"rename into allowed name", func(p *PR) {
			p.Files = []File{{Path: "go.mod", Previous: ".github/workflows/ci.yml"}}
		}, false},
		{"no files", func(p *PR) { p.Files = nil }, false},
	}
	for _, c := range cases {
		pr := base
		pr.Files = append([]File(nil), base.Files...)
		c.mod(&pr)
		if _, got := Match([]Rule{deps}, pr); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if _, ok := Match(nil, base); ok {
		t.Error("matched with no rules")
	}
	if _, ok := Match([]Rule{{Name: "empty", Enabled: true}}, base); ok {
		t.Error("empty rule matched")
	}
	off := deps
	off.Enabled = false
	if _, ok := Match([]Rule{off}, base); ok {
		t.Error("disabled rule matched")
	}
	green := Rule{Name: "green", Enabled: true, When: C{All: []C{
		{Repo: []string{".*"}}, {MaxLines: lines(1000)}, {ChecksGreen: true}, {Requester: []string{"a@x.io"}},
	}}}
	pr := base
	pr.Requester = "A@x.io"
	if _, ok := Match([]Rule{green}, pr); ok {
		t.Error("matched with checks not green")
	}
	pr.ChecksGreen = true
	if name, ok := Match([]Rule{deps, green}, pr); !ok || name != "deps" {
		t.Errorf("got %q %v, want first rule deps", name, ok)
	}
}

func TestValidate(t *testing.T) {
	repo := C{Repo: []string{"infracost/cli"}}
	cases := []struct {
		name string
		when C
		err  string
	}{
		{"ok", C{All: []C{repo, {Paths: []string{"*.md"}}}}, ""},
		{"no repo", C{Paths: []string{"*.md"}}, ""},
		{"no limit", C{All: []C{repo, {Author: []string{"x"}}}}, "paths or maxLines"},
		{"or escapes limit", C{All: []C{repo, {Any: []C{{Paths: []string{"*.md"}}, {Author: []string{"x"}}}}}}, "paths or maxLines"},
		{"or of limits", C{All: []C{repo, {Any: []C{{Paths: []string{"*.md"}}, {MaxLines: lines(5)}}}}}, ""},
		{"two keys", C{Repo: []string{"a/b"}, Paths: []string{"*.md"}}, "exactly one"},
		{"empty group", C{All: []C{}}, "empty all"},
		{"bad regex", C{All: []C{{Repo: []string{"("}}, {MaxLines: lines(1)}}}, "bad repo pattern"},
		{"bad glob", C{All: []C{repo, {Paths: []string{"[a"}}}}, "bad path glob"},
	}
	for _, c := range cases {
		err := Validate([]Rule{{Name: c.name, When: c.when}})
		switch {
		case c.err == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: got %v, want error containing %q", c.name, err, c.err)
		}
	}
}

// Rules are written as crit's config wrote them: JSON, checked as they're read.
func TestParse(t *testing.T) {
	rules, err := Parse(`[{"name": "docs", "enabled": true, "when": {"all": [{"repo": ["acme/.*"]}, {"paths": ["*.md"]}]}}]`)
	if err != nil || len(rules) != 1 || rules[0].Name != "docs" || !AnyEnabled(rules) {
		t.Fatalf("Parse: %+v %v", rules, err)
	}
	if name, ok := Match(rules, PR{Repo: "acme/api", Files: []File{{Path: "README.md"}}}); !ok || name != "docs" {
		t.Errorf("Match: %q %v", name, ok)
	}
	if _, err := Parse(`[{"name": "loose", "enabled": true, "when": {"repo": ["acme/.*"]}}]`); err == nil {
		t.Error("a rule with no limit parsed")
	}
	if rules, err := Parse("  "); err != nil || rules != nil {
		t.Errorf("empty: %v %v", rules, err)
	}
}
