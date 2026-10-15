// Package tour reads a tour session's steps (crit's internal/tour): markdown files at the workspace root whose fences
// quote code straight out of the tour's pinned worktrees, so the code and its line numbers are genuinely the code's.
// The reader draws what Load returns; Markdown and HTML export it.
package tour

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Files a tour keeps beside its steps, relative to its root.
const (
	AssetsDir    = "assets"    // screenshots and images
	HarnessDir   = "harness"   // a demo's scaffolding
	HTMLFile     = "tour.html" // the HTML export
	MarkdownFile = "tour.md"   // the markdown export
)

// Kind is a kind of tour: what the agent researches and how it shapes the steps. The format never changes.
type Kind struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Desc  string `json:"desc"`
}

// Kinds is every kind, in menu order; the first is the default.
var Kinds = []Kind{
	{"walkthrough", "Walkthrough", "how the code works"},
	{"onboarding", "Onboarding", "a whole codebase"},
	{"archaeology", "Archaeology", "how it got this way"},
	{"showcase", "Showcase", "what it does, from outside"},
	{"demo", "Demo", "built and run for real"},
	{"incident", "Incident", "symptom to cause"},
	{"feasibility", "Feasibility", "how hard would X be"},
}

// DefaultKind is a tour's kind when none was asked for.
const DefaultKind = "walkthrough"

// ValidKind reports whether s names a kind of tour.
func ValidKind(s string) bool {
	return slices.ContainsFunc(Kinds, func(k Kind) bool { return k.ID == s })
}

//go:embed kinds
var guidance embed.FS

// Guidance is how to shape a kind of tour, for its agent; "" for a kind with none.
func Guidance(kind string) string {
	b, err := guidance.ReadFile("kinds/" + kind + ".md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// stepFile matches a step: two or three digits, a slug, .md. The number orders the tour, and keeps a stray NOTES.md
// out of it.
var stepFile = regexp.MustCompile(`^\d{2,3}-[^/]*\.md$`)

// IsStep reports whether a file name is a step's.
func IsStep(name string) bool { return stepFile.MatchString(name) }

// StepFiles lists a tour's steps in order.
func StepFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && IsStep(e.Name()) {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

// MatchStep resolves a step's file name, or "3" or "03", to the step file it names; "" when there's none.
func MatchStep(root, want string) string {
	files, err := StepFiles(root)
	if err != nil {
		return ""
	}
	want = strings.TrimSpace(want)
	for _, f := range files {
		num, _, _ := strings.Cut(f, "-")
		if f == want || num == want || strings.TrimLeft(num, "0") == strings.TrimLeft(want, "0") {
			return f
		}
	}
	return ""
}

// AssetPath resolves an image a step names to a file inside the tour, refusing anything outside it.
func AssetPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%q is not a path inside the tour", rel)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(rootAbs, filepath.Clean(filepath.FromSlash(rel)))
	if !inside(rootAbs, abs) {
		return "", fmt.Errorf("%q leaves the tour", rel)
	}
	if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
		return "", fmt.Errorf("%s is not there", rel)
	}
	return abs, nil
}

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
