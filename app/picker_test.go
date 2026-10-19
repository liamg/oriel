package app

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

func (s *errScene) picker() *picker {
	for _, e := range s.plugins {
		if p, ok := e.(*picker); ok {
			return p
		}
	}
	return nil
}

// pickerScene is omniScene's world with a picker open on source.
func pickerScene(t *testing.T, source, query string) *errScene {
	s := omniScene(t, "")
	s.omni().open = false
	s.run("picker.open", registry.Args{"source": source, "query": query})
	s.frame()
	s.frame()
	return s
}

// waitFor frames until the joined frame text has want, for sources that answer off the UI's goroutine.
func (s *errScene) waitFor(want string) string {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.frame()
		text := strings.ReplaceAll(s.frameText(), "\n", "")
		if strings.Contains(text, want) || time.Now().After(deadline) {
			return text
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// SH-5: "each source returns, ranks and previews".
func TestPickerSources(t *testing.T) {
	for _, tt := range []struct{ source, query, first, preview string }{
		{"files", "tiers", "internal/pricing/tiers.go", "return t * 100"},
		{"grep", "t \\* 100", "return t * 100", "func PriceForTier"},
		{"grep (PCRE2)", "(?<=return )t\\h\\*(?=\\s100)", "return t * 100", "func PriceForTier"},
		{"buffers", "round", "internal/pricing/round.go", "decimal.NewFromInt(7)"},
		{"symbols", "PFT", "PriceForTier", "func PriceForTier"},
		{"symbols.file", "", "PriceForTier", "func PriceForTier"},
		{"keymaps", "picker resume", "picker.resume", "<Space>'"},
		{"commands", "omnibox", "Search everything", "omnibox.open"},
	} {
		t.Run(tt.source, func(t *testing.T) {
			tt.source, _, _ = strings.Cut(tt.source, " ")
			s := pickerScene(t, tt.source, tt.query)
			src := mustSource(t, s, tt.source)
			g, ok := rankSource(s.h(), src, tt.query, pickerMax)
			for deadline := time.Now().Add(5 * time.Second); !ok && time.Now().Before(deadline); { // a live source answers off the UI's goroutine
				time.Sleep(5 * time.Millisecond)
				g, ok = rankSource(s.h(), src, tt.query, pickerMax)
			}
			if !ok {
				t.Fatalf("no results")
			}
			text := s.waitFor(tt.preview)
			first := g.hits[0].item
			if !strings.Contains(first.Label+" "+first.Detail, tt.first) {
				t.Errorf("first result %q (%s), want %q", first.Label, first.Detail, tt.first)
			}
			if !strings.Contains(text, tt.preview) {
				t.Errorf("the preview lacks %q:\n%s", tt.preview, text)
			}
		})
	}
}

func (s *errScene) h() *pluginHost { return s.sh.host("test") }

// sourcesLoaded asks every picker source for its items, then draws frames until none is still loading.
func (s *errScene) sourcesLoaded() {
	s.t.Helper()
	for _, src := range s.reg.Pickers() {
		if src.Items != nil && src.Loading != nil {
			src.Items(nil, "")
		}
	}
	if !s.Until(15*time.Second, func() bool {
		for _, src := range s.reg.Pickers() {
			if sourceLoading(src) {
				return false
			}
		}
		return true
	}) {
		s.t.Fatal("the picker sources never finished loading")
	}
}

func mustSource(t *testing.T, s *errScene, id string) registry.PickerSource {
	s.sourcesLoaded()
	for _, src := range s.reg.Pickers() {
		if src.ID == id {
			return src
		}
	}
	t.Fatalf("no source %s", id)
	return registry.PickerSource{}
}

// SH-5: "␣' resumes": the last picker comes back with its source, query and selection.
func TestPickerResumes(t *testing.T) {
	s := pickerScene(t, "files", "go")
	p := s.picker()
	p.sel = 1
	p.open = false
	s.frame()
	s.editor().e.Key(" ")
	s.editor().e.Key("'")
	s.run("picker.resume", nil)
	s.frame()
	if !p.open || p.source != "files" || p.query != "go" || p.sel != 1 {
		t.Errorf("resumed: open %v source %q query %q sel %d", p.open, p.source, p.query, p.sel)
	}
}

// SH-5: "quickfix and attach actions work".
func TestPickerActions(t *testing.T) {
	s := pickerScene(t, "files", ".go")
	p := s.picker()
	src := mustSource(t, s, "files")
	g, _ := rankSource(s.h(), src, ".go", pickerMax)
	var qf registry.PickerAction
	var attach registry.PickerAction
	for _, a := range p.actions(src) {
		switch a.Key {
		case "<C-q>":
			qf = a
		case "<C-a>":
			attach = a
		}
	}
	p.act(qf, g)
	items, title, _ := s.editor().e.Quickfix()
	if len(items) != 2 || !strings.HasPrefix(title, "Files") || items[0].Name == "" {
		t.Errorf("quickfix: %q %+v", title, items)
	}
	var got registry.Args
	s.reg.Command(registry.Command{ID: "request.attach", Run: func(_ registry.Ctx, a registry.Args) error { got = a; return nil }})
	p.open = true
	p.picked[g.hits[1].item.ID] = true
	p.act(attach, g)
	refs, _ := got["items"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["path"] != g.hits[1].item.Path {
		t.Errorf("attach got %+v", got)
	}
	// :copen is the quickfix picker now, and Enter jumps.
	s.editor().e.Run("copen")
	s.frame()
	if !p.open || p.source != "quickfix" {
		t.Errorf(":copen opened %q (open %v)", p.source, p.open)
	}
}

// 26-picker: the files picker on a query, with its preview.
func TestPickerSnapshot(t *testing.T) {
	s := pickerScene(t, "files", "rndgo")
	s.waitHighlights()
	s.golden("picker-files")
}
