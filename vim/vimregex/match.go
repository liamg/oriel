package vimregex

import "math"

type pos = Pos

func (a pos) before(b pos) bool { return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col }

type span struct {
	s, e       pos
	sSet, eSet bool
}

// saved is the state a failed path must restore.
type saved struct {
	caps         [10]span
	zs, ze       pos
	zsSet, zeSet bool
}

type matcher struct {
	re    *Regex
	in    Input
	st    *State
	n     int
	ic    bool
	saved // the current captures and \zs \ze
	steps int
	depth int
	err   error
	cache [4]struct {
		n int
		b []byte
	}
	next int
}

var noState State

func (r *Regex) newMatcher(in Input, st *State) *matcher {
	if st == nil {
		st = &noState
	}
	m := &matcher{re: r, in: in, st: st, n: in.Lines(), ic: r.ic}
	for i := range m.cache {
		m.cache[i].n = -1
	}
	return m
}

// line returns line l; the line after the last is empty, reached by matching the last end-of-line.
func (m *matcher) line(l int) []byte {
	if l < 0 || l >= m.n {
		return nil
	}
	for i := range m.cache {
		if m.cache[i].n == l {
			return m.cache[i].b
		}
	}
	b := m.in.Line(l)
	m.cache[m.next] = struct {
		n int
		b []byte
	}{l, b}
	m.next = (m.next + 1) % len(m.cache)
	return b
}

func (m *matcher) try(p pos) (Match, bool) {
	m.saved = saved{}
	var end pos
	if !m.match(m.re.prog, p, func(q pos) bool { end = q; return true }) {
		return Match{}, false
	}
	start := p
	if m.zsSet {
		start = m.zs
	}
	if m.zeSet {
		end = m.ze
	}
	res := Match{Start: start, End: end}
	res.Groups[0] = Group{Start: res.Start, End: res.End, Set: true}
	for i := 1; i < 10; i++ {
		if c := m.caps[i]; c.sSet && c.eSet {
			res.Groups[i] = Group{Start: c.s, End: c.e, Set: true}
		}
	}
	return res, true
}

type cont func(pos) bool

func accept(pos) bool { return true }

func (m *matcher) tick() bool {
	if m.err != nil {
		return false
	}
	m.steps++
	if m.steps > maxSteps {
		m.err = ErrTooComplex
		return false
	}
	return true
}

func (m *matcher) match(n *node, p pos, k cont) bool {
	if !m.tick() {
		return false
	}
	m.depth++
	if m.depth > maxDepth {
		m.err = ErrTooComplex
		m.depth--
		return false
	}
	ok := m.match1(n, p, k)
	m.depth--
	return ok
}

func (m *matcher) match1(n *node, p pos, k cont) bool {
	switch n.op {
	case opEmpty:
		return k(p)
	case opChar, opCombOnly, opAny, opClass, opSet, opNL:
		q, ok := m.one(n, p)
		return ok && k(q)
	case opBOL, opEOL, opBOW, opEOW, opBOF, opEOF, opCursor, opVisual, opMark, opLnum, opCol, opVcol:
		return m.assert(n, p) && k(p)
	case opSkipComb:
		line := m.line(p.Line)
		q := p
		for q.Col < len(line) {
			r, l := decode(line, q.Col)
			if !isComposing(r) {
				break
			}
			q.Col += l
		}
		return k(q)
	case opZS:
		old, oldSet := m.zs, m.zsSet
		m.zs, m.zsSet = p, true
		if k(p) {
			return true
		}
		m.zs, m.zsSet = old, oldSet
		return false
	case opZE:
		old, oldSet := m.ze, m.zeSet
		m.ze, m.zeSet = p, true
		if k(p) {
			return true
		}
		m.ze, m.zeSet = old, oldSet
		return false
	case opBackref:
		q, ok := m.backref(n.num, p)
		return ok && k(q)
	case opCapture:
		i := n.num
		old := m.caps[i]
		m.caps[i].s, m.caps[i].sSet = p, true
		if m.match(n.subs[0], p, func(q pos) bool {
			inner := m.caps[i]
			m.caps[i].e, m.caps[i].eSet = q, true
			if k(q) {
				return true
			}
			m.caps[i] = inner
			return false
		}) {
			return true
		}
		m.caps[i] = old
		return false
	case opConcat:
		return m.seq(n.subs, 0, p, k)
	case opAlt:
		for _, s := range n.subs {
			if m.match(s, p, k) {
				return true
			}
			if m.err != nil {
				return false
			}
		}
		return false
	case opRepeat:
		if n.subs[0].unit() {
			return m.repeatUnit(n, p, k)
		}
		return m.repeat(n, p, 0, k)
	case opAhead:
		sv := m.saved
		if !m.match(n.subs[0], p, accept) {
			m.saved = sv
			return false
		}
		m.dropZ(sv)
		if k(p) {
			return true
		}
		m.saved = sv
		return false
	case opNotAhead:
		sv := m.saved
		ok := m.match(n.subs[0], p, accept)
		m.saved = sv
		return !ok && m.err == nil && k(p)
	case opAtomic:
		sv := m.saved
		var end pos
		if !m.match(n.subs[0], p, func(q pos) bool { end = q; return true }) {
			m.saved = sv
			return false
		}
		m.dropZ(sv)
		if k(end) {
			return true
		}
		m.saved = sv
		return false
	case opBehind, opNotBehind:
		sv := m.saved
		ok := m.behind(n, p)
		if n.op == opNotBehind {
			m.saved = sv
			return !ok && m.err == nil && k(p)
		}
		if !ok {
			m.saved = sv
			return false
		}
		m.dropZ(sv)
		if k(p) {
			return true
		}
		m.saved = sv
		return false
	}
	return false
}

// dropZ undoes \zs and \ze set inside a look-around or \@>, keeping its groups, as nvim does.
func (m *matcher) dropZ(sv saved) {
	m.zs, m.zsSet, m.ze, m.zeSet = sv.zs, sv.zsSet, sv.ze, sv.zeSet
}

// seq matches ns[i:] in order, stepping over single characters and assertions without nesting.
func (m *matcher) seq(ns []*node, i int, p pos, k cont) bool {
	for i < len(ns) {
		n := ns[i]
		if n.unit() {
			q, ok := m.one(n, p)
			if !ok {
				return false
			}
			p = q
		} else if n.op >= opBOL && n.op <= opVcol {
			if !m.tick() || !m.assert(n, p) {
				return false
			}
		} else {
			break
		}
		i++
	}
	if i == len(ns) {
		return k(p)
	}
	if i == len(ns)-1 {
		return m.match(ns[i], p, k)
	}
	return m.match(ns[i], p, func(q pos) bool { return m.seq(ns, i+1, q, k) })
}

// repeat matches a multi on an atom that isn't a single character. An iteration that matches nothing
// ends the repetition, once the minimum is reached.
func (m *matcher) repeat(n *node, p pos, count int, k cont) bool {
	next := func(q pos) bool {
		if q == p && count+1 >= n.min {
			return k(q)
		}
		return m.repeat(n, q, count+1, k)
	}
	if n.greedy {
		if count < n.max && m.match(n.subs[0], p, next) {
			return true
		}
		return count >= n.min && m.err == nil && k(p)
	}
	if count >= n.min && k(p) {
		return true
	}
	return count < n.max && m.err == nil && m.match(n.subs[0], p, next)
}

// repeatUnit matches a multi on a single character: count forward, then back up one at a time.
func (m *matcher) repeatUnit(n *node, p pos, k cont) bool {
	sub := n.subs[0]
	count, q := 0, p
	if n.greedy {
		for count < n.max {
			r, ok := m.one(sub, q)
			if !ok {
				break
			}
			q = r
			count++
		}
		if m.err != nil {
			return false
		}
		for count >= n.min {
			if k(q) {
				return true
			}
			if m.err != nil || count == n.min {
				return false
			}
			q = m.back(q)
			count--
		}
		return false
	}
	for {
		if count >= n.min {
			if k(q) {
				return true
			}
			if m.err != nil {
				return false
			}
		}
		if count >= n.max {
			return false
		}
		r, ok := m.one(sub, q)
		if !ok {
			return false
		}
		q = r
		count++
	}
}

// back steps back over one character, or to the end of the previous line.
func (m *matcher) back(p pos) pos {
	if p.Col == 0 {
		if p.Line == 0 {
			return p
		}
		return pos{p.Line - 1, len(m.line(p.Line - 1))}
	}
	return pos{p.Line, clusterStart(m.line(p.Line), p.Col)}
}

// behind tries the atom at starts going back from p, nearest first, until one ends exactly at p. It
// looks back over the line p is on and the one before, or `limit` bytes when that is set.
func (m *matcher) behind(n *node, p pos) bool {
	limit := n.num
	s := p
	for {
		if m.match(n.subs[0], s, func(q pos) bool { return q == p }) {
			return true
		}
		if m.err != nil {
			return false
		}
		end := p.Col
		if s.Line < p.Line {
			end = len(m.line(s.Line))
		}
		if limit > 0 && end-s.Col >= limit {
			return false
		}
		if s.Col == 0 {
			if s.Line < p.Line || s.Line == 0 {
				return false
			}
			s = pos{s.Line - 1, len(m.line(s.Line - 1))}
		} else {
			s.Col = clusterStart(m.line(s.Line), s.Col)
		}
	}
}

// one matches a single-character node at p.
func (m *matcher) one(n *node, p pos) (pos, bool) {
	if !m.tick() {
		return p, false
	}
	line := m.line(p.Line)
	if p.Col >= len(line) {
		if (n.nl || n.op == opNL || n.op == opSet && n.set.nl) && p.Line < m.n {
			return pos{p.Line + 1, 0}, true
		}
		return p, false
	}
	if n.op == opNL {
		return p, false
	}
	r, l := decode(line, p.Col)
	end := clusterEnd(line, p.Col)
	switch n.op {
	case opAny:
	case opClass:
		if !m.inClass(n.cls, r) {
			return p, false
		}
	case opSet:
		if n.set.has(r, m) == n.neg {
			return p, false
		}
	case opChar:
		if r != n.r && !(m.ic && foldEq(r, n.r)) {
			return p, false
		}
		if m.re.icombine || n.anyComb {
			break
		}
		if end == p.Col+l {
			if len(n.comb) > 0 {
				return p, false
			}
			break
		}
		if len(n.comb) == 0 || !hasAll(line[p.Col+l:end], n.comb) {
			return p, false
		}
	case opCombOnly:
		if !hasAll(line[p.Col:end], []rune{n.r}) {
			return p, false
		}
	}
	return pos{p.Line, end}, true
}

func hasAll(b []byte, want []rune) bool {
	for _, w := range want {
		found := false
		for i := 0; i < len(b); {
			r, l := decode(b, i)
			if r == w {
				found = true
				break
			}
			i += l
		}
		if !found {
			return false
		}
	}
	return true
}

func (m *matcher) backref(i int, p pos) (pos, bool) {
	g := m.caps[i]
	if !g.sSet || !g.eSet || !g.s.before(g.e) {
		return p, true
	}
	s, q := g.s, p
	for s != g.e {
		if !m.tick() {
			return p, false
		}
		sl := m.line(s.Line)
		if s.Col >= len(sl) {
			if ql := m.line(q.Line); q.Col < len(ql) || q.Line >= m.n {
				return p, false
			}
			s, q = pos{s.Line + 1, 0}, pos{q.Line + 1, 0}
			continue
		}
		ql := m.line(q.Line)
		if q.Col >= len(ql) {
			return p, false
		}
		a, al := decode(sl, s.Col)
		b, bl := decode(ql, q.Col)
		if a != b && !(m.ic && foldEq(a, b)) {
			return p, false
		}
		s.Col += al
		q.Col += bl
	}
	return q, true
}

func cmpNum(v int, n *node, want int) bool {
	switch n.cmp {
	case '<':
		return v < want
	case '>':
		return v > want
	}
	return v == want
}

func (m *matcher) vcol(line, col int) int {
	if m.st.VCol != nil {
		return m.st.VCol(line, col)
	}
	return col
}

// vcolEnd is the last screen column of the character at (line, col).
func (m *matcher) vcolEnd(line, col int) int {
	b := m.line(line)
	if col >= len(b) {
		return m.vcol(line, col)
	}
	return m.vcol(line, clusterEnd(b, col)) - 1
}

func (m *matcher) assert(n *node, p pos) bool {
	line := m.line(p.Line)
	switch n.op {
	case opBOL:
		return p.Col == 0
	case opEOL:
		return p.Col >= len(line)
	case opBOW:
		if p.Col >= len(line) {
			return false
		}
		r, _ := decode(line, p.Col)
		this := m.wordClass(r)
		return this > 1 && m.prevClass(line, p.Col) != this
	case opEOW:
		if p.Col == 0 {
			return false
		}
		this := 0
		if p.Col < len(line) {
			r, _ := decode(line, p.Col)
			this = m.wordClass(r)
		}
		prev := m.prevClass(line, p.Col)
		return this != prev && prev > 1
	case opBOF:
		return p.Line == 0 && p.Col == 0
	case opEOF:
		return p.Line == m.n-1 && p.Col >= len(line)
	case opCursor:
		return p.Line == m.st.Cursor.Line && p.Col == m.st.Cursor.Col
	case opVisual:
		return m.inVisual(p)
	case opMark:
		if m.st.Mark == nil {
			return false
		}
		mk, ok := m.st.Mark(n.r)
		if !ok {
			return false
		}
		mc := mk.Col
		if mk.Line == p.Line && mc >= math.MaxInt32 {
			mc = len(line)
		}
		switch {
		case mk.Line == p.Line && mc == p.Col:
			return n.cmp == '\''
		case mk.Line < p.Line || mk.Line == p.Line && mc < p.Col:
			return n.cmp == '>'
		}
		return n.cmp == '<'
	case opLnum:
		want := n.num
		if n.cur {
			want = m.st.Cursor.Line + 1
		}
		return cmpNum(p.Line+1, n, want)
	case opCol:
		want := n.num
		if n.cur {
			want = m.st.Cursor.Col + 1
		}
		return cmpNum(p.Col+1, n, want)
	case opVcol:
		want := n.num
		if n.cur {
			want = m.vcolEnd(m.st.Cursor.Line, m.st.Cursor.Col) + 1
		}
		v := 0
		if p.Line < m.n {
			v = m.vcol(p.Line, p.Col)
		}
		return cmpNum(v+1, n, want)
	}
	return false
}

func (m *matcher) prevClass(line []byte, col int) int {
	if col == 0 {
		return -1
	}
	r, _ := decode(line, clusterStart(line, col))
	return m.wordClass(r)
}

func (m *matcher) inVisual(p pos) bool {
	a := m.st.Visual
	if a == nil {
		return false
	}
	top, bot := pos{a.Start.Line, a.Start.Col}, pos{a.End.Line, a.End.Col}
	if bot.before(top) {
		top, bot = bot, top
	}
	if p.Line < top.Line || p.Line > bot.Line {
		return false
	}
	switch a.Mode {
	case 'v':
		return !(p.Line == top.Line && p.Col < top.Col || p.Line == bot.Line && p.Col >= bot.Col+1)
	case 0x16:
		start, end := m.vcol(top.Line, top.Col), m.vcolEnd(top.Line, top.Col)
		start = min(start, m.vcol(bot.Line, bot.Col))
		end = max(end, m.vcolEnd(bot.Line, bot.Col))
		c := m.vcol(p.Line, p.Col)
		return c >= start && c <= end
	}
	return true
}
