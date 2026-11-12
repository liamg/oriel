package apptest

import (
	"strings"

	"github.com/liamg/oriel/render"
)

// Shows is the middle of the first place text is drawn whole inside every clip it's drawn in (a run cut with "…" counts
// for what's left of it), and whether there's one: text scrolled past a box's edge, or cut off, doesn't show.
func (s *Scene) Shows(text string) (render.Point, bool) {
	if s.List() == nil {
		return render.Point{}, false
	}
	clips := []render.Rect{{Max: render.Point{X: 1 << 20, Y: 1 << 20}}}
	for _, op := range s.List().Ops {
		top := clips[len(clips)-1]
		switch op.Kind {
		case render.OpPushClip:
			clips = append(clips, render.Rect{Min: render.Point{X: max(top.Min.X, op.Rect.Min.X), Y: max(top.Min.Y, op.Rect.Min.Y)},
				Max: render.Point{X: min(top.Max.X, op.Rect.Max.X), Y: min(top.Max.Y, op.Rect.Max.Y)}})
		case render.OpPushOpacity:
			clips = append(clips, top)
		case render.OpPop:
			clips = clips[:max(len(clips)-1, 1)]
		}
		if op.Kind != render.OpGlyphs || op.Run == nil || op.At.Y-op.Run.Ascent < top.Min.Y-.5 || op.At.Y+op.Run.Descent > top.Max.Y+.5 {
			continue
		}
		body := strings.TrimSuffix(op.Run.Text, "…")
		carets := map[int]float32{}
		for b, x := range op.Run.Carets() {
			carets[b] = x
		}
		for from := 0; ; {
			i := strings.Index(body[from:], text)
			if i < 0 {
				break
			}
			i += from
			x0, ok0 := carets[i]
			x1, ok1 := carets[i+len(text)]
			if ok0 && ok1 && op.At.X+x0 >= top.Min.X-.5 && op.At.X+x1 <= top.Max.X+.5 {
				return render.Point{X: op.At.X + (x0+x1)/2, Y: op.At.Y - op.Run.Ascent/2}, true
			}
			from = i + 1
		}
	}
	return render.Point{}, false
}
