package vim

import (
	"strings"
)

// span is a text object's range for an operator: start, the end and how the end counts.
type span struct {
	start, end Pos
	kind       motionKind
	empty      bool // nothing to operate on, but not a failure
	noAdjust   bool // an exclusive end in column 0 stays put
}

type object func(e *Engine, v *view, count int, inner bool) (span, bool)

var objects = map[string]object{}

func init() {
	word := func(big bool) object {
		return func(e *Engine, v *view, n int, inner bool) (span, bool) { return currentWord(v, e.cur, n, !inner, big) }
	}
	block := func(open, close rune) object {
		return func(e *Engine, v *view, n int, inner bool) (span, bool) {
			return currentBlock(v, e.cur, n, !inner, open, close)
		}
	}
	quote := func(q rune) object {
		return func(e *Engine, v *view, n int, inner bool) (span, bool) { return currentQuote(v, e.cur, n, !inner, q) }
	}
	for _, p := range []string{"i", "a"} {
		objects[p+"w"] = word(false)
		objects[p+"W"] = word(true)
		objects[p+"s"] = func(e *Engine, v *view, n int, inner bool) (span, bool) { return currentSent(v, e.cur, n, !inner) }
		objects[p+"p"] = func(e *Engine, v *view, n int, inner bool) (span, bool) { return currentPar(v, e.cur, n, !inner) }
		objects[p+"t"] = func(e *Engine, v *view, n int, inner bool) (span, bool) { return currentTag(v, e.cur, n, !inner) }
		for _, k := range []string{"(", ")", "b"} {
			objects[p+k] = block('(', ')')
		}
		for _, k := range []string{"{", "}", "B"} {
			objects[p+k] = block('{', '}')
		}
		for _, k := range []string{"[", "]"} {
			objects[p+k] = block('[', ']')
		}
		for _, k := range []string{"<", ">"} {
			objects[p+k] = block('<', '>')
		}
		for _, q := range []rune{'"', '\'', '`'} {
			objects[p+string(q)] = quote(q)
		}
	}
}

// RegisterObject adds a text object, "iX" and "aX" style keys included. A plugin's object shadows a built-in one.
func RegisterObject(keys string, o func(e *Engine, from Pos, count int, inner bool) (start, end Pos, linewiseObj bool, ok bool)) {
	objects[keys] = func(e *Engine, v *view, n int, inner bool) (span, bool) {
		s, en, lw, ok := o(e, e.cur, n, inner)
		kind := exclusive
		if lw {
			kind = linewise
		}
		return span{start: s, end: en, kind: kind}, ok
	}
}

func (v *view) oneleft(p *Pos) bool {
	if p.Col == 0 {
		return false
	}
	p.Col = v.prevCol(p.Line, p.Col)
	return true
}

// backInLine goes back to the start of the word or white space under p, not past the line start.
func (v *view) backInLine(p *Pos, big bool) {
	sclass := v.cls(*p, big)
	for p.Col > 0 {
		v.dec(p)
		if v.cls(*p, big) != sclass {
			v.inc(p)
			break
		}
	}
}

// currentWord is vim's current_word() for an operator.
func currentWord(v *view, cur Pos, count int, include, big bool) (span, bool) {
	p := cur
	v.backInLine(&p, big)
	start := p
	includeWhite := false
	inclusiveEnd := true
	if (v.cls(p, big) == 0) == include {
		if !endWord(v, &p, 1, big, true, true) {
			return span{}, false
		}
	} else {
		fwdWord(v, &p, 1, big, true)
		if p.Col == 0 {
			v.decl(&p)
		} else {
			v.oneleft(&p)
		}
		if include {
			includeWhite = true
		}
	}
	for count--; count > 0; count-- {
		inclusiveEnd = true
		if v.incl(&p) == -1 {
			return span{}, false
		}
		if include != (v.cls(p, big) == 0) {
			if !fwdWord(v, &p, 1, big, true) && count > 1 {
				return span{}, false
			}
			if !v.oneleft(&p) {
				inclusiveEnd = false
			}
		} else if !endWord(v, &p, 1, big, true, true) {
			return span{}, false
		}
	}
	if includeWhite && (v.cls(p, big) != 0 || p.Col == 0 && !inclusiveEnd) {
		q := start
		if v.oneleft(&q) {
			v.backInLine(&q, big)
			if v.cls(q, big) == 0 && q.Col > 0 {
				start = q
			}
		}
	}
	kind := exclusive
	if inclusiveEnd {
		kind = inclusive
	}
	return span{start: start, end: p, kind: kind}, true
}

// currentPar is vim's current_par() for an operator: always linewise.
func currentPar(v *view, cur Pos, count int, include bool) (span, bool) {
	startLine := cur.Line
	white := v.lineWhite(startLine)
	for startLine > 0 {
		if white {
			if !v.lineWhite(startLine - 1) {
				break
			}
		} else if v.lineWhite(startLine-1) || v.startPS(startLine, 0) {
			break
		}
		startLine--
	}
	end := startLine
	for end < v.lines() && v.lineWhite(end) {
		end++
	}
	end--
	i := count
	if !include && white {
		i--
	}
	last := v.lines() - 1
	for ; i > 0; i-- {
		if end == last {
			return span{}, false
		}
		doWhite := false
		if !include {
			doWhite = v.lineWhite(end + 1)
		}
		if include || !doWhite {
			end++
			for end < last && !v.lineWhite(end+1) && !v.startPS(end+1, 0) {
				end++
			}
		}
		if i == 1 && white && include {
			break
		}
		if include || doWhite {
			for end < last && v.lineWhite(end+1) {
				end++
			}
		}
	}
	if !white && !v.lineWhite(end) && include {
		for startLine > 0 && v.lineWhite(startLine-1) {
			startLine--
		}
	}
	return span{start: Pos{startLine, 0}, end: Pos{end, 0}, kind: linewise}, true
}

// findUnmatched walks from p for the unmatched open (backwards) or close (forwards) bracket.
func findUnmatched(v *view, p Pos, open, close rune, fwd bool) (Pos, bool) {
	want, other := open, close
	if fwd {
		want, other = close, open
	}
	depth := 0
	for {
		var r int
		if fwd {
			r = v.inc(&p)
		} else {
			r = v.dec(&p)
		}
		if r == -1 {
			return p, false
		}
		if escaped(v, p) {
			continue
		}
		switch v.gchar(p) {
		case other:
			depth++
		case want:
			if depth == 0 {
				return p, true
			}
			depth--
		}
	}
}

// escaped reports whether the character at p follows an odd number of backslashes.
func escaped(v *view, p Pos) bool {
	l := v.line(p.Line)
	n := 0
	for i := p.Col - 1; i >= 0 && l[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// currentBlock is vim's current_block() for an operator.
func currentBlock(v *view, cur Pos, count int, include bool, open, close rune) (span, bool) {
	p := cur
	if open == '{' {
		for v.inindent(p, 1) {
			if v.inc(&p) != 0 {
				break
			}
		}
	}
	if v.gchar(p) == open {
		p.Col++
	}
	var start Pos
	found := false
	if _, ok := findUnmatched(v, p, open, close, false); ok {
		for ; count > 0; count-- {
			q, ok := findUnmatched(v, p, open, close, false)
			if !ok {
				return span{}, false
			}
			p, start, found = q, q, true
		}
	} else {
		// No block around the cursor: look forward, as vim's findmatchlimit(FM_FORWARD) does: a close
		// bracket before the next open one has to be balanced first.
		for ; count > 0; count-- {
			q := p
			depth := 0
			for {
				if v.inc(&q) == -1 {
					return span{}, false
				}
				if escaped(v, q) {
					continue
				}
				c := v.gchar(q)
				if c == close {
					depth++
				} else if c == open {
					if depth == 0 {
						break
					}
					depth--
				}
			}
			p, start, found = q, q, true
		}
	}
	if !found {
		return span{}, false
	}
	end, ok := findUnmatched(v, start, open, close, true)
	if !ok {
		return span{}, false
	}
	sol := false
	if !include {
		v.incl(&start)
		sol = end.Col == 0
		v.decl(&end)
		for v.inindent(end, 1) {
			sol = true
			if v.decl(&end) != 0 {
				break
			}
		}
	}
	s := span{start: start, kind: exclusive}
	switch {
	case sol:
		v.incl(&end)
		s.end = end
	case start.Line < end.Line || start.Line == end.Line && start.Col <= end.Col:
		s.end = end
		s.kind = inclusive
	default:
		s.end = start
		s.empty = true
	}
	return s, true
}

func findNextQuote(l []byte, col int, q byte, escape bool) int {
	for col < len(l) {
		c := l[col]
		if escape && c == '\\' {
			col++
			if col >= len(l) {
				return -1
			}
		} else if c == q {
			return col
		}
		col++
	}
	return -1
}

func findPrevQuote(l []byte, col int, q byte) int {
	for col > 0 {
		col--
		n := 0
		for col-n > 0 && l[col-n-1] == '\\' {
			n++
		}
		if n&1 == 1 {
			col -= n
		} else if l[col] == q {
			break
		}
	}
	return col
}

// currentQuote is vim's current_quote() for an operator: quotes never span lines.
func currentQuote(v *view, cur Pos, count int, include bool, quote rune) (span, bool) {
	l := v.line(cur.Line)
	q := byte(quote)
	colStart, colEnd := cur.Col, 0
	if cur.Col < len(l) && l[cur.Col] == q {
		first := cur.Col
		colStart = 0
		for {
			colStart = findNextQuote(l, colStart, q, false)
			if colStart < 0 || colStart > first {
				return span{}, false
			}
			colEnd = findNextQuote(l, colStart+1, q, true)
			if colEnd < 0 {
				return span{}, false
			}
			if colStart <= first && first <= colEnd {
				break
			}
			colStart = colEnd + 1
		}
	} else {
		colStart = findPrevQuote(l, colStart, q)
		if colStart >= len(l) || l[colStart] != q {
			colStart = findNextQuote(l, colStart, q, false)
			if colStart < 0 {
				return span{}, false
			}
		}
		colEnd = findNextQuote(l, colStart+1, q, true)
		if colEnd < 0 {
			return span{}, false
		}
	}
	if include {
		if colEnd+1 < len(l) && isWhite(rune(l[colEnd+1])) {
			for colEnd+1 < len(l) && isWhite(rune(l[colEnd+1])) {
				colEnd++
			}
		} else {
			for colStart > 0 && isWhite(rune(l[colStart-1])) {
				colStart--
			}
		}
	}
	if !include && count < 2 {
		colStart++
	}
	s := span{start: Pos{cur.Line, colStart}, end: Pos{cur.Line, colEnd}, kind: exclusive}
	if include || count > 1 {
		e := s.end
		if v.inc(&e) == 2 {
			s.kind = inclusive
		}
		s.end = e
	}
	return s, true
}

// findFirstBlank moves p back to the first of the blanks before it.
func findFirstBlank(v *view, p *Pos) {
	for v.decl(p) != -1 {
		if c := v.gchar(*p); !isWhite(c) {
			v.incl(p)
			break
		}
	}
}

func findSentForward(v *view, p Pos, count int, atStart bool) Pos {
	for ; count > 0; count-- {
		p, _ = findSent(v, p, 1, 1)
		if atStart {
			findFirstBlank(v, &p)
		}
		if count == 1 || atStart {
			v.decl(&p)
		}
		atStart = !atStart
	}
	return p
}

// currentSent is vim's current_sent() for an operator.
func currentSent(v *view, cur Pos, count int, include bool) (span, bool) {
	start := cur
	next, _ := findSent(v, cur, 1, 1)
	p := cur
	for isWhite(v.gchar(p)) {
		v.incl(&p)
	}
	startBlank := false
	at := next
	if p == next {
		startBlank = true
		findFirstBlank(v, &start)
	} else {
		at, _ = findSent(v, next, -1, 1)
		start = at
	}
	ncount := count
	if include {
		ncount = count * 2
	} else if startBlank {
		ncount--
	}
	end := at
	if startBlank {
		end = next
	}
	if ncount > 0 {
		end = findSentForward(v, end, ncount, true)
	} else {
		v.decl(&end)
	}
	if include {
		if startBlank {
			findFirstBlank(v, &end)
			if isWhite(v.gchar(end)) {
				v.decl(&end)
			}
		} else if !isWhite(v.gchar(end)) {
			findFirstBlank(v, &start)
		}
	}
	kind := exclusive
	if v.incl(&end) == -1 {
		kind = inclusive
	}
	return span{start: start, end: end, kind: kind}, true
}

// tag is one HTML/XML tag in the buffer, by byte offsets.
type tag struct {
	start, end int // end is just past '>'
	name       string
	closing    bool
}

func scanTags(s string) []tag {
	var out []tag
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		j := i + 1
		closing := j < len(s) && s[j] == '/'
		if closing {
			j++
		}
		k := j
		for k < len(s) && !strings.ContainsRune(" \t\n/>", rune(s[k])) {
			k++
		}
		if k == j {
			continue
		}
		gt := strings.IndexByte(s[k:], '>')
		if gt < 0 {
			break
		}
		gt += k
		if strings.IndexByte(s[i+1:gt], '<') >= 0 {
			continue
		}
		if !closing && s[gt-1] == '/' {
			i = gt
			continue
		}
		out = append(out, tag{start: i, end: gt + 1, name: strings.ToLower(s[j:k]), closing: closing})
		i = gt
	}
	return out
}

// currentTag finds the count-th tag block around the cursor.
func currentTag(v *view, cur Pos, count int, include bool) (span, bool) {
	s := v.s.String()
	off := v.offset(cur)
	tags := scanTags(s)
	for _, t := range tags {
		if off >= t.start && off < t.end {
			if t.closing {
				off = t.start - 1
			} else {
				off = t.end
			}
			break
		}
	}
	var open, close *tag
	lo, hi := off, off
	for ; count > 0; count-- {
		var stack []tag
		var o *tag
		for i := range tags {
			t := tags[i]
			if t.start >= lo && !(t.end <= lo) {
				break
			}
			if t.closing {
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j].name == t.name {
						stack = stack[:j]
						break
					}
				}
			} else {
				stack = append(stack, t)
			}
		}
		for len(stack) > 0 {
			cand := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			depth := 0
			for i := range tags {
				t := tags[i]
				if t.start < cand.end || t.name != cand.name {
					continue
				}
				if !t.closing {
					depth++
					continue
				}
				if depth > 0 {
					depth--
					continue
				}
				if t.end > hi {
					c := cand
					o, close = &c, &tags[i]
				}
				break
			}
			if o != nil {
				break
			}
		}
		if o == nil {
			return span{}, false
		}
		open = o
		lo, hi = open.start, close.end
	}
	if include {
		return span{start: v.pos(open.start), end: v.pos(close.end), kind: exclusive}, true
	}
	if open.end == close.start {
		return span{start: v.pos(open.end), end: v.pos(open.end), empty: true}, true
	}
	end := v.pos(close.start)
	if end.Col == 0 {
		return span{start: v.pos(open.end), end: end, kind: exclusive, noAdjust: true}, true
	}
	v.dec(&end)
	return span{start: v.pos(open.end), end: end, kind: inclusive}, true
}
