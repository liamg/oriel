package ui_test

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui"
)

func TestDrawStacksAColumn(t *testing.T) {
	be := headless.New()
	w, _ := be.NewWindow(render.WindowOpts{})
	var st ui.Style
	st.Font.Size = 10
	var h float32
	w.Frame(func(f render.Frame) {
		h = ui.Draw(f, be.Shaper(), st, ui.Column(ui.Text("a"), ui.Column(ui.Text("b"), ui.Text("c"))), render.Point{X: 5, Y: 7})
	})
	ops := w.(*headless.Window).Frames()[0].Ops
	if len(ops) != 3 {
		t.Fatalf("%d ops, want 3", len(ops))
	}
	for i, want := range []struct {
		text string
		y    float32
	}{{"a", 7}, {"b", 21}, {"c", 35}} {
		if ops[i].Run.Text != want.text || ops[i].At != (render.Point{X: 5, Y: want.y}) {
			t.Errorf("op %d: %q at %v; want %q at y %v", i, ops[i].Run.Text, ops[i].At, want.text, want.y)
		}
	}
	if h != 42 {
		t.Errorf("height %v, want 42", h)
	}
}
