package diagram

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/render"
)

const (
	seqGap      = 80 // least room between two participants' boxes
	seqPadX     = 14
	seqPadY     = 9
	seqMinW     = 120
	seqLoopW    = 30 // a message to itself
	seqLoopH    = 18
	seqActW     = 10
	seqMsgMaxW  = 220
	seqNoteMaxW = 170
	actorFigure = 30
)

type seqMsg struct {
	item           int
	from, to       int
	x0, x1, y      float32
	label          block
	self           bool
	number         int
	labelX, labelY float32 // a self message's label, from its left
}

type seqNote struct {
	rect render.Rect
	text block
}

type seqBlock struct {
	kind, text string
	y0, y1     float32
	elses      []seqElse
	rect       render.Rect
	parent     int
	has        bool
}

type seqElse struct {
	y    float32
	text string
}

type seqAct struct {
	p, depth int
	y0, y1   float32
}

func layoutSequence(s *Sequence, m *measure, f Fonts) *Layout {
	l := &Layout{}
	np := len(s.Participants)
	idx := map[string]int{}
	labels := make([]block, np)
	widths := make([]float32, np)
	var boxH float32
	heights := make([]float32, np)
	for i, p := range s.Participants {
		idx[p.ID] = i
		labels[i] = m.text(p.Label, f.Label, 160)
		widths[i] = max(labels[i].w+2*seqPadX, seqMinW)
		h := labels[i].h + 2*seqPadY
		if p.Actor {
			h += actorFigure
		}
		heights[i] = h
		boxH = max(boxH, h)
	}
	msgLabel := func(it SeqItem, n int) block {
		t := it.Text
		if s.Autonumber {
			t = fmt.Sprint(n) + ". " + t
		}
		return m.text(t, f.Small, seqMsgMaxW)
	}
	noteText := func(it SeqItem) block { return m.text(it.Text, f.Small, seqNoteMaxW) }

	// columns: each participant's centre, far enough apart for the boxes and for what's between them
	cx := make([]float32, np)
	for i := 1; i < np; i++ {
		cx[i] = cx[i-1] + widths[i-1]/2 + widths[i]/2 + seqGap
	}
	type need struct {
		a, b int
		d    float32
	}
	var needs []need
	n := 0
	for _, it := range s.Items {
		switch it.Kind {
		case Message:
			n++
			a, b := idx[it.From], idx[it.To]
			lb := msgLabel(it, n)
			if a == b {
				if a+1 < np {
					needs = append(needs, need{a, a + 1, seqLoopW + lb.w + 24 + widths[a+1]/2})
				}
				continue
			}
			needs = append(needs, need{min(a, b), max(a, b), lb.w + 36})
		case Note:
			a := idx[it.From]
			w := noteText(it).w + 2*10
			switch {
			case it.Side == RightOf && a+1 < np:
				needs = append(needs, need{a, a + 1, w + 24 + widths[a+1]/2})
			case it.Side == LeftOf && a > 0:
				needs = append(needs, need{a - 1, a, w + 24 + widths[a-1]/2})
			case it.Side == Over && it.To != "":
				b := idx[it.To]
				needs = append(needs, need{min(a, b), max(a, b), w - 40})
			}
		}
	}
	slices.SortStableFunc(needs, func(x, y need) int { return (x.b - x.a) - (y.b - y.a) })
	for range 2 {
		for _, nd := range needs {
			if d := nd.d - (cx[nd.b] - cx[nd.a]); d > 0 {
				for k := nd.b; k < np; k++ {
					cx[k] += d
				}
			}
		}
	}

	// rows
	y := boxH + 26
	var msgs []seqMsg
	var notes []seqNote
	var blocks []seqBlock
	var acts []seqAct
	var open []int
	active := make([][]int, np) // per participant, its open activations
	activeAt := func(p int) float32 {
		return float32(len(active[p])) * seqActW / 2
	}
	activate := func(p int, at float32) {
		acts = append(acts, seqAct{p: p, depth: len(active[p]), y0: at})
		active[p] = append(active[p], len(acts)-1)
	}
	deactivate := func(p int, at float32) {
		if k := len(active[p]); k > 0 {
			acts[active[p][k-1]].y1 = at
			active[p] = active[p][:k-1]
		}
	}
	touch := func(r render.Rect) {
		for _, b := range open {
			bl := &blocks[b]
			if !bl.has {
				bl.rect, bl.has = r, true
				continue
			}
			bl.rect = union(bl.rect, r)
		}
	}
	n = 0
	for i, it := range s.Items {
		switch it.Kind {
		case Message:
			n++
			a, b := idx[it.From], idx[it.To]
			lb := msgLabel(it, n)
			ms := seqMsg{item: i, from: a, to: b, label: lb, number: n}
			y += lb.h + 6
			if a == b {
				ms.self = true
				x := cx[a] + activeAt(a)
				ms.x0, ms.x1, ms.y = x, x, y
				ms.labelX, ms.labelY = x+8, y-6-lb.h
				if it.On {
					activate(b, y+seqLoopH-4)
				}
				touch(render.Rect{Min: render.Point{X: cx[a] - widths[a]/2, Y: ms.labelY}, Max: render.Point{X: max(x+max(seqLoopW+4, lb.w+12), cx[a]+widths[a]/2), Y: y + seqLoopH}})
				y += seqLoopH + 14
			} else {
				ms.y = y
				dir := float32(1)
				if b < a {
					dir = -1
				}
				ms.x0 = cx[a] + dir*activeAt(a)
				if it.On {
					activate(b, y-4)
				}
				ms.x1 = cx[b] - dir*activeAt(b)
				lo, hi := min(a, b), max(a, b)
				touch(render.Rect{Min: render.Point{X: cx[lo] - widths[lo]/2, Y: y - lb.h - 6}, Max: render.Point{X: cx[hi] + widths[hi]/2, Y: y + 4}})
				y += 22
			}
			if it.Off {
				deactivate(a, ms.y+8)
			}
			msgs = append(msgs, ms)
		case Note:
			tb := noteText(it)
			w, h := tb.w+20, tb.h+12
			a := idx[it.From]
			var r render.Rect
			switch it.Side {
			case RightOf:
				r = render.XYWH(cx[a]+activeAt(a)+12, y, w, h)
			case LeftOf:
				r = render.XYWH(cx[a]-activeAt(a)-12-w, y, w, h)
			default:
				x0, x1 := cx[a], cx[a]
				if it.To != "" {
					b := idx[it.To]
					x0, x1 = min(cx[a], cx[b]), max(cx[a], cx[b])
				}
				w = max(w, x1-x0+44)
				r = render.XYWH((x0+x1)/2-w/2, y, w, h)
			}
			notes = append(notes, seqNote{r, tb})
			touch(r)
			y += h + 14
		case BlockStart:
			parent := -1
			if len(open) > 0 {
				parent = open[len(open)-1]
			}
			blocks = append(blocks, seqBlock{kind: it.Block, text: it.Text, y0: y, parent: parent})
			open = append(open, len(blocks)-1)
			if it.Block != "box" {
				y += 30
			}
		case BlockElse:
			if len(open) > 0 {
				b := &blocks[open[len(open)-1]]
				y += 4
				b.elses = append(b.elses, seqElse{y, it.Text})
				y += 26
			}
		case BlockEnd:
			if len(open) > 0 {
				b := &blocks[open[len(open)-1]]
				open = open[:len(open)-1]
				if b.kind != "box" {
					y += 6
				}
				b.y1 = y
				if b.kind != "box" {
					y += 14
				}
			}
		case Activate:
			activate(idx[it.From], y-4)
		case Deactivate:
			deactivate(idx[it.From], y+4)
		}
	}
	end := y - 8
	for p := range active {
		for len(active[p]) > 0 {
			acts[active[p][len(active[p])-1]].y1 = end
			active[p] = active[p][:len(active[p])-1]
		}
	}

	// block boxes, outermost last so each wraps those inside it
	tabW := func(b *seqBlock) float32 {
		w := m.width(strings.ToUpper(b.kind), f.Title) + 16
		if b.text != "" {
			w += m.width("["+b.text+"]", f.Small) + 12
		}
		return w
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		b := &blocks[i]
		if b.kind == "box" {
			continue
		}
		if !b.has {
			b.rect = render.XYWH(cx[0], b.y0, 0, 0)
		}
		pad := float32(16)
		r := render.Rect{Min: render.Point{X: b.rect.Min.X - pad, Y: b.y0}, Max: render.Point{X: b.rect.Max.X + pad, Y: b.y1}}
		for _, e := range b.elses {
			r.Max.X = max(r.Max.X, r.Min.X+m.width("["+e.text+"]", f.Small)+24)
		}
		r.Max.X = max(r.Max.X, r.Min.X+tabW(b)+20)
		b.rect = r
		if b.parent >= 0 {
			pb := &blocks[b.parent]
			if pb.has {
				pb.rect = union(pb.rect, r)
			} else {
				pb.rect, pb.has = r, true
			}
		}
	}

	// participants and lifelines
	for i, p := range s.Participants {
		l.Nodes = append(l.Nodes, Box{ID: p.ID, Label: p.Label, Rect: render.XYWH(cx[i]-widths[i]/2, boxH-heights[i], widths[i], heights[i])})
	}
	for _, ms := range msgs {
		l.Edges = append(l.Edges, Route{Edge: ms.item, From: ms.from, To: ms.to, Points: []render.Point{{X: ms.x0, Y: ms.y}, {X: ms.x1, Y: ms.y}}, Loop: ms.self,
			Line: Solid, EndMark: s.Items[ms.item].Head})
	}

	var out []prim
	for i := range s.Participants {
		var lp render.Path
		lp.MoveTo(pt(cx[i], boxH)).LineTo(pt(cx[i], end+8))
		out = append(out, prim{kind: primDashed, pts: flatten(&lp), dash: [2]float32{4, 4}, stroke: roleLifeline, width: 1})
	}
	for _, a := range acts {
		x := cx[a.p] - seqActW/2 + float32(a.depth)*seqActW/2
		out = append(out, prim{kind: primRect, rect: render.XYWH(x, a.y0, seqActW, max(a.y1-a.y0, 8)), radius: 2, fill: roleActivation, stroke: roleNodeStroke, width: 1})
	}
	for _, b := range blocks {
		if b.kind == "box" {
			continue
		}
		if b.kind == "rect" {
			out = append(out, prim{kind: primRect, rect: b.rect, radius: 6, fill: roleGroupFill})
			continue
		}
		out = append(out, prim{kind: primRect, rect: b.rect, radius: 6, stroke: roleBlockStroke, width: 1})
		kind := m.text(strings.ToUpper(b.kind), f.Title, 400)
		tab := render.XYWH(b.rect.Min.X, b.rect.Min.Y, kind.w+16, kind.h+8)
		var tp render.Path
		tp.MoveTo(pt(tab.Min.X+6, tab.Min.Y)).LineTo(pt(tab.Max.X, tab.Min.Y)).LineTo(pt(tab.Max.X, tab.Max.Y-6)).LineTo(pt(tab.Max.X-6, tab.Max.Y)).
			LineTo(pt(tab.Min.X, tab.Max.Y)).LineTo(pt(tab.Min.X, tab.Min.Y+6)).QuadTo(pt(tab.Min.X, tab.Min.Y), pt(tab.Min.X+6, tab.Min.Y)).Close()
		out = append(out, prim{kind: primPath, path: &tp, fill: roleBlockTab, stroke: roleBlockStroke, width: 1})
		out = append(out, kind.at((tab.Min.X+tab.Max.X)/2, (tab.Min.Y+tab.Max.Y)/2, roleBlockText)...)
		if b.text != "" {
			cond := m.text("["+b.text+"]", f.Small, 400)
			// on a ground of its own, so a lifeline or activation under it doesn't run through it
			out = append(out, prim{kind: primRect, rect: render.XYWH(tab.Max.X+4, tab.Min.Y+2, cond.w+8, tab.Dy()-4), radius: 3, fill: roleLabelFill})
			out = append(out, cond.left(tab.Max.X+8, tab.Min.Y+(tab.Dy()-cond.h)/2, roleBlockText)...)
		}
		for _, e := range b.elses {
			var dl render.Path
			dl.MoveTo(pt(b.rect.Min.X, e.y)).LineTo(pt(b.rect.Max.X, e.y))
			out = append(out, prim{kind: primDashed, pts: flatten(&dl), dash: [2]float32{4, 3}, stroke: roleBlockStroke, width: 1})
			if e.text != "" {
				cond := m.text("["+e.text+"]", f.Small, 400)
				out = append(out, cond.left(b.rect.Min.X+10, e.y+5, roleBlockText)...)
			}
		}
	}
	for _, ms := range msgs {
		it := s.Items[ms.item]
		style := Solid
		if it.Dotted {
			style = Dotted
		}
		var path render.Path
		if ms.self {
			x, y := ms.x0, ms.y
			path.MoveTo(pt(x, y)).LineTo(pt(x+seqLoopW-8, y)).QuadTo(pt(x+seqLoopW, y), pt(x+seqLoopW, y+8)).
				LineTo(pt(x+seqLoopW, y+seqLoopH-8)).QuadTo(pt(x+seqLoopW, y+seqLoopH), pt(x+seqLoopW-8, y+seqLoopH)).LineTo(pt(x+1, y+seqLoopH))
			out = append(out, ms.label.left(ms.labelX, ms.labelY, roleEdgeText)...)
			l.cut(ms.label, render.XYWH(ms.labelX, ms.labelY, ms.label.w, ms.label.h))
		} else {
			path.MoveTo(pt(ms.x0, ms.y)).LineTo(pt(ms.x1, ms.y))
			out = append(out, ms.label.at((ms.x0+ms.x1)/2, ms.y-6-ms.label.h/2, roleEdgeText)...)
			l.cut(ms.label, render.XYWH((ms.x0+ms.x1)/2-ms.label.w/2, ms.y-6-ms.label.h, ms.label.w, ms.label.h))
		}
		end := it.Head
		if it.Async {
			end = NoMarker
		}
		start := NoMarker
		if it.Both {
			start = ArrowHead
		}
		out = append(out, linePrims(&path, style, start, end)...)
		if it.Async {
			out = append(out, openArrow(&path)...)
		}
	}
	for _, nt := range notes {
		r := nt.rect
		var np render.Path
		fold := float32(8)
		np.MoveTo(pt(r.Min.X+3, r.Min.Y)).LineTo(pt(r.Max.X-fold, r.Min.Y)).LineTo(pt(r.Max.X, r.Min.Y+fold)).LineTo(pt(r.Max.X, r.Max.Y-3)).
			QuadTo(r.Max, pt(r.Max.X-3, r.Max.Y)).LineTo(pt(r.Min.X+3, r.Max.Y)).QuadTo(pt(r.Min.X, r.Max.Y), pt(r.Min.X, r.Max.Y-3)).
			LineTo(pt(r.Min.X, r.Min.Y+3)).QuadTo(r.Min, pt(r.Min.X+3, r.Min.Y)).Close()
		out = append(out, prim{kind: primPath, path: &np, fill: roleNoteFill, stroke: roleNoteStroke, width: 1})
		out = append(out, nt.text.at((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, roleNoteText)...)
		l.cut(nt.text, r)
	}
	for i, p := range s.Participants {
		r := l.Nodes[i].Rect
		l.cut(labels[i], r)
		if p.Actor {
			out = append(out, actorPrims(cx[i], r.Min.Y+2)...)
			out = append(out, labels[i].at(cx[i], r.Min.Y+actorFigure+(r.Dy()-actorFigure)/2, roleNodeText)...)
			continue
		}
		out = append(out, prim{kind: primRect, rect: r, radius: 6, fill: roleNodeFill, stroke: roleNodeStroke, width: 1})
		out = append(out, labels[i].at(cx[i], (r.Min.Y+r.Max.Y)/2, roleNodeText)...)
	}

	// normalise to the margin
	bb := render.Rect{Min: render.Point{X: l.Nodes[0].Rect.Min.X, Y: 0}, Max: render.Point{X: l.Nodes[np-1].Rect.Max.X, Y: end + 8}}
	for _, nt := range notes {
		bb = union(bb, nt.rect)
	}
	for _, b := range blocks {
		if b.kind != "box" {
			bb = union(bb, b.rect)
		}
	}
	for _, ms := range msgs {
		if ms.self {
			bb = union(bb, render.XYWH(ms.labelX, ms.labelY, max(ms.label.w, seqLoopW), ms.label.h))
		}
	}
	d := render.Point{X: margin - bb.Min.X, Y: margin - bb.Min.Y}
	for i := range out {
		shift(&out[i], d)
	}
	for i := range l.Nodes {
		l.Nodes[i].Rect = render.Rect{Min: add(l.Nodes[i].Rect.Min, d), Max: add(l.Nodes[i].Rect.Max, d)}
	}
	for i := range l.Truncated {
		l.Truncated[i].Rect = render.Rect{Min: add(l.Truncated[i].Rect.Min, d), Max: add(l.Truncated[i].Rect.Max, d)}
	}
	for i := range l.Edges {
		for j := range l.Edges[i].Points {
			l.Edges[i].Points[j] = add(l.Edges[i].Points[j], d)
		}
	}
	l.Size = render.Point{X: bb.Dx() + 2*margin, Y: bb.Dy() + 2*margin}
	l.prims = out
	return l
}

func union(a, b render.Rect) render.Rect {
	return render.Rect{Min: render.Point{X: min(a.Min.X, b.Min.X), Y: min(a.Min.Y, b.Min.Y)}, Max: render.Point{X: max(a.Max.X, b.Max.X), Y: max(a.Max.Y, b.Max.Y)}}
}

func shift(p *prim, d render.Point) {
	p.rect = render.Rect{Min: add(p.rect.Min, d), Max: add(p.rect.Max, d)}
	p.at = add(p.at, d)
	for i := range p.pts {
		p.pts[i] = add(p.pts[i], d)
	}
	if p.path != nil {
		for i := range p.path.Segs {
			for j := range p.path.Segs[i].Pts {
				p.path.Segs[i].Pts[j] = add(p.path.Segs[i].Pts[j], d)
			}
		}
	}
}

// openArrow is the two strokes of an async message's open arrowhead at its path's end.
func openArrow(path *render.Path) []prim {
	s := path.Segs[len(path.Segs)-1]
	tip := s.Pts[0]
	d := endTangent(path)
	n := pt(-d.Y, d.X)
	base := sub(tip, mul(d, arrowLen))
	var p render.Path
	p.MoveTo(add(base, mul(n, arrowHalf))).LineTo(tip).LineTo(sub(base, mul(n, arrowHalf)))
	return []prim{{kind: primPath, path: &p, stroke: roleEdge, width: 1.3}}
}

// actorPrims is a stick figure, its head's top at (cx, top).
func actorPrims(cx, top float32) []prim {
	head := render.Ellipse(render.XYWH(cx-5.5, top, 11, 11))
	var body render.Path
	body.MoveTo(pt(cx, top+11)).LineTo(pt(cx, top+20)).
		MoveTo(pt(cx-8, top+14)).LineTo(pt(cx+8, top+14)).
		MoveTo(pt(cx, top+20)).LineTo(pt(cx-6, top+27)).
		MoveTo(pt(cx, top+20)).LineTo(pt(cx+6, top+27))
	return []prim{{kind: primPath, path: head, fill: roleNodeFill, stroke: roleNodeText, width: 1.3}, {kind: primPath, path: &body, stroke: roleNodeText, width: 1.3}}
}
