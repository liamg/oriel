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

// Room the columns leave around what sits between two lifelines.
const (
	seqBesideGap  = 24 // between a self message or a side note and the next lifeline's box
	seqLabelRoom  = 36 // around a message's label, between its two lifelines
	seqNotePadX   = 10 // a note's text from its sides, when measuring its column room
	seqOverInside = 40 // how far a note over two lifelines may reach past them
)

// seqLayout is a sequence diagram as it's laid out, phase by phase: the participants measured, their columns
// spaced (columns), the items placed down the page (rows), the blocks boxed (boxBlocks), then drawn (draw) and moved
// to the margin (normalise).
type seqLayout struct {
	s *Sequence
	m *measure
	f Fonts
	l *Layout

	np      int
	idx     map[string]int // a participant's index, by ID
	labels  []block
	widths  []float32
	heights []float32
	boxH    float32   // the tallest participant box
	cx      []float32 // each participant's centre

	msgs   []seqMsg
	notes  []seqNote
	blocks []seqBlock
	acts   []seqAct
	open   []int   // the blocks open at the current row, outermost first
	active [][]int // per participant, its open activations
	end    float32 // where the last row ends
	out    []prim
}

func layoutSequence(s *Sequence, m *measure, f Fonts) *Layout {
	q := &seqLayout{s: s, m: m, f: f, l: &Layout{}}
	q.participants()
	q.columns()
	q.rows()
	q.boxBlocks()
	q.draw()
	q.normalise()
	return q.l
}

// participants measures each participant's box.
func (q *seqLayout) participants() {
	q.np = len(q.s.Participants)
	q.idx = map[string]int{}
	q.labels = make([]block, q.np)
	q.widths = make([]float32, q.np)
	q.heights = make([]float32, q.np)
	for i, p := range q.s.Participants {
		q.idx[p.ID] = i
		q.labels[i] = q.m.text(p.Label, q.f.Label, 160)
		q.widths[i] = max(q.labels[i].w+2*seqPadX, seqMinW)
		h := q.labels[i].h + 2*seqPadY
		if p.Actor {
			h += actorFigure
		}
		q.heights[i] = h
		q.boxH = max(q.boxH, h)
	}
}

func (q *seqLayout) msgLabel(it SeqItem, n int) block {
	t := it.Text
	if q.s.Autonumber {
		t = fmt.Sprint(n) + ". " + t
	}
	return q.m.text(t, q.f.Small, seqMsgMaxW)
}

func (q *seqLayout) noteText(it SeqItem) block { return q.m.text(it.Text, q.f.Small, seqNoteMaxW) }

// seqNeed is room two columns need between their centres.
type seqNeed struct {
	a, b int
	d    float32
}

// columns places each participant's centre, far enough apart for the boxes and for what's between them.
func (q *seqLayout) columns() {
	np, widths := q.np, q.widths
	q.cx = make([]float32, np)
	cx := q.cx
	for i := 1; i < np; i++ {
		cx[i] = cx[i-1] + widths[i-1]/2 + widths[i]/2 + seqGap
	}
	needs := q.needs()
	slices.SortStableFunc(needs, func(x, y seqNeed) int { return (x.b - x.a) - (y.b - y.a) })
	for range 2 {
		for _, nd := range needs {
			if d := nd.d - (cx[nd.b] - cx[nd.a]); d > 0 {
				for k := nd.b; k < np; k++ {
					cx[k] += d
				}
			}
		}
	}
}

// needs is the room each message and note asks for between the columns it spans.
func (q *seqLayout) needs() []seqNeed {
	np, widths, idx := q.np, q.widths, q.idx
	var needs []seqNeed
	n := 0
	for _, it := range q.s.Items {
		switch it.Kind {
		case Message:
			n++
			a, b := idx[it.From], idx[it.To]
			lb := q.msgLabel(it, n)
			if a == b {
				if a+1 < np {
					needs = append(needs, seqNeed{a, a + 1, seqLoopW + lb.w + seqBesideGap + widths[a+1]/2})
				}
				continue
			}
			needs = append(needs, seqNeed{min(a, b), max(a, b), lb.w + seqLabelRoom})
		case Note:
			a := idx[it.From]
			w := q.noteText(it).w + 2*seqNotePadX
			switch {
			case it.Side == RightOf && a+1 < np:
				needs = append(needs, seqNeed{a, a + 1, w + seqBesideGap + widths[a+1]/2})
			case it.Side == LeftOf && a > 0:
				needs = append(needs, seqNeed{a - 1, a, w + seqBesideGap + widths[a-1]/2})
			case it.Side == Over && it.To != "":
				b := idx[it.To]
				needs = append(needs, seqNeed{min(a, b), max(a, b), w - seqOverInside})
			}
		}
	}
	return needs
}

func (q *seqLayout) activeAt(p int) float32 { return float32(len(q.active[p])) * seqActW / 2 }

func (q *seqLayout) activate(p int, at float32) {
	q.acts = append(q.acts, seqAct{p: p, depth: len(q.active[p]), y0: at})
	q.active[p] = append(q.active[p], len(q.acts)-1)
}

func (q *seqLayout) deactivate(p int, at float32) {
	if k := len(q.active[p]); k > 0 {
		q.acts[q.active[p][k-1]].y1 = at
		q.active[p] = q.active[p][:k-1]
	}
}

// touch grows every open block to take in r.
func (q *seqLayout) touch(r render.Rect) {
	for _, b := range q.open {
		bl := &q.blocks[b]
		if !bl.has {
			bl.rect, bl.has = r, true
			continue
		}
		bl.rect = union(bl.rect, r)
	}
}

// rows places the items down the page, one after another, and ends the activations still open.
func (q *seqLayout) rows() {
	q.active = make([][]int, q.np)
	y := q.boxH + 26
	n := 0
	for i, it := range q.s.Items {
		switch it.Kind {
		case Message:
			n++
			y = q.message(i, it, n, y)
		case Note:
			y = q.note(it, y)
		case BlockStart:
			parent := -1
			if len(q.open) > 0 {
				parent = q.open[len(q.open)-1]
			}
			q.blocks = append(q.blocks, seqBlock{kind: it.Block, text: it.Text, y0: y, parent: parent})
			q.open = append(q.open, len(q.blocks)-1)
			if it.Block != "box" {
				y += 30
			}
		case BlockElse:
			if len(q.open) > 0 {
				b := &q.blocks[q.open[len(q.open)-1]]
				y += 4
				b.elses = append(b.elses, seqElse{y, it.Text})
				y += 26
			}
		case BlockEnd:
			if len(q.open) > 0 {
				b := &q.blocks[q.open[len(q.open)-1]]
				q.open = q.open[:len(q.open)-1]
				if b.kind != "box" {
					y += 6
				}
				b.y1 = y
				if b.kind != "box" {
					y += 14
				}
			}
		case Activate:
			q.activate(q.idx[it.From], y-4)
		case Deactivate:
			q.deactivate(q.idx[it.From], y+4)
		}
	}
	q.end = y - 8
	for p := range q.active {
		for len(q.active[p]) > 0 {
			q.acts[q.active[p][len(q.active[p])-1]].y1 = q.end
			q.active[p] = q.active[p][:len(q.active[p])-1]
		}
	}
}

// message places item i, the nth message, at y, and returns where the next row starts.
func (q *seqLayout) message(i int, it SeqItem, n int, y float32) float32 {
	cx, widths := q.cx, q.widths
	a, b := q.idx[it.From], q.idx[it.To]
	lb := q.msgLabel(it, n)
	ms := seqMsg{item: i, from: a, to: b, label: lb, number: n}
	y += lb.h + 6
	if a == b {
		ms.self = true
		x := cx[a] + q.activeAt(a)
		ms.x0, ms.x1, ms.y = x, x, y
		ms.labelX, ms.labelY = x+8, y-6-lb.h
		if it.On {
			q.activate(b, y+seqLoopH-4)
		}
		q.touch(render.Rect{Min: render.Point{X: cx[a] - widths[a]/2, Y: ms.labelY}, Max: render.Point{X: max(x+max(seqLoopW+4, lb.w+12), cx[a]+widths[a]/2), Y: y + seqLoopH}})
		y += seqLoopH + 14
	} else {
		ms.y = y
		dir := float32(1)
		if b < a {
			dir = -1
		}
		ms.x0 = cx[a] + dir*q.activeAt(a)
		if it.On {
			q.activate(b, y-4)
		}
		ms.x1 = cx[b] - dir*q.activeAt(b)
		lo, hi := min(a, b), max(a, b)
		q.touch(render.Rect{Min: render.Point{X: cx[lo] - widths[lo]/2, Y: y - lb.h - 6}, Max: render.Point{X: cx[hi] + widths[hi]/2, Y: y + 4}})
		y += 22
	}
	if it.Off {
		q.deactivate(a, ms.y+8)
	}
	q.msgs = append(q.msgs, ms)
	return y
}

// note places a note at y, and returns where the next row starts.
func (q *seqLayout) note(it SeqItem, y float32) float32 {
	cx := q.cx
	tb := q.noteText(it)
	w, h := tb.w+20, tb.h+12
	a := q.idx[it.From]
	var r render.Rect
	switch it.Side {
	case RightOf:
		r = render.XYWH(cx[a]+q.activeAt(a)+12, y, w, h)
	case LeftOf:
		r = render.XYWH(cx[a]-q.activeAt(a)-12-w, y, w, h)
	default:
		x0, x1 := cx[a], cx[a]
		if it.To != "" {
			b := q.idx[it.To]
			x0, x1 = min(cx[a], cx[b]), max(cx[a], cx[b])
		}
		w = max(w, x1-x0+44)
		r = render.XYWH((x0+x1)/2-w/2, y, w, h)
	}
	q.notes = append(q.notes, seqNote{r, tb})
	q.touch(r)
	return y + h + 14
}

// boxBlocks sizes the block boxes, outermost last so each wraps those inside it.
func (q *seqLayout) boxBlocks() {
	m, f := q.m, q.f
	tabW := func(b *seqBlock) float32 {
		w := m.width(strings.ToUpper(b.kind), f.Title) + 16
		if b.text != "" {
			w += m.width("["+b.text+"]", f.Small) + 12
		}
		return w
	}
	for i := len(q.blocks) - 1; i >= 0; i-- {
		b := &q.blocks[i]
		if b.kind == "box" {
			continue
		}
		if !b.has {
			b.rect = render.XYWH(q.cx[0], b.y0, 0, 0)
		}
		pad := float32(16)
		r := render.Rect{Min: render.Point{X: b.rect.Min.X - pad, Y: b.y0}, Max: render.Point{X: b.rect.Max.X + pad, Y: b.y1}}
		for _, e := range b.elses {
			r.Max.X = max(r.Max.X, r.Min.X+m.width("["+e.text+"]", f.Small)+24)
		}
		r.Max.X = max(r.Max.X, r.Min.X+tabW(b)+20)
		b.rect = r
		if b.parent >= 0 {
			pb := &q.blocks[b.parent]
			if pb.has {
				pb.rect = union(pb.rect, r)
			} else {
				pb.rect, pb.has = r, true
			}
		}
	}
}

// draw lays out the participants and messages as the Layout's nodes and edges, and draws everything, back to front.
func (q *seqLayout) draw() {
	s, l, cx, widths := q.s, q.l, q.cx, q.widths
	for i, p := range s.Participants {
		l.Nodes = append(l.Nodes, Box{ID: p.ID, Label: p.Label, Rect: render.XYWH(cx[i]-widths[i]/2, q.boxH-q.heights[i], widths[i], q.heights[i])})
	}
	for _, ms := range q.msgs {
		l.Edges = append(l.Edges, Route{Edge: ms.item, From: ms.from, To: ms.to, Points: []render.Point{{X: ms.x0, Y: ms.y}, {X: ms.x1, Y: ms.y}}, Loop: ms.self,
			Line: Solid, EndMark: s.Items[ms.item].Head})
	}
	for i := range s.Participants {
		var lp render.Path
		lp.MoveTo(pt(cx[i], q.boxH)).LineTo(pt(cx[i], q.end+8))
		q.out = append(q.out, prim{kind: primDashed, pts: flatten(&lp), dash: [2]float32{4, 4}, stroke: roleLifeline, width: 1})
	}
	for _, a := range q.acts {
		x := cx[a.p] - seqActW/2 + float32(a.depth)*seqActW/2
		q.out = append(q.out, prim{kind: primRect, rect: render.XYWH(x, a.y0, seqActW, max(a.y1-a.y0, 8)), radius: 2, fill: roleActivation, stroke: roleNodeStroke, width: 1})
	}
	for _, b := range q.blocks {
		q.drawBlock(b)
	}
	for _, ms := range q.msgs {
		q.drawMessage(ms)
	}
	for _, nt := range q.notes {
		q.drawNote(nt)
	}
	for i, p := range s.Participants {
		r := l.Nodes[i].Rect
		l.cut(q.labels[i], r)
		if p.Actor {
			q.out = append(q.out, actorPrims(cx[i], r.Min.Y+2)...)
			q.out = append(q.out, q.labels[i].at(cx[i], r.Min.Y+actorFigure+(r.Dy()-actorFigure)/2, roleNodeText)...)
			continue
		}
		q.out = append(q.out, prim{kind: primRect, rect: r, radius: 6, fill: roleNodeFill, stroke: roleNodeStroke, width: 1})
		q.out = append(q.out, q.labels[i].at(cx[i], (r.Min.Y+r.Max.Y)/2, roleNodeText)...)
	}
}

// drawBlock draws a block's box, its tab and condition, and its else lines; a box block draws nothing.
func (q *seqLayout) drawBlock(b seqBlock) {
	m, f := q.m, q.f
	if b.kind == "box" {
		return
	}
	if b.kind == "rect" {
		q.out = append(q.out, prim{kind: primRect, rect: b.rect, radius: 6, fill: roleGroupFill})
		return
	}
	q.out = append(q.out, prim{kind: primRect, rect: b.rect, radius: 6, stroke: roleBlockStroke, width: 1})
	kind := m.text(strings.ToUpper(b.kind), f.Title, 400)
	tab := render.XYWH(b.rect.Min.X, b.rect.Min.Y, kind.w+16, kind.h+8)
	var tp render.Path
	tp.MoveTo(pt(tab.Min.X+6, tab.Min.Y)).LineTo(pt(tab.Max.X, tab.Min.Y)).LineTo(pt(tab.Max.X, tab.Max.Y-6)).LineTo(pt(tab.Max.X-6, tab.Max.Y)).
		LineTo(pt(tab.Min.X, tab.Max.Y)).LineTo(pt(tab.Min.X, tab.Min.Y+6)).QuadTo(pt(tab.Min.X, tab.Min.Y), pt(tab.Min.X+6, tab.Min.Y)).Close()
	q.out = append(q.out, prim{kind: primPath, path: &tp, fill: roleBlockTab, stroke: roleBlockStroke, width: 1})
	q.out = append(q.out, kind.at((tab.Min.X+tab.Max.X)/2, (tab.Min.Y+tab.Max.Y)/2, roleBlockText)...)
	if b.text != "" {
		cond := m.text("["+b.text+"]", f.Small, 400)
		// on a ground of its own, so a lifeline or activation under it doesn't run through it
		q.out = append(q.out, prim{kind: primRect, rect: render.XYWH(tab.Max.X+4, tab.Min.Y+2, cond.w+8, tab.Dy()-4), radius: 3, fill: roleLabelFill})
		q.out = append(q.out, cond.left(tab.Max.X+8, tab.Min.Y+(tab.Dy()-cond.h)/2, roleBlockText)...)
	}
	for _, e := range b.elses {
		var dl render.Path
		dl.MoveTo(pt(b.rect.Min.X, e.y)).LineTo(pt(b.rect.Max.X, e.y))
		q.out = append(q.out, prim{kind: primDashed, pts: flatten(&dl), dash: [2]float32{4, 3}, stroke: roleBlockStroke, width: 1})
		if e.text != "" {
			cond := m.text("["+e.text+"]", f.Small, 400)
			q.out = append(q.out, cond.left(b.rect.Min.X+10, e.y+5, roleBlockText)...)
		}
	}
}

// drawMessage draws a message's line or loop, its label and its arrowheads.
func (q *seqLayout) drawMessage(ms seqMsg) {
	it := q.s.Items[ms.item]
	style := Solid
	if it.Dotted {
		style = Dotted
	}
	var path render.Path
	if ms.self {
		x, y := ms.x0, ms.y
		path.MoveTo(pt(x, y)).LineTo(pt(x+seqLoopW-8, y)).QuadTo(pt(x+seqLoopW, y), pt(x+seqLoopW, y+8)).
			LineTo(pt(x+seqLoopW, y+seqLoopH-8)).QuadTo(pt(x+seqLoopW, y+seqLoopH), pt(x+seqLoopW-8, y+seqLoopH)).LineTo(pt(x+1, y+seqLoopH))
		q.out = append(q.out, ms.label.left(ms.labelX, ms.labelY, roleEdgeText)...)
		q.l.cut(ms.label, render.XYWH(ms.labelX, ms.labelY, ms.label.w, ms.label.h))
	} else {
		path.MoveTo(pt(ms.x0, ms.y)).LineTo(pt(ms.x1, ms.y))
		q.out = append(q.out, ms.label.at((ms.x0+ms.x1)/2, ms.y-6-ms.label.h/2, roleEdgeText)...)
		q.l.cut(ms.label, render.XYWH((ms.x0+ms.x1)/2-ms.label.w/2, ms.y-6-ms.label.h, ms.label.w, ms.label.h))
	}
	end := it.Head
	if it.Async {
		end = NoMarker
	}
	start := NoMarker
	if it.Both {
		start = ArrowHead
	}
	q.out = append(q.out, linePrims(&path, style, start, end)...)
	if it.Async {
		q.out = append(q.out, openArrow(&path)...)
	}
}

// drawNote draws a note: a box with a folded corner, and its text.
func (q *seqLayout) drawNote(nt seqNote) {
	r := nt.rect
	var np render.Path
	fold := float32(8)
	np.MoveTo(pt(r.Min.X+3, r.Min.Y)).LineTo(pt(r.Max.X-fold, r.Min.Y)).LineTo(pt(r.Max.X, r.Min.Y+fold)).LineTo(pt(r.Max.X, r.Max.Y-3)).
		QuadTo(r.Max, pt(r.Max.X-3, r.Max.Y)).LineTo(pt(r.Min.X+3, r.Max.Y)).QuadTo(pt(r.Min.X, r.Max.Y), pt(r.Min.X, r.Max.Y-3)).
		LineTo(pt(r.Min.X, r.Min.Y+3)).QuadTo(r.Min, pt(r.Min.X+3, r.Min.Y)).Close()
	q.out = append(q.out, prim{kind: primPath, path: &np, fill: roleNoteFill, stroke: roleNoteStroke, width: 1})
	q.out = append(q.out, nt.text.at((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, roleNoteText)...)
	q.l.cut(nt.text, r)
}

// normalise moves everything to the margin and sizes the Layout to it.
func (q *seqLayout) normalise() {
	l := q.l
	bb := render.Rect{Min: render.Point{X: l.Nodes[0].Rect.Min.X, Y: 0}, Max: render.Point{X: l.Nodes[q.np-1].Rect.Max.X, Y: q.end + 8}}
	for _, nt := range q.notes {
		bb = union(bb, nt.rect)
	}
	for _, b := range q.blocks {
		if b.kind != "box" {
			bb = union(bb, b.rect)
		}
	}
	for _, ms := range q.msgs {
		if ms.self {
			bb = union(bb, render.XYWH(ms.labelX, ms.labelY, max(ms.label.w, seqLoopW), ms.label.h))
		}
	}
	d := render.Point{X: margin - bb.Min.X, Y: margin - bb.Min.Y}
	for i := range q.out {
		shift(&q.out[i], d)
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
	l.prims = q.out
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
