package render

// ShownRun is a run of text drawn in a list: its box, what of it the clips it's drawn in let show (empty when they
// hide it all), and how opaque the layers it's in draw it.
type ShownRun struct {
	Text    string
	At      Point // its baseline's start
	Box     Rect
	Shown   Rect
	Opacity float32
}

// Visible says some of it shows: not clipped away, nor drawn fully transparent.
func (r ShownRun) Visible() bool { return r.Shown.Dx() > 0 && r.Shown.Dy() > 0 && r.Opacity > 0 }

// Runs is every run of text in l, in drawing order, with what of it shows.
func (l *List) Runs() []ShownRun {
	type layer struct {
		clip    Rect
		clipped bool
		opacity float32
	}
	stack := []layer{{opacity: 1}}
	var out []ShownRun
	for _, op := range l.Ops {
		top := stack[len(stack)-1]
		switch op.Kind {
		case OpPushClip:
			c := op.Rect
			if top.clipped {
				c = cut(top.clip, c)
			}
			stack = append(stack, layer{clip: c, clipped: true, opacity: top.opacity})
		case OpPushOpacity:
			stack = append(stack, layer{clip: top.clip, clipped: top.clipped, opacity: top.opacity * op.Amount})
		case OpPop:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case OpGlyphs:
			box := Rect{Min: Point{X: op.At.X, Y: op.At.Y - op.Run.Ascent}, Max: Point{X: op.At.X + op.Run.Advance, Y: op.At.Y + op.Run.Descent}}
			shown := box
			if top.clipped {
				shown = cut(top.clip, box)
			}
			out = append(out, ShownRun{Text: op.Run.Text, At: op.At, Box: box, Shown: shown, Opacity: top.opacity})
		}
	}
	return out
}

// cut is what a and b share; empty, at a corner, when they don't meet.
func cut(a, b Rect) Rect {
	r := Rect{
		Min: Point{X: max(a.Min.X, b.Min.X), Y: max(a.Min.Y, b.Min.Y)},
		Max: Point{X: min(a.Max.X, b.Max.X), Y: min(a.Max.Y, b.Max.Y)},
	}
	if r.Max.X < r.Min.X || r.Max.Y < r.Min.Y {
		return Rect{Min: r.Min, Max: r.Min}
	}
	return r
}
