package diagram

import (
	"math"
	"slices"

	"github.com/liamg/oriel/render"
)

// Spacing, in logical pixels.
const (
	nodePadX, nodePadY = 16, 10
	nodeMinW, nodeMinH = 64, 38
	labelMaxW          = 180 // a node label wraps past this
	edgeLabelMaxW      = 150
	groupPad           = 14
	dummyGap           = 14
	margin             = 2
	loopReach          = 26
)

type nodeKind uint8

const (
	kReal nodeKind = iota
	kDummy
	kLabel // a dummy carrying its edge's label
	kSpine // holds a subgraph's place in a rank it has nothing in
	kLeft  // a subgraph's left (TB) or top (LR) border in one rank
	kRight
)

type lnode struct {
	kind         nodeKind
	ref          int // the flowchart node, or the edge for a dummy
	group        int // innermost subgraph; the bordered subgraph for kLeft / kRight
	rank         int
	along        float32 // extent along the order axis (TB: width)
	across       float32 // extent along the rank axis
	trail        float32 // room after it on the order axis (a self loop)
	pos          float32
	bary         float32
	preds, succs []adj
}

type adj struct {
	n int
	w float32
}

type ledge struct {
	u, v     int // real nodes, after reversal u's rank is lower
	minlen   int
	edge     int
	reversed bool
	chain    []int // u, dummies…, v
}

type flowLayout struct {
	f        *Flowchart
	m        *measure
	fonts    Fonts
	vertical bool // TB or BT
	nodes    []lnode
	edges    []ledge
	loops    []int // self-loop edges
	layers   [][]int
	chains   [][]int // each group's ancestors, top first, then itself
	minRank  []int
	maxRank  []int
	labels   []block // per flowchart node
	elabels  []block // per edge
	title    float32 // a group title's height
	titles   []block
	nodeGap  float32
	halfGap  float32
}

func layoutFlow(f *Flowchart, m *measure, fonts Fonts) *Layout {
	g := &flowLayout{f: f, m: m, fonts: fonts, vertical: f.Dir == TB || f.Dir == BT}
	g.nodeGap, g.halfGap = 30, 26
	if !g.vertical {
		g.nodeGap, g.halfGap = 22, 30
	}
	g.build()
	g.breakCycles()
	g.rank()
	g.addDummies()
	g.spans()
	g.order()
	g.borders()
	g.place()
	return g.result()
}

func (g *flowLayout) build() {
	f := g.f
	g.chains = make([][]int, len(f.Groups))
	for i := range f.Groups {
		var c []int
		for p := i; p >= 0; p = f.Groups[p].Parent {
			c = append(c, p)
		}
		slices.Reverse(c)
		g.chains[i] = c
	}
	for _, gr := range f.Groups {
		g.titles = append(g.titles, g.m.text(gr.Title, g.fonts.Title, 400))
	}
	g.title = g.m.text("Hg", g.fonts.Title, 400).h + 8
	for i, n := range f.Nodes {
		b := g.m.text(n.Label, g.fonts.Label, labelMaxW)
		g.labels = append(g.labels, b)
		w, h := nodeSize(n.Shape, b.w, b.h)
		ln := lnode{kind: kReal, ref: i, group: n.Group}
		ln.along, ln.across = w, h
		if !g.vertical {
			ln.along, ln.across = h, w
		}
		g.nodes = append(g.nodes, ln)
	}
	index := map[string]int{}
	for i, n := range f.Nodes {
		index[n.ID] = i
	}
	end := func(id string, first bool) int {
		if i, ok := index[id]; ok {
			return i
		}
		gi := f.group(id)
		for k := range f.Nodes {
			j := k
			if !first {
				j = len(f.Nodes) - 1 - k
			}
			if g.inGroup(f.Nodes[j].Group, gi) {
				return j
			}
		}
		return -1
	}
	for i, e := range f.Edges {
		g.elabels = append(g.elabels, g.m.text(e.Label, g.fonts.Small, edgeLabelMaxW))
		u, v := end(e.From, false), end(e.To, true)
		if u < 0 || v < 0 {
			continue
		}
		if u == v {
			g.loops = append(g.loops, i)
			lw := float32(loopReach + 4)
			if e.Label != "" {
				lw += g.elabels[i].w + 14
			}
			g.nodes[u].trail = max(g.nodes[u].trail, lw)
			continue
		}
		g.edges = append(g.edges, ledge{u: u, v: v, minlen: 2 * max(1, e.Length), edge: i})
	}
}

// inGroup says whether group a is gi or inside it.
func (g *flowLayout) inGroup(a, gi int) bool {
	if gi < 0 {
		return true
	}
	for ; a >= 0; a = g.f.Groups[a].Parent {
		if a == gi {
			return true
		}
	}
	return false
}

// nodeSize is a node's outline around a w×h label.
func nodeSize(s Shape, tw, th float32) (float32, float32) {
	w, h := max(tw+2*nodePadX, nodeMinW), max(th+2*nodePadY, nodeMinH)
	switch s {
	case Subroutine:
		w += 12
	case Stadium:
		w += h * .3
	case Flag:
		w += h * .35
	case Lean, LeanLeft, Trapezoid, TrapezoidAlt:
		w += h * .7
	case Hexagon:
		w += h * .5
	case Cylinder:
		h += 12
	case Diamond:
		a, b := (tw/2+8)/.6, (th/2+6)/.4
		w, h = max(2*a, 64), max(2*b, 48)
	case Circle, DoubleCircle:
		d := max(float32(math.Hypot(float64(tw), float64(th)))+16, 44)
		if s == DoubleCircle {
			d += 10
		}
		w, h = d, d
	}
	return w, h
}

// breakCycles reverses each edge a depth-first walk finds going back.
func (g *flowLayout) breakCycles() {
	out := make([][]int, len(g.nodes))
	for i, e := range g.edges {
		out[e.u] = append(out[e.u], i)
	}
	state := make([]uint8, len(g.nodes))
	var walk func(n int)
	walk = func(n int) {
		state[n] = 1
		for _, ei := range out[n] {
			e := &g.edges[ei]
			switch state[e.v] {
			case 0:
				walk(e.v)
			case 1:
				e.reversed = true
			}
		}
		state[n] = 2
	}
	for n := range g.nodes {
		if state[n] == 0 {
			walk(n)
		}
	}
	for i := range g.edges {
		if e := &g.edges[i]; e.reversed {
			e.u, e.v = e.v, e.u
		}
	}
}

// rank gives each node the longest path to it, then pulls sources down next to what they feed.
func (g *flowLayout) rank() {
	n := len(g.nodes)
	indeg := make([]int, n)
	for _, e := range g.edges {
		indeg[e.v]++
	}
	var topo, queue []int
	for i := range n {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		topo = append(topo, u)
		for _, e := range g.edges {
			if e.u == u {
				g.nodes[e.v].rank = max(g.nodes[e.v].rank, g.nodes[u].rank+e.minlen)
				if indeg[e.v]--; indeg[e.v] == 0 {
					queue = append(queue, e.v)
				}
			}
		}
	}
	for i := len(topo) - 1; i >= 0; i-- {
		u := topo[i]
		if slices.ContainsFunc(g.edges, func(e ledge) bool { return e.v == u }) {
			continue
		}
		r := math.MaxInt
		for _, e := range g.edges {
			if e.u == u {
				r = min(r, g.nodes[e.v].rank-e.minlen)
			}
		}
		if r != math.MaxInt {
			g.nodes[u].rank = r
		}
	}
	lo := math.MaxInt
	for _, nd := range g.nodes {
		lo = min(lo, nd.rank)
	}
	for i := range g.nodes {
		g.nodes[i].rank -= lo
	}
}

// common is the innermost subgraph holding both a and b, or -1.
func (g *flowLayout) common(a, b int) int {
	if a < 0 || b < 0 {
		return -1
	}
	ca, cb := g.chains[a], g.chains[b]
	c := -1
	for i := 0; i < len(ca) && i < len(cb) && ca[i] == cb[i]; i++ {
		c = ca[i]
	}
	return c
}

// endGroup is the subgraph an edge's end sits in: its node's, or, for an end on a subgraph, that one's parent.
func (g *flowLayout) endGroup(id string, n int) int {
	if gi := g.f.group(id); gi >= 0 && g.f.Node(id) == nil {
		return g.f.Groups[gi].Parent
	}
	return g.nodes[n].group
}

func (g *flowLayout) addDummies() {
	for i := range g.edges {
		e := &g.edges[i]
		fe := g.f.Edges[e.edge]
		from, to := fe.From, fe.To
		if e.reversed {
			from, to = to, from
		}
		grp := g.common(g.endGroup(from, e.u), g.endGroup(to, e.v))
		ru, rv := g.nodes[e.u].rank, g.nodes[e.v].rank
		e.chain = []int{e.u}
		mid := ru + (rv-ru)/2
		for r := ru + 1; r < rv; r++ {
			d := lnode{kind: kDummy, ref: e.edge, group: grp, rank: r}
			if r == mid && fe.Label != "" {
				lb := g.elabels[e.edge]
				d.kind = kLabel
				d.along, d.across = lb.w+14, lb.h+8
				if !g.vertical {
					d.along, d.across = d.across, d.along
				}
			}
			g.nodes = append(g.nodes, d)
			e.chain = append(e.chain, len(g.nodes)-1)
		}
		e.chain = append(e.chain, e.v)
		for k := 1; k < len(e.chain); k++ {
			a, b := e.chain[k-1], e.chain[k]
			w := float32(2)
			if g.nodes[a].kind != kReal && g.nodes[b].kind != kReal {
				w = 8
			}
			if fe.Line == Invisible {
				w /= 4
			}
			g.nodes[a].succs = append(g.nodes[a].succs, adj{b, w})
			g.nodes[b].preds = append(g.nodes[b].preds, adj{a, w})
		}
	}
}

// spans finds each subgraph's ranks and holds its place, with a spine, in any rank between them it has nothing in.
func (g *flowLayout) spans() {
	ng := len(g.f.Groups)
	g.minRank, g.maxRank = make([]int, ng), make([]int, ng)
	for i := range ng {
		g.minRank[i], g.maxRank[i] = math.MaxInt, -1
	}
	has := map[[2]int]bool{}
	for _, n := range g.nodes {
		if n.group < 0 {
			continue
		}
		for _, gi := range g.chains[n.group] {
			g.minRank[gi], g.maxRank[gi] = min(g.minRank[gi], n.rank), max(g.maxRank[gi], n.rank)
			has[[2]int{gi, n.rank}] = true
		}
	}
	for gi := range ng {
		for r := g.minRank[gi]; r <= g.maxRank[gi]; r++ {
			if !has[[2]int{gi, r}] {
				g.nodes = append(g.nodes, lnode{kind: kSpine, ref: -1, group: gi, rank: r})
				for _, a := range g.chains[gi] {
					has[[2]int{a, r}] = true
				}
			}
		}
	}
	top := 0
	for _, n := range g.nodes {
		top = max(top, n.rank)
	}
	g.layers = make([][]int, top+1)
	for i, n := range g.nodes {
		g.layers[n.rank] = append(g.layers[n.rank], i)
	}
}

// order reduces crossings with barycentre sweeps, each subgraph's nodes kept together in every rank.
func (g *flowLayout) order() {
	for _, l := range g.layers {
		for i, n := range l {
			g.nodes[n].pos = float32(i)
		}
	}
	best, bestX := g.snapshot(), g.crossings()
	for it := range 16 {
		down := it%2 == 0
		for k := 1; k < len(g.layers); k++ {
			r := k
			if !down {
				r = len(g.layers) - 1 - k
			}
			for _, n := range g.layers[r] {
				nb := g.nodes[n].preds
				if !down {
					nb = g.nodes[n].succs
				}
				g.nodes[n].bary = g.mean(nb, g.nodes[n].pos)
			}
			g.sortLayer(r)
		}
		if x := g.crossings(); x < bestX {
			best, bestX = g.snapshot(), x
		}
		if bestX == 0 {
			break
		}
	}
	g.layers = best
	for _, l := range g.layers {
		for i, n := range l {
			g.nodes[n].pos = float32(i)
		}
	}
}

func (g *flowLayout) mean(nb []adj, own float32) float32 {
	var s, w float32
	for _, a := range nb {
		s += g.nodes[a.n].pos * a.w
		w += a.w
	}
	if w == 0 {
		return own
	}
	return s / w
}

func (g *flowLayout) snapshot() [][]int {
	out := make([][]int, len(g.layers))
	for i, l := range g.layers {
		out[i] = slices.Clone(l)
	}
	return out
}

type keyPart struct {
	v  float32
	id int
}

// sortLayer orders a rank by barycentre, keeping each subgraph's nodes together: a node sorts by its outermost
// subgraph's mean barycentre in the rank, then the next one in, then its own.
func (g *flowLayout) sortLayer(r int) {
	l := g.layers[r]
	sum, cnt := map[int]float32{}, map[int]float32{}
	for _, n := range l {
		if gr := g.nodes[n].group; gr >= 0 {
			for _, gi := range g.chains[gr] {
				sum[gi] += g.nodes[n].bary
				cnt[gi]++
			}
		}
	}
	keys := map[int][]keyPart{}
	for _, n := range l {
		var k []keyPart
		if gr := g.nodes[n].group; gr >= 0 {
			for _, gi := range g.chains[gr] {
				k = append(k, keyPart{sum[gi] / cnt[gi], -1 - gi})
			}
		}
		keys[n] = append(k, keyPart{g.nodes[n].bary, n})
	}
	slices.SortStableFunc(l, func(a, b int) int {
		ka, kb := keys[a], keys[b]
		for i := 0; i < len(ka) && i < len(kb); i++ {
			switch {
			case ka[i].v < kb[i].v:
				return -1
			case ka[i].v > kb[i].v:
				return 1
			case ka[i].id != kb[i].id:
				return ka[i].id - kb[i].id
			}
		}
		return len(ka) - len(kb)
	})
	for i, n := range l {
		g.nodes[n].pos = float32(i)
	}
}

func (g *flowLayout) crossings() int {
	at := make([]int, len(g.nodes))
	for _, l := range g.layers {
		for i, n := range l {
			at[n] = i
		}
	}
	x := 0
	for _, l := range g.layers {
		var segs [][2]int
		for _, n := range l {
			for _, s := range g.nodes[n].succs {
				segs = append(segs, [2]int{at[n], at[s.n]})
			}
		}
		for i := range segs {
			for j := i + 1; j < len(segs); j++ {
				a, b := segs[i], segs[j]
				if (a[0]-b[0])*(a[1]-b[1]) < 0 {
					x++
				}
			}
		}
	}
	return x
}

// borders puts each subgraph's border nodes either side of its run of nodes in every rank it spans.
func (g *flowLayout) borders() {
	for r, l := range g.layers {
		var out []int
		var open []int
		closeTo := func(depth int) {
			for len(open) > depth {
				gi := open[len(open)-1]
				open = open[:len(open)-1]
				g.nodes = append(g.nodes, lnode{kind: kRight, ref: -1, group: gi, rank: r})
				out = append(out, len(g.nodes)-1)
			}
		}
		for _, n := range l {
			var chain []int
			if gr := g.nodes[n].group; gr >= 0 {
				chain = g.chains[gr]
			}
			d := 0
			for d < len(open) && d < len(chain) && open[d] == chain[d] {
				d++
			}
			closeTo(d)
			for _, gi := range chain[d:] {
				open = append(open, gi)
				g.nodes = append(g.nodes, lnode{kind: kLeft, ref: -1, group: gi, rank: r})
				out = append(out, len(g.nodes)-1)
			}
			out = append(out, n)
		}
		closeTo(0)
		g.layers[r] = out
	}
}

// sep is the least distance between the centres of a and b, b next after a in a rank.
func (g *flowLayout) sep(a, b int) float32 {
	na, nb := &g.nodes[a], &g.nodes[b]
	d := na.along/2 + na.trail + nb.along/2
	switch {
	case na.kind == kLeft:
		d += groupPad
		if !g.vertical {
			d += g.title
		}
	case nb.kind == kRight:
		d += groupPad
	case na.kind == kRight || nb.kind == kLeft:
		d += g.nodeGap * .75
	case na.kind != kReal || nb.kind != kReal:
		d += dummyGap
	default:
		d += g.nodeGap
	}
	return d
}

// solve places a rank's nodes as near their wanted positions as their order and spacing allow, nearest in the
// weighted least squares sense (pool adjacent violators).
func (g *flowLayout) solve(l []int, want, weight []float32) {
	type pool struct{ w, wt, n float32 }
	var stack []pool
	var cum float32
	offs := make([]float32, len(l))
	for i := range l {
		if i > 0 {
			cum += g.sep(l[i-1], l[i])
		}
		offs[i] = cum
		p := pool{weight[i], weight[i] * (want[i] - cum), 1}
		for len(stack) > 0 && stack[len(stack)-1].wt/stack[len(stack)-1].w > p.wt/p.w {
			q := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			p = pool{p.w + q.w, p.wt + q.wt, p.n + q.n}
		}
		stack = append(stack, p)
	}
	i := 0
	for _, p := range stack {
		y := p.wt / p.w
		for range int(p.n) {
			g.nodes[l[i]].pos = y + offs[i]
			i++
		}
	}
}

// edges of each subgraph's box: the least left border and the most right border in any rank
func (g *flowLayout) groupEdges() ([]float32, []float32) {
	lo, hi := make([]float32, len(g.f.Groups)), make([]float32, len(g.f.Groups))
	for i := range lo {
		lo[i], hi[i] = float32(math.Inf(1)), float32(math.Inf(-1))
	}
	for _, n := range g.nodes {
		switch n.kind {
		case kLeft:
			lo[n.group] = min(lo[n.group], n.pos)
		case kRight:
			hi[n.group] = max(hi[n.group], n.pos)
		}
	}
	return lo, hi
}

// tightEdges is where each subgraph's box would sit hugging what's in it: the least room its borders need from
// their neighbours inside, in any rank.
func (g *flowLayout) tightEdges() ([]float32, []float32) {
	lo, hi := make([]float32, len(g.f.Groups)), make([]float32, len(g.f.Groups))
	for i := range lo {
		lo[i], hi[i] = float32(math.Inf(1)), float32(math.Inf(-1))
	}
	for _, l := range g.layers {
		for i, n := range l {
			switch nd := g.nodes[n]; nd.kind {
			case kLeft:
				lo[nd.group] = min(lo[nd.group], g.nodes[l[i+1]].pos-g.sep(n, l[i+1]))
			case kRight:
				hi[nd.group] = max(hi[nd.group], g.nodes[l[i-1]].pos+g.sep(l[i-1], n))
			}
		}
	}
	return lo, hi
}

// place gives each node its position along its rank: packed, then pulled towards its neighbours in sweeps, then
// with each subgraph's borders lined up so its box is a rectangle that nothing else is inside.
func (g *flowLayout) place() {
	for _, l := range g.layers {
		var x float32
		for i, n := range l {
			if i > 0 {
				x += g.sep(l[i-1], n)
			}
			g.nodes[n].pos = x
		}
		for _, n := range l {
			g.nodes[n].pos -= x / 2
		}
	}
	sweep := func(l []int, nbOf func(n *lnode) []adj, borderW float32) {
		want, weight := make([]float32, len(l)), make([]float32, len(l))
		lo, hi := g.tightEdges()
		for i, n := range l {
			nd := &g.nodes[n]
			want[i], weight[i] = nd.pos, .01
			switch nd.kind {
			case kLeft:
				want[i], weight[i] = lo[nd.group], borderW
				continue
			case kRight:
				want[i], weight[i] = hi[nd.group], borderW
				continue
			}
			var s, w float32
			for _, a := range nbOf(nd) {
				s += g.nodes[a.n].pos * a.w
				w += a.w
			}
			if w > 0 {
				want[i], weight[i] = s/w, w
			}
		}
		g.solve(l, want, weight)
	}
	preds := func(n *lnode) []adj { return n.preds }
	succs := func(n *lnode) []adj { return n.succs }
	both := func(n *lnode) []adj { return append(slices.Clip(n.preds), n.succs...) }
	for it := range 40 {
		switch {
		case it < 16 && it%2 == 0:
			for r := 1; r < len(g.layers); r++ {
				sweep(g.layers[r], preds, .5)
			}
		case it < 16:
			for r := len(g.layers) - 2; r >= 0; r-- {
				sweep(g.layers[r], succs, .5)
			}
		case it%2 == 0:
			for r := range g.layers {
				sweep(g.layers[r], both, .5)
			}
		default:
			for r := len(g.layers) - 1; r >= 0; r-- {
				sweep(g.layers[r], both, .5)
			}
		}
	}
	for range 24 {
		for r := range g.layers {
			sweep(g.layers[r], both, 1e4)
		}
		lo, hi := g.groupEdges()
		square := true
		for _, n := range g.nodes {
			if n.kind == kLeft && n.pos-lo[n.group] > .25 || n.kind == kRight && hi[n.group]-n.pos > .25 {
				square = false
			}
		}
		if square {
			break
		}
	}
}

// result turns the placed graph into boxes, routes and what's drawn, in the flowchart's direction.
func (g *flowLayout) result() *Layout {
	// rank positions
	nr := len(g.layers)
	thick := make([]float32, nr)
	for r, l := range g.layers {
		for _, n := range l {
			thick[r] = max(thick[r], g.nodes[n].across)
		}
	}
	titleStart := g.f.Dir == TB
	titleEnd := g.f.Dir == BT
	depth := func(r int, starts bool) int {
		d := 0
		for _, n := range g.layers[r] {
			if g.nodes[n].group < 0 {
				continue
			}
			c := 0
			for _, gi := range g.chains[g.nodes[n].group] {
				if starts && g.minRank[gi] == r || !starts && g.maxRank[gi] == r {
					c++
				}
			}
			d = max(d, c)
		}
		return d
	}
	room := func(starts bool) float32 {
		x := float32(groupPad)
		if starts && titleStart || !starts && titleEnd {
			x += g.title
		}
		return x
	}
	rpos := make([]float32, nr)
	for r := 1; r < nr; r++ {
		rpos[r] = rpos[r-1] + thick[r-1]/2 + thick[r]/2 + g.halfGap + float32(depth(r-1, false))*room(false) + float32(depth(r, true))*room(true)
	}
	// subgraph extents along the rank axis
	ng := len(g.f.Groups)
	gStart, gEnd := make([]float32, ng), make([]float32, ng)
	var extent func(gi int)
	done := make([]bool, ng)
	extent = func(gi int) {
		if done[gi] {
			return
		}
		done[gi] = true
		s, e := float32(math.Inf(1)), float32(math.Inf(-1))
		if g.maxRank[gi] >= 0 {
			s = rpos[g.minRank[gi]] - thick[g.minRank[gi]]/2
			e = rpos[g.maxRank[gi]] + thick[g.maxRank[gi]]/2
		}
		for c := range ng {
			if g.f.Groups[c].Parent == gi {
				extent(c)
				s, e = min(s, gStart[c]), max(e, gEnd[c])
			}
		}
		gStart[gi], gEnd[gi] = s-room(true), e+room(false)
	}
	for gi := range ng {
		extent(gi)
	}
	lo, hi := g.groupEdges()

	// in TB / LR coordinates: x along the order axis for TB
	pt := func(along, across float32) render.Point {
		if g.vertical {
			return render.Point{X: along, Y: across}
		}
		return render.Point{X: across, Y: along}
	}
	rect := func(along0, across0, along1, across1 float32) render.Rect {
		a, b := pt(along0, across0), pt(along1, across1)
		return render.Rect{Min: render.Point{X: min(a.X, b.X), Y: min(a.Y, b.Y)}, Max: render.Point{X: max(a.X, b.X), Y: max(a.Y, b.Y)}}
	}
	l := &Layout{}
	f := g.f
	for i, n := range f.Nodes {
		nd := g.nodes[i]
		c := rpos[nd.rank]
		l.Nodes = append(l.Nodes, Box{ID: n.ID, Label: n.Label, Shape: n.Shape, Rect: rect(nd.pos-nd.along/2, c-nd.across/2, nd.pos+nd.along/2, c+nd.across/2)})
	}
	for gi, gr := range f.Groups {
		r := render.Rect{}
		if !math.IsInf(float64(lo[gi]), 0) {
			r = rect(lo[gi], gStart[gi], hi[gi], gEnd[gi])
		}
		l.Groups = append(l.Groups, Box{ID: gr.ID, Label: gr.Title, Rect: r})
	}
	// ports: where each edge meets a node, spread along its side by where the edge goes
	type port struct {
		edge   int // index into g.edges
		start  bool
		toward float32
	}
	ports := map[[2]int][]port{} // node, side (0 start of rank axis, 1 end)
	for i, e := range g.edges {
		ports[[2]int{e.u, 1}] = append(ports[[2]int{e.u, 1}], port{i, true, g.nodes[e.chain[1]].pos})
		ports[[2]int{e.v, 0}] = append(ports[[2]int{e.v, 0}], port{i, false, g.nodes[e.chain[len(e.chain)-2]].pos})
	}
	ends := make([][2]render.Point, len(g.edges))
	for key, ps := range ports {
		n, side := key[0], key[1]
		slices.SortStableFunc(ps, func(a, b port) int {
			switch {
			case a.toward < b.toward:
				return -1
			case a.toward > b.toward:
				return 1
			}
			return 0
		})
		nd := g.nodes[n]
		shape := f.Nodes[n].Shape
		spread := float32(0)
		switch {
		case len(ps) < 2:
		case shape == Diamond || shape == Circle || shape == DoubleCircle:
			// along a slanted or round side, edges spread to where it's still steep enough to leave cleanly
			spread = min(float32(len(ps)-1)*20, nd.along*.5)
		case spreads(shape, g.vertical):
			spread = max(min(float32(len(ps)-1)*14, nd.along-2*30), 0)
		}
		for i, p := range ps {
			off := float32(0)
			if len(ps) > 1 {
				off = (float32(i)/float32(len(ps)-1) - .5) * spread
			}
			d := sideDepth(shape, nd.along, nd.across, g.vertical, side == 1, off)
			across := rpos[nd.rank] - d
			if side == 1 {
				across = rpos[nd.rank] + d
			}
			at := pt(nd.pos+off, across)
			if p.start {
				ends[p.edge][0] = at
			} else {
				ends[p.edge][1] = at
			}
		}
	}
	for i, e := range g.edges {
		fe := f.Edges[e.edge]
		pts := []render.Point{ends[i][0]}
		var label render.Rect
		for _, d := range e.chain[1 : len(e.chain)-1] {
			nd := g.nodes[d]
			p := pt(nd.pos, rpos[nd.rank])
			if nd.kind == kLabel {
				label = rect(nd.pos-nd.along/2, rpos[nd.rank]-nd.across/2, nd.pos+nd.along/2, rpos[nd.rank]+nd.across/2)
				label = inset(label, 3, 2)
			}
			pts = append(pts, p)
		}
		pts = append(pts, ends[i][1])
		pts = simplify(pts, g.vertical)
		if e.reversed {
			slices.Reverse(pts)
		}
		rt := Route{Edge: e.edge, Points: pts, Back: e.reversed, Label: label, Vertical: g.vertical, Line: fe.Line, StartMark: fe.Start, EndMark: fe.End}
		rt.From, rt.To = g.edgeEnd(fe.From, e.u, e.v, e.reversed, true), g.edgeEnd(fe.To, e.u, e.v, e.reversed, false)
		rt.FromGroup, rt.ToGroup = f.Node(fe.From) == nil, f.Node(fe.To) == nil
		l.Edges = append(l.Edges, rt)
	}
	// an edge on a subgraph ends on its box
	for i := range l.Edges {
		rt := &l.Edges[i]
		if rt.FromGroup {
			rt.Points[0] = clipToBox(l.Groups[rt.From].Rect, rt.Points[1], rt.Points[0])
		}
		if rt.ToGroup {
			n := len(rt.Points)
			rt.Points[n-1] = clipToBox(l.Groups[rt.To].Rect, rt.Points[n-2], rt.Points[n-1])
		}
	}
	for _, ei := range g.loops {
		fe := f.Edges[ei]
		n := slices.IndexFunc(f.Nodes, func(nd Node) bool { return nd.ID == fe.From })
		b := l.Nodes[n].Rect
		var a, z render.Point
		var label render.Rect
		lb := g.elabels[ei]
		if g.vertical {
			cy := (b.Min.Y + b.Max.Y) / 2
			a, z = render.Point{X: b.Max.X, Y: cy - 8}, render.Point{X: b.Max.X, Y: cy + 8}
			if fe.Label != "" {
				label = render.XYWH(b.Max.X+loopReach+4, cy-lb.h/2-4, lb.w+14, lb.h+8)
			}
		} else {
			cx := (b.Min.X + b.Max.X) / 2
			a, z = render.Point{X: cx - 8, Y: b.Max.Y}, render.Point{X: cx + 8, Y: b.Max.Y}
			if fe.Label != "" {
				label = render.XYWH(cx-lb.w/2-7, b.Max.Y+loopReach+4, lb.w+14, lb.h+8)
			}
		}
		l.Edges = append(l.Edges, Route{Edge: ei, From: n, To: n, Points: []render.Point{a, z}, Loop: true, Label: label, Vertical: g.vertical,
			Line: fe.Line, StartMark: fe.Start, EndMark: fe.End})
	}
	g.finish(l)
	return l
}

// edgeEnd is the node or subgraph index of an edge's From (or To).
func (g *flowLayout) edgeEnd(id string, u, v int, reversed, from bool) int {
	if g.f.Node(id) == nil {
		return g.f.group(id)
	}
	if from != reversed {
		return u
	}
	return v
}

// spreads says whether edges meeting a shape's side spread out along it, or all meet at its middle.
func spreads(s Shape, vertical bool) bool {
	switch s {
	case Rect, Round, Subroutine:
		return true
	case Stadium, Cylinder, Lean, LeanLeft, Trapezoid, TrapezoidAlt, Flag, Hexagon:
		return vertical
	}
	return false
}

// sideDepth is how far a shape's side is from its centre along the rank axis, off its middle along the order axis.
func sideDepth(s Shape, along, across float32, vertical, end bool, off float32) float32 {
	switch u := 2 * off / along; s {
	case Diamond:
		return across / 2 * (1 - abs(u))
	case Circle, DoubleCircle:
		return across / 2 * float32(math.Sqrt(float64(max(0, 1-u*u))))
	}
	if vertical {
		return across / 2
	}
	w, h := across, along
	switch s {
	case Lean, LeanLeft, Trapezoid, TrapezoidAlt:
		return w/2 - h*.35/2
	case Flag:
		if !end {
			return w/2 - h*.35
		}
	}
	return w / 2
}

// simplify drops the dummies an edge's curve would pass through anyway, keeping the ends.
func simplify(pts []render.Point, vertical bool) []render.Point {
	// dummies nearly in line are put in line, so a long edge runs straight between them
	for i := 1; i+2 < len(pts); i++ {
		a, b := &pts[i], &pts[i+1]
		if vertical && abs(a.X-b.X) < 6 {
			a.X, b.X = (a.X+b.X)/2, (a.X+b.X)/2
		} else if !vertical && abs(a.Y-b.Y) < 6 {
			a.Y, b.Y = (a.Y+b.Y)/2, (a.Y+b.Y)/2
		}
	}
	for changed := true; changed; {
		changed = false
		for i := 1; i+1 < len(pts); i++ {
			a, p, b := pts[i-1], pts[i], pts[i+1]
			pa, pp, pb, ra, rp, rb := a.X, p.X, b.X, a.Y, p.Y, b.Y
			if !vertical {
				pa, pp, pb, ra, rp, rb = a.Y, p.Y, b.Y, a.X, p.X, b.X
			}
			if rb == ra {
				continue
			}
			t := (rp - ra) / (rb - ra)
			s := t * t * (3 - 2*t)
			if abs(pa+(pb-pa)*s-pp) < 6 {
				pts = slices.Delete(pts, i, i+1)
				changed = true
				break
			}
		}
	}
	return pts
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func inset(r render.Rect, dx, dy float32) render.Rect {
	return render.Rect{Min: render.Point{X: r.Min.X + dx, Y: r.Min.Y + dy}, Max: render.Point{X: r.Max.X - dx, Y: r.Max.Y - dy}}
}

// clipToBox is where the line from a, outside r, towards b first meets r's border.
func clipToBox(r render.Rect, a, b render.Point) render.Point {
	t0 := float32(0)
	dx, dy := b.X-a.X, b.Y-a.Y
	for _, c := range [][2]float32{{-dx, a.X - r.Min.X}, {dx, r.Max.X - a.X}, {-dy, a.Y - r.Min.Y}, {dy, r.Max.Y - a.Y}} {
		p, q := c[0], c[1]
		if p < 0 {
			t0 = max(t0, q/p)
		}
	}
	return render.Point{X: a.X + dx*t0, Y: a.Y + dy*t0}
}

// finish flips a BT or RL chart, moves it all to start at the margin, and makes what's drawn.
func (g *flowLayout) finish(l *Layout) {
	var bb render.Rect
	first := true
	grow := func(r render.Rect) {
		if r.Dx() <= 0 && r.Dy() <= 0 {
			return
		}
		if first {
			bb, first = r, false
			return
		}
		bb.Min.X, bb.Min.Y = min(bb.Min.X, r.Min.X), min(bb.Min.Y, r.Min.Y)
		bb.Max.X, bb.Max.Y = max(bb.Max.X, r.Max.X), max(bb.Max.Y, r.Max.Y)
	}
	for _, n := range l.Nodes {
		grow(n.Rect)
	}
	for _, gr := range l.Groups {
		grow(gr.Rect)
	}
	for _, e := range l.Edges {
		grow(e.Label)
		for _, p := range e.Points {
			grow(render.Rect{Min: p, Max: render.Point{X: p.X + .01, Y: p.Y + .01}})
		}
		if e.Loop {
			p := e.Points[1]
			if e.Vertical {
				grow(render.XYWH(p.X, p.Y, loopReach+1, 1))
			} else {
				grow(render.XYWH(p.X, p.Y, 1, loopReach+1))
			}
		}
	}
	flipX, flipY := g.f.Dir == RL, g.f.Dir == BT
	mp := func(p render.Point) render.Point {
		x, y := p.X-bb.Min.X+margin, p.Y-bb.Min.Y+margin
		if flipX {
			x = bb.Max.X - p.X + margin
		}
		if flipY {
			y = bb.Max.Y - p.Y + margin
		}
		return render.Point{X: x, Y: y}
	}
	mr := func(r render.Rect) render.Rect {
		if r == (render.Rect{}) {
			return r
		}
		a, b := mp(r.Min), mp(r.Max)
		return render.Rect{Min: render.Point{X: min(a.X, b.X), Y: min(a.Y, b.Y)}, Max: render.Point{X: max(a.X, b.X), Y: max(a.Y, b.Y)}}
	}
	for i := range l.Nodes {
		l.Nodes[i].Rect = mr(l.Nodes[i].Rect)
	}
	for i := range l.Groups {
		l.Groups[i].Rect = mr(l.Groups[i].Rect)
	}
	for i := range l.Edges {
		e := &l.Edges[i]
		e.Label = mr(e.Label)
		for j := range e.Points {
			e.Points[j] = mp(e.Points[j])
		}
	}
	l.Size = render.Point{X: bb.Dx() + 2*margin, Y: bb.Dy() + 2*margin}
	l.prims = g.prims(l)
}
