package apptest

import (
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/tools/perf"
)

// Lint fails the test for each interactive node in the frame with no help id, or one that doesn't resolve.
func (s *Scene) Lint(state string) {
	s.T.Helper()
	for _, p := range s.HelpLint() {
		s.T.Errorf("%s: %s", state, p)
	}
}

// FullText is the frame's texts one a line, whole where the frame cut them to fit with "…".
func (s *Scene) FullText() string {
	var lines []string
	for _, l := range strings.Split(s.UI().Dump(), "\n") {
		l = strings.TrimSpace(l)
		if i := strings.Index(l, " \""); strings.HasPrefix(l, "text ") && i > 0 {
			if q, err := strconv.QuotedPrefix(l[i+1:]); err == nil {
				t, _ := strconv.Unquote(q)
				lines = append(lines, t)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// WantFrame fails the test for each of wants the frame lacks.
func (s *Scene) WantFrame(wants ...string) {
	s.T.Helper()
	s.Quiet()
	text := s.Text()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			s.T.Errorf("the frame lacks %q:\n%s", w, text)
		}
	}
}

// TextPoint is the middle of the first run of text in the frame that reads text.
func (s *Scene) TextPoint(text string) render.Point {
	s.T.Helper()
	if s.List() == nil {
		s.T.Fatalf("no %q: no frame drawn yet", text)
	}
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == text {
			return render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	s.T.Fatalf("no %q in the frame", text)
	return render.Point{}
}

// TextStart is where the first run of text starting with prefix is drawn: its pen on the baseline.
func (s *Scene) TextStart(prefix string) (render.Point, bool) {
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && strings.HasPrefix(op.Run.Text, prefix) {
			return op.At, true
		}
	}
	return render.Point{}, false
}

// MeanFrame is how long a frame takes, on average over 100 after 10 to settle, timed by perf.Times.
func (s *Scene) MeanFrame() time.Duration {
	s.Frames(10)
	return MeanFrames(s)[0]
}

// MeanFrames is each scene's MeanFrame, their frames taken in turn so what runs alongside slows each alike.
func MeanFrames(scenes ...*Scene) []time.Duration {
	for _, s := range scenes {
		s.Frames(10)
	}
	n := len(scenes)
	times := perf.Times(3, 100*n, nil, func(i int) { scenes[i%n].Frame() })
	out := make([]time.Duration, n)
	for i := range out {
		var mine []time.Duration
		for j := i; j < len(times); j += n {
			mine = append(mine, times[j])
		}
		out[i] = perf.Mean(mine)
	}
	return out
}

// Frames draws n frames, and more while what they read from disk is coming in.
func (s *Scene) Frames(n int) {
	for range n {
		s.Frame()
	}
	if rd, ok := kit.ReadsOf(s.Registry()); ok && rd.Busy() {
		s.SettleReads()
	}
}

// SignOnRow is the line number num drawn in the gutter and the sign drawn on its row.
func (s *Scene) SignOnRow(num, sign string) (n, g *render.Op) {
	s.T.Helper()
	ops := s.List().Ops
	for i, op := range ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == num {
			n = &ops[i]
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == sign && n != nil && op.At.Y > n.At.Y-8 && op.At.Y < n.At.Y+8 {
			return n, &ops[i]
		}
	}
	s.T.Fatalf("no %q and %q on one row", num, sign)
	return nil, nil
}

// SignPoint is the middle of the sign drawn on the row numbered num.
func (s *Scene) SignPoint(num, sign string) render.Point {
	s.T.Helper()
	_, g := s.SignOnRow(num, sign)
	return render.Point{X: g.At.X + g.Run.Advance/2, Y: g.At.Y - g.Run.Ascent/2}
}

// FindRole is the first node in the accessibility tree with role and named label, or nil.
func (s *Scene) FindRole(role render.Role, label string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Role == role && n.Name == label {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.Access())
}

// NodeRole is FindRole's node; the test fails without one.
func (s *Scene) NodeRole(role render.Role, label string) *render.AccessNode {
	s.T.Helper()
	n := s.FindRole(role, label)
	if n == nil {
		s.T.Fatalf("no %s labelled %q", role, label)
	}
	return n
}

// Find is the first node in the accessibility tree named label, or nil.
func (s *Scene) Find(label string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n == nil {
			return nil
		}
		if n.Name == label {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.Access())
}

// Node is the first node in the accessibility tree named label; the test fails without one.
func (s *Scene) Node(label string) *render.AccessNode {
	s.T.Helper()
	n := s.Find(label)
	if n == nil {
		s.T.Fatalf("nothing labelled %q", label)
	}
	return n
}

// Count is how many nodes in the accessibility tree are named label.
func (s *Scene) Count(label string) int {
	n := 0
	var walk func(*render.AccessNode)
	walk = func(a *render.AccessNode) {
		if a.Name == label {
			n++
		}
		for _, k := range a.Children {
			walk(k)
		}
	}
	walk(s.Access())
	return n
}

// Centre is the middle of r.
func Centre(r render.Rect) render.Point {
	return render.Point{X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2}
}

// Within is the first node inside the one whose label starts with in whose own label starts with label.
func (s *Scene) Within(in, label string) *render.AccessNode {
	s.T.Helper()
	var find func(n *render.AccessNode, name string) *render.AccessNode
	find = func(n *render.AccessNode, name string) *render.AccessNode {
		if strings.HasPrefix(n.Name, name) {
			return n
		}
		for _, k := range n.Children {
			if f := find(k, name); f != nil {
				return f
			}
		}
		return nil
	}
	parent := find(s.Access(), in)
	if parent == nil {
		s.T.Fatalf("nothing named %q", in)
	}
	n := find(parent, label)
	if n == nil {
		s.T.Fatalf("nothing named %q in %q", label, in)
	}
	return n
}

// Has says whether the frame has every one of texts.
func (s *Scene) Has(texts ...string) bool {
	frame := s.Text()
	for _, t := range texts {
		if !strings.Contains(frame, t) {
			return false
		}
	}
	return true
}

// StartCard is the start view's button labelled label.
func (s *Scene) StartCard(label string) *render.AccessNode {
	s.T.Helper()
	var found *render.AccessNode
	var find func(n *render.AccessNode)
	find = func(n *render.AccessNode) {
		if n.Name == label && n.Role == render.RoleButton && n.Bounds.Min.X >= 0 {
			found = n
		}
		for _, k := range n.Children {
			find(k)
		}
	}
	find(s.Access())
	if found == nil {
		s.T.Fatalf("no start card %q:\n%s", label, s.Text())
	}
	return found
}

// CountPrefix is how many nodes in the accessibility tree have a name starting with prefix.
func (s *Scene) CountPrefix(prefix string) int {
	n := 0
	var walk func(*render.AccessNode)
	walk = func(a *render.AccessNode) {
		if strings.HasPrefix(a.Name, prefix) {
			n++
		}
		for _, k := range a.Children {
			walk(k)
		}
	}
	walk(s.Access())
	return n
}
