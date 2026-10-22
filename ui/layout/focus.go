package layout

import (
	"cmp"
	"slices"

	"github.com/liamg/oriel/render"
)

// Dir is a direction for ⌃W h/j/k/l.
type Dir string

const (
	DirLeft  Dir = "h"
	DirDown  Dir = "j"
	DirUp    Dir = "k"
	DirRight Dir = "l"
)

// FocusDir moves focus to the nearest pane in dir from the focused one, across main and the docks alike, by
// where they were drawn last frame (rects, by leaf).
func (r *Root) FocusDir(d Dir, rects map[*Node]render.Rect) bool {
	if r.Focus == nil {
		return false
	}
	w := r.Find(*r.Focus)
	if w == nil {
		return false
	}
	from, ok := rects[w.Leaf]
	if !ok {
		return false
	}
	cx, cy := (from.Min.X+from.Max.X)/2, (from.Min.Y+from.Max.Y)/2
	type cand struct {
		l *Node
		d float32
		r render.Rect
	}
	var cands []cand
	for l, rc := range rects {
		if l == w.Leaf || len(l.Tabs) == 0 {
			continue
		}
		var gap, off float32
		switch d {
		case DirLeft:
			gap, off = from.Min.X-rc.Max.X, abs(cy-(rc.Min.Y+rc.Max.Y)/2)
		case DirRight:
			gap, off = rc.Min.X-from.Max.X, abs(cy-(rc.Min.Y+rc.Max.Y)/2)
		case DirUp:
			gap, off = from.Min.Y-rc.Max.Y, abs(cx-(rc.Min.X+rc.Max.X)/2)
		case DirDown:
			gap, off = rc.Min.Y-from.Max.Y, abs(cx-(rc.Min.X+rc.Max.X)/2)
		}
		if gap >= -1 {
			cands = append(cands, cand{l, gap*4 + off, rc})
		}
	}
	if len(cands) == 0 {
		return false
	}
	// Nearest first; a tie goes back to where focus came from, then to the leftmost, topmost.
	prev := func(c cand) bool { return r.prev != nil && slices.Contains(c.l.Tabs, *r.prev) }
	slices.SortFunc(cands, func(a, b cand) int {
		switch {
		case abs(a.d-b.d) > 1:
			return cmp.Compare(a.d, b.d)
		case prev(a) != prev(b):
			if prev(a) {
				return -1
			}
			return 1
		case a.r.Min.X != b.r.Min.X:
			return cmp.Compare(a.r.Min.X, b.r.Min.X)
		}
		return cmp.Compare(a.r.Min.Y, b.r.Min.Y)
	})
	best := cands[0].l
	v := best.Tabs[best.Active]
	r.prev, r.Focus = r.Focus, &v
	return true
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
