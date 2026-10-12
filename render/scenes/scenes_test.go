package scenes

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

func TestScenesDraw(t *testing.T) {
	ts, err := typeset.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range All() {
		for _, scale := range []float32{1, 1.25, 2} {
			c := NewCanvas(ts)
			st := NewState()
			var l render.List
			for i := range 3 {
				st.Frame, st.T, st.Line = i, float64(i)/4, FileLines+i
				l = render.List{}
				c.Begin(l.Record(scale))
				s.Draw(c, st)
			}
			if len(l.Ops) == 0 {
				t.Errorf("%s at %v× drew nothing", s.Name(), scale)
			}
			depth := 0
			for _, op := range l.Ops {
				switch op.Kind {
				case render.OpPushClip, render.OpPushOpacity:
					depth++
				case render.OpPop:
					depth--
				}
				if depth < 0 {
					t.Fatalf("%s pops more than it pushes", s.Name())
				}
			}
			if depth != 0 {
				t.Errorf("%s leaves %d layers pushed", s.Name(), depth)
			}
		}
	}
}

func TestIDEHasItsText(t *testing.T) {
	ts, _ := typeset.New(nil)
	var l render.List
	c := NewCanvas(ts)
	c.Begin(l.Record(1))
	IDE(c)
	var text []string
	for _, op := range l.Ops {
		if op.Kind == render.OpGlyphs {
			text = append(text, op.Run.Text)
		}
	}
	all := strings.Join(text, "|")
	for _, want := range []string{"ORIEL", "pricing-rounding", "roundHalfEven", "NORMAL", "Negative values round the wrong way"} {
		if !strings.Contains(all, want) {
			t.Errorf("01-ide has no %q", want)
		}
	}
}

func TestGoFile(t *testing.T) {
	f := GoFile(FileLines)
	if len(f) != FileLines || f[0] != "package pricing" {
		t.Fatalf("got %d lines, first %q", len(f), f[0])
	}
}
