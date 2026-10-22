package editor_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// editor.font_size and editor.line_height are the code's own, and the rows follow them.
func TestEditorFontSize(t *testing.T) {
	s := editorScene(t)
	if m := editorOf(s).Metrics(); m.Font.Size != 13.5 || m.LineH != 21 {
		t.Fatalf("default code %v px on %v px rows, want 13.5 on 21 (01-ide)", m.Font.Size, m.LineH)
	}
	s.Registry().Options().Set("editor.font_size", registry.LayerUser, registry.At{}, 18.0)
	s.Registry().Options().Set("editor.line_height", registry.LayerUser, registry.At{}, 1.5)
	s.Frame()
	m := editorOf(s).Metrics()
	if m.Font.Size != 18 || m.LineH != 27 {
		t.Errorf("code %v px on %v px rows, want 18 on 27", m.Font.Size, m.LineH)
	}
	sizes := map[float32]bool{}
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Font.Family == typeset.Mono {
			sizes[op.Run.Font.Size] = true
		}
	}
	if !sizes[18] {
		t.Errorf("no code drawn at 18 px: %v", sizes)
	}
}
