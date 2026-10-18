package tour

import (
	"math"
	"strconv"
	"strings"
)

// MarkKind is what an annotation on a screenshot is.
type MarkKind string

const (
	Pin   MarkKind = "pin"   // pin X,Y label: a numbered marker on a point
	Box   MarkKind = "box"   // box X,Y W,H label: a region, with everything else dimmed
	Arrow MarkKind = "arrow" // arrow X,Y -> X2,Y2 label: from the first point to the second
)

// Mark is one annotation on a screenshot. Every coordinate is a percentage of the image, so it holds at any size.
type Mark struct {
	Kind   MarkKind
	X, Y   float64
	X2, Y2 float64 // an arrow's head; a box's width and height
	Label  string
	N      int // pins and arrows are numbered in the order they're written; boxes are 0

	LabelBelow  bool // near the top edge, its label goes under it
	LabelToLeft bool // near the right edge, its label goes to its left
	AtLeftEdge  bool // near the left edge, it's anchored rather than centred
}

// ParseMarks reads a shot fence's annotations. A line that can't be read is skipped: a typo costs that annotation,
// never the screenshot.
func ParseMarks(body string) []Mark {
	var out []Mark
	n := 0
	for line := range strings.SplitSeq(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "pin", "at":
			x, y, ok := point(f[1])
			if !ok {
				continue
			}
			n++
			out = append(out, Mark{Kind: Pin, X: x, Y: y, N: n, Label: unquote(strings.Join(f[2:], " "))})
		case "box":
			if len(f) < 3 {
				continue
			}
			x, y, ok := point(f[1])
			w, h, ok2 := point(f[2])
			if !ok || !ok2 {
				continue
			}
			out = append(out, Mark{Kind: Box, X: x, Y: y, X2: w, Y2: h, Label: unquote(strings.Join(f[3:], " "))})
		case "arrow":
			var pts, words []string
			for _, w := range f[1:] {
				switch {
				case w == "->" || w == "→":
				case len(pts) < 2 && strings.Contains(w, ","):
					pts = append(pts, w)
				default:
					words = append(words, w)
				}
			}
			if len(pts) < 2 {
				continue
			}
			x, y, ok := point(pts[0])
			x2, y2, ok2 := point(pts[1])
			if !ok || !ok2 {
				continue
			}
			n++
			out = append(out, Mark{Kind: Arrow, X: x, Y: y, X2: x2, Y2: y2, N: n, Label: unquote(strings.Join(words, " "))})
		}
	}
	for i := range out {
		m := &out[i]
		if m.Kind == Box {
			m.LabelBelow = m.Y+m.Y2 <= 86 // under the box, unless the box sits at the bottom
			continue
		}
		m.LabelBelow, m.LabelToLeft, m.AtLeftEdge = m.Y < 12, m.X > 72, m.X < 14
	}
	return out
}

// point reads "62,18" as a pair of percentages.
func point(s string) (float64, float64, bool) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), ",")
	if !ok {
		return 0, 0, false
	}
	x, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return clampPct(x), clampPct(y), true
}

func clampPct(v float64) float64 { return math.Max(0, math.Min(100, v)) }
