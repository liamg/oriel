package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// omniScene is 02-omnibox's world: three sessions, internal/pricing with round.go and tiers.go open, and the
// omnibox up.
func omniScene(t *testing.T, query string) *errScene {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "internal", "pricing"), 0o755)
	os.WriteFile(filepath.Join(dir, "internal", "pricing", "round.go"), []byte(roundGo()), 0o644)
	os.WriteFile(filepath.Join(dir, "internal", "pricing", "tiers.go"), []byte("package pricing\n\n// PriceForTier is a tier's price.\nfunc PriceForTier(t int) int {\n\treturn t * 100\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# pricing\n"), 0o644)
	s := newScene(t, "", dir, render.Point{X: 1600, Y: 1000})
	for _, ev := range []core.SessionOpened{
		{ID: "s1", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusWorking},
		{ID: "s2", Name: "pricing api usage", Kind: core.KindWork, Status: core.StatusNeedsYou},
		{ID: "s3", Name: "price-feed backfill", Kind: core.KindJob, Status: core.StatusOff},
		{ID: "s4", Name: "dash-filters", Kind: core.KindWork, Status: core.StatusReviewer},
	} {
		s.store.Apply(ev)
	}
	s.store.Apply(core.SessionFocused{ID: "s1"})
	s.run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.run("editor.open", registry.Args{"path": "internal/pricing/tiers.go"})
	s.reg.Command(registry.Command{ID: "plan.new", Title: "New plan from price…", Run: func(registry.Ctx, registry.Args) error { return nil }})
	s.reg.Command(registry.Command{ID: "job.run", Title: "Run job: pricing test sweep", Run: func(registry.Ctx, registry.Args) error { return nil }})
	s.sourcesLoaded()
	s.run("omnibox.open", registry.Args{"query": query})
	s.frame()
	s.frame()
	s.waitHighlights()
	return s
}

func (s *errScene) omni() *omnibox {
	for _, e := range s.plugins {
		if o, ok := e.(*omnibox); ok {
			return o
		}
	}
	return nil
}

// SH-4: the merged list for "pri", ranked and grouped, against a golden.
func TestOmniboxSnapshot(t *testing.T) {
	s := omniScene(t, "pri")
	s.golden("omnibox")
	text := strings.ReplaceAll(s.frameText(), "\n", "")
	for _, want := range []string{"SESSIONS", "COMMANDS", "FILES", "SYMBOLS", "PriceForTier", "internal/pricing/round.go"} {
		if !strings.Contains(text, want) {
			t.Errorf("the omnibox lacks %q", want)
		}
	}
	// The session that needs you comes first, as 02-omnibox has it.
	groups, _ := s.omni().results(false)
	if groups[0].src.ID != "sessions" || groups[0].hits[0].item.Label != "pricing api usage" {
		t.Errorf("first result: %s %q", groups[0].src.ID, groups[0].hits[0].item.Label)
	}
}

// SH-4: "preview pane renders for each source".
func TestOmniboxPreviewEverySource(t *testing.T) {
	for _, tt := range []struct{ query, want string }{
		{"pricing api", "needs you"},
		{">job", "job.run"},
		{"@tiers", "    return t * 100"},
		{"#PriceFor", "func PriceForTier"},
	} {
		s := omniScene(t, tt.query)
		if !strings.Contains(strings.ReplaceAll(s.frameText(), "\n", ""), tt.want) {
			t.Errorf("%q: the preview lacks %q:\n%s", tt.query, tt.want, s.frameText())
		}
	}
}

// Prefixes narrow to one source: @ files, # symbols, > commands.
func TestOmniboxPrefixes(t *testing.T) {
	for q, src := range map[string]string{"@round": "files", "#Price": "symbols", ">plan": "commands"} {
		s := omniScene(t, q)
		groups, _ := s.omni().results(false)
		if len(groups) != 1 || groups[0].src.ID != src {
			var ids []string
			for _, g := range groups {
				ids = append(ids, g.src.ID)
			}
			t.Errorf("%q: sources %v, want only %s", q, ids, src)
		}
	}
}

// Over more files than are ranked in the frame, the omnibox ranks them off the loop: typing shows the last ranking
// until the new one is made, then the files matching what was typed.
func TestOmniboxRanksManyFilesOffTheLoop(t *testing.T) {
	dir := t.TempDir()
	for i := range rankAway + 500 {
		p := filepath.Join(dir, fmt.Sprintf("pkg%d", i%20), fmt.Sprintf("f%05d.go", i))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("package p\n"), 0o644)
	}
	s := newScene(t, "", dir, render.Point{X: 1600, Y: 1000})
	s.sourcesLoaded()
	s.run("omnibox.open", nil)
	s.frame()
	for _, k := range strings.Split("@f02371", "") {
		s.typeKeys(k)
		s.frame()
	}
	if !s.Until(10*time.Second, func() bool { return strings.Contains(s.frameText(), "pkg11/f02371.go") }) {
		t.Fatalf("the omnibox never showed pkg11/f02371.go:\n%s", s.frameText())
	}
	s.typeKeys("Down")
	s.frame()
	groups, _ := s.omni().results(true)
	if len(groups) != 1 || groups[0].hits[0].item.Label != "pkg11/f02371.go" {
		t.Errorf("results after the ranking: %+v", groups)
	}
}
