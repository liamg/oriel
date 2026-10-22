package shell_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// omniScene is 02-omnibox's world: three sessions, internal/pricing with round.go and tiers.go open, and the
// omnibox up.
func omniScene(t *testing.T, query string) *apptest.Scene {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "internal", "pricing"), 0o755)
	os.WriteFile(filepath.Join(dir, "internal", "pricing", "round.go"), []byte(rig.RoundGo()), 0o644)
	os.WriteFile(filepath.Join(dir, "internal", "pricing", "tiers.go"), []byte("package pricing\n\n// PriceForTier is a tier's price.\nfunc PriceForTier(t int) int {\n\treturn t * 100\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# pricing\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1600, Y: 1000}})
	for _, ev := range []core.SessionOpened{
		{ID: "s1", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusWorking},
		{ID: "s2", Name: "pricing api usage", Kind: core.KindWork, Status: core.StatusNeedsYou},
		{ID: "s3", Name: "price-feed backfill", Kind: core.KindJob, Status: core.StatusOff},
		{ID: "s4", Name: "dash-filters", Kind: core.KindWork, Status: core.StatusReviewer},
	} {
		s.Store().Apply(ev)
	}
	s.Store().Apply(core.SessionFocused{ID: "s1"})
	s.Run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.Run("editor.open", registry.Args{"path": "internal/pricing/tiers.go"})
	s.Registry().Command(registry.Command{ID: "plan.new", Title: "New plan from price…", Run: func(registry.Ctx, registry.Args) error { return nil }})
	s.Registry().Command(registry.Command{ID: "job.run", Title: "Run job: pricing test sweep", Run: func(registry.Ctx, registry.Args) error { return nil }})
	sourcesLoaded(s)
	s.Run("omnibox.open", registry.Args{"query": query})
	s.Frame()
	s.Frame()
	s.WaitSyntax()
	return s
}

func omni(s *apptest.Scene) *shell.Omnibox { return apptest.PluginOf[*shell.Omnibox](s, "omnibox") }

// The merged list for "pri", ranked and grouped, against a golden.
func TestOmniboxSnapshot(t *testing.T) {
	s := omniScene(t, "pri")
	s.Golden("omnibox")
	text := strings.ReplaceAll(s.Text(), "\n", "")
	for _, want := range []string{"SESSIONS", "COMMANDS", "FILES", "SYMBOLS", "PriceForTier", "internal/pricing/round.go"} {
		if !strings.Contains(text, want) {
			t.Errorf("the omnibox lacks %q", want)
		}
	}
	// The session that needs you comes first, as 02-omnibox has it.
	groups := omni(s).Results(false)
	if groups[0].Source() != "sessions" || groups[0].Items()[0].Label != "pricing api usage" {
		t.Errorf("first result: %s %q", groups[0].Source(), groups[0].Items()[0].Label)
	}
}

// The preview pane renders for each source.
func TestOmniboxPreviewEverySource(t *testing.T) {
	for _, tt := range []struct{ query, want string }{
		{"pricing api", "needs you"},
		{">job", "job.run"},
		{"@tiers", "    return t * 100"},
		{"#PriceFor", "func PriceForTier"},
	} {
		s := omniScene(t, tt.query)
		if !strings.Contains(strings.ReplaceAll(s.Text(), "\n", ""), tt.want) {
			t.Errorf("%q: the preview lacks %q:\n%s", tt.query, tt.want, s.Text())
		}
	}
}

// Prefixes narrow to one source: @ files, # symbols, > commands.
func TestOmniboxPrefixes(t *testing.T) {
	for q, src := range map[string]string{"@round": "files", "#Price": "symbols", ">plan": "commands"} {
		s := omniScene(t, q)
		groups := omni(s).Results(false)
		if len(groups) != 1 || groups[0].Source() != src {
			var ids []string
			for _, g := range groups {
				ids = append(ids, g.Source())
			}
			t.Errorf("%q: sources %v, want only %s", q, ids, src)
		}
	}
}

// Over more files than are ranked in the frame, the omnibox ranks them off the loop: typing shows the last ranking
// until the new one is made, then the files matching what was typed.
func TestOmniboxRanksManyFilesOffTheLoop(t *testing.T) {
	dir := t.TempDir()
	for i := range shell.RankAway + 500 {
		p := filepath.Join(dir, fmt.Sprintf("pkg%d", i%20), fmt.Sprintf("f%05d.go", i))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("package p\n"), 0o644)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1600, Y: 1000}})
	sourcesLoaded(s)
	s.Run("omnibox.open", nil)
	s.Frame()
	for _, k := range strings.Split("@f02371", "") {
		s.Type(k)
		s.Frame()
	}
	s.Until("pkg11/f02371.go in the omnibox", func() bool { return strings.Contains(s.Text(), "pkg11/f02371.go") })
	s.Type("Down")
	s.Frame()
	groups := omni(s).Results(true)
	if len(groups) != 1 || groups[0].Items()[0].Label != "pkg11/f02371.go" {
		t.Errorf("results after the ranking: %+v", groups)
	}
}

// ⌘K, a query and Enter open what it found.
func TestOmniboxOpensAFile(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOptions{Write: map[string]string{"a.txt": "alpha\n", "deep/tiers.go": "package deep\n\nfunc PriceForTier() {}\n"},
		Files: []string{"a.txt"}})
	r.WaitText("alpha")
	r.TypeKeys("<D-k>")
	r.WaitText("SESSIONS")
	r.TypeKeys("@tiers<CR>")
	r.WaitFrame("tiers.go open", func(s string) bool { return strings.Contains(s, "PriceForTier") && !strings.Contains(s, "FILES") })
}
