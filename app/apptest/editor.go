package apptest

import (
	"image"
	"testing"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// Editor is the editor as a plugin reaches it, its Editors service; the test fails without one.
func (s *Scene) Editor() services.Editors {
	s.T.Helper()
	ed, ok := services.EditorsOf(s.Registry())
	if !ok {
		s.T.Fatal("no editor")
	}
	return ed
}

// EditorWindow is the editor's current window; the test fails without one.
func (s *Scene) EditorWindow() registry.EditorWindow {
	s.T.Helper()
	w, ok := s.Editor().Window("")
	if !ok {
		s.T.Fatal("no editor window")
	}
	return w
}

// EditorKeys types keys (vim notation) into the editor's current window as a mapping's right-hand side would, past
// the keymaps, a frame after each.
func (s *Scene) EditorKeys(keys string) {
	s.T.Helper()
	for _, k := range keymap.Parse(keys) {
		s.Run("editor.keys", registry.Args{"keys": keymap.Format([]string{k}), "noremap": true})
		s.Frame()
	}
}

// Line is the current editor window's line n, 0-based.
func (s *Scene) Line(n int) string {
	s.T.Helper()
	return string(s.EditorWindow().Text.Snapshot().Line(n))
}

// Decorations are every provider's decorations of the current editor window's lines [top, bottom), by line.
func (s *Scene) Decorations(top, bottom int) map[int][]uireg.Decoration {
	s.T.Helper()
	w := s.EditorWindow()
	ctx := uireg.DecorationCtx{Window: w.ID, Presenter: w.Presenter, Path: w.Path, Buffer: w.Text, Snap: w.Text.Snapshot(), Top: top,
		Bottom: bottom, Now: s.Now}
	if b, ok := s.Editor().Buffer(w.Path); ok {
		ctx.FileBytes = b.Bytes
	}
	return s.Registry().Decorate(ctx)
}

// WaitSyntax frames until the current editor window's buffer is parsed, and its parse has caught up.
func (s *Scene) WaitSyntax() {
	s.T.Helper()
	ed := s.Editor()
	s.Until("the buffer's parse", func() bool {
		_, ok := ed.Syntax(s.EditorWindow().Text)
		return ok
	})
	doc, _ := ed.Syntax(s.EditorWindow().Text)
	doc.Wait()
	s.RunQueued()
	s.Frame()
}

// EditorColumn is the editor column of 01-ide: from the left edge of the editor to the minimap, from the tabs to
// the agent dock.
func EditorColumn() image.Rectangle { return image.Rect(294, 38, 1150, 773) }

// EditorScene is the editor on round.go, in a window the size of the mockup's editor column, with the cursor where
// the mockup has it: line 46, on the ':' of "case 0:".
func EditorScene(t *testing.T) *Scene {
	dir := t.TempDir()
	rig.WriteFiles(t, dir, map[string]string{"internal/pricing/round.go": rig.RoundGo()})
	s := New(t, Options{WorkDir: dir, Size: render.Point{X: float32(EditorColumn().Dx()), Y: float32(EditorColumn().Dy())}})
	s.Registry().View(kit.TitleBarView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.ColumnNode() })) // the column starts under it
	s.Registry().View(kit.RailView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.ColumnNode() }))     // and right of the rail
	s.Run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.Frame()
	s.Frame()
	s.EditorKeys("46Gzt38zt46G$")
	s.WaitHighlights()
	return s
}

// WaitHighlights frames until each editor window on screen whose file has a language is parsed, and the parse has
// caught up.
func (s *Scene) WaitHighlights() {
	s.T.Helper()
	s.Frame() // asks for each buffer's parser
	ed := s.Editor()
	for _, v := range s.Host().Layout().Shown() {
		w, ok := ed.Window(v.Instance)
		if v.ID != kit.EditorView || !ok {
			continue
		}
		if _, ok := kit.LanguageFor(s.Registry(), w.Path, w.Text.Snapshot().Bytes()); !ok {
			continue
		}
		s.Until(w.Path+"'s parse", func() bool { _, ok := ed.Syntax(w.Text); return ok })
		doc, _ := ed.Syntax(w.Text)
		doc.Wait()
	}
	s.RunQueued()
	s.Frame()
}
