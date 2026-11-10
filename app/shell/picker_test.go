package shell_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

func picker(s *apptest.Scene) *shell.Picker { return apptest.PluginOf[*shell.Picker](s, "picker") }

// pickerScene is omniScene's world with a picker open on source.
func pickerScene(t *testing.T, source, query string) *apptest.Scene {
	s := omniScene(t, "")
	omni(s).Close()
	s.Run("picker.open", registry.Args{"source": source, "query": query})
	s.Frame()
	s.Frame()
	return s
}

// Each source returns, ranks and previews.
func TestPickerSources(t *testing.T) {
	for _, tt := range []struct{ source, query, first, preview string }{
		{"files", "tiers", "internal/pricing/tiers.go", "return t * 100"},
		{"grep", "t \\* 100", "return t * 100", "func PriceForTier"},
		{"grep (PCRE2)", "(?<=return )t\\h\\*(?=\\s100)", "return t * 100", "func PriceForTier"},
		{"buffers", "round", "internal/pricing/round.go", "decimal.NewFromInt(7)"},
		{"symbols", "PFT", "PriceForTier", "func PriceForTier"},
		{"symbols.file", "", "PriceForTier", "func PriceForTier"},
		{"keymaps", "picker resume", "picker.resume", "<Space>'"},
		{"commands", "omnibox", "omnibox.open", "Search everything"},
	} {
		t.Run(tt.source, func(t *testing.T) {
			tt.source, _, _ = strings.Cut(tt.source, " ")
			s := pickerScene(t, tt.source, tt.query)
			src := mustSource(t, s, tt.source)
			g, _ := shell.RankSource(s.Headless, src, tt.query, shell.PickerMax)
			apptest.Poll(t, "results", func() bool { // a live source answers off the UI's goroutine
				var ok bool
				g, ok = shell.RankSource(s.Headless, src, tt.query, shell.PickerMax)
				return ok
			})
			text := s.WaitFor(tt.preview)
			first := g.Items()[0]
			if !strings.Contains(first.Label+" "+first.Detail, tt.first) {
				t.Errorf("first result %q (%s), want %q", first.Label, first.Detail, tt.first)
			}
			if !strings.Contains(text, tt.preview) {
				t.Errorf("the preview lacks %q:\n%s", tt.preview, text)
			}
		})
	}
}

// sourcesLoaded asks every picker source for its items, then draws frames until none is still loading.
func sourcesLoaded(s *apptest.Scene) {
	s.T.Helper()
	for _, src := range s.Registry().Pickers() {
		if src.Items != nil && src.Loading != nil {
			src.Items(nil, "")
		}
	}
	s.Until("the picker sources to load", func() bool {
		for _, src := range s.Registry().Pickers() {
			if shell.SourceLoading(src) {
				return false
			}
		}
		return true
	})
}

func mustSource(t *testing.T, s *apptest.Scene, id string) uireg.PickerSource {
	sourcesLoaded(s)
	for _, src := range s.Registry().Pickers() {
		if src.ID == id {
			return src
		}
	}
	t.Fatalf("no source %s", id)
	return uireg.PickerSource{}
}

// ␣' resumes: the last picker comes back with its source, query and selection.
func TestPickerResumes(t *testing.T) {
	s := pickerScene(t, "files", "go")
	p := picker(s)
	p.Select(1)
	p.Close()
	s.Frame()
	s.EditorKeys(" '")
	s.Run("picker.resume", nil)
	s.Frame()
	if !p.Open() || p.Source() != "files" || p.Query() != "go" || p.Selected() != 1 {
		t.Errorf("resumed: open %v source %q query %q sel %d", p.Open(), p.Source(), p.Query(), p.Selected())
	}
}

// The quickfix and attach actions work.
func TestPickerActions(t *testing.T) {
	s := pickerScene(t, "files", ".go")
	p := picker(s)
	src := mustSource(t, s, "files")
	g, _ := shell.RankSource(s.Headless, src, ".go", shell.PickerMax)
	var qf uireg.PickerAction
	var attach uireg.PickerAction
	for _, a := range p.Actions(src) {
		switch a.Key {
		case "<C-q>":
			qf = a
		case "<C-a>":
			attach = a
		}
	}
	p.Act(qf, g)
	title, items := s.Editor().Quickfix()
	if len(items) != 2 || !strings.HasPrefix(title, "Files") || items[0].Path == "" {
		t.Errorf("quickfix: %q %+v", title, items)
	}
	var got registry.Args
	s.Registry().Command(registry.Command{ID: "request.attach", Run: func(_ registry.Ctx, a registry.Args) error { got = a; return nil }})
	p.Reopen()
	p.Pick(g.Items()[1].ID)
	p.Act(attach, g)
	refs, _ := got["items"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["path"] != g.Items()[1].Path {
		t.Errorf("attach got %+v", got)
	}
	// :copen is the quickfix picker now, and Enter jumps.
	s.EditorKeys(":copen<CR>")
	s.Frame()
	if !p.Open() || p.Source() != "quickfix" {
		t.Errorf(":copen opened %q (open %v)", p.Source(), p.Open())
	}
}

// 26-picker: the files picker on a query, with its preview.
func TestPickerSnapshot(t *testing.T) {
	s := pickerScene(t, "files", "rndgo")
	s.WaitSyntax()
	s.Golden("picker-files")
}
