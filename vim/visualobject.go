package vim

// visualObject applies a text object in Visual mode: on a one-character area it selects the object, on a
// bigger one it extends the area, as vim's current_word(), current_par() and current_block() do.
func (e *Engine) visualObject(v *view, c cmd) {
	single := *e.visual == e.cur
	kind := c.obj[1]
	inner := c.obj[0] == 'i'
	switch kind {
	case 's':
		e.visualSentence(v, c.count1(), !inner)
		return
	case '"', '\'', '`':
		e.visualQuote(v, c.count1(), !inner, kind)
		return
	}
	if !single {
		switch kind {
		case 'w', 'W':
			e.extendWord(v, c.count1(), !inner, kind == 'W')
			return
		case 'p':
			e.extendPar(v, c.count1(), !inner)
			return
		}
		if open, close, ok := blockPair(kind); ok {
			e.extendBlock(v, c.count1(), !inner, open, close)
			return
		}
	}
	s, ok := objects[c.obj](e, v, c.count1(), inner)
	if ok && s.empty && kind == 't' {
		// An empty tag block selects the tags themselves.
		s, ok = objects["at"](e, v, c.count1(), false)
	}
	if !ok || s.empty {
		return
	}
	end := s.end
	if s.kind == exclusive && !s.noAdjust {
		if end.Col > 0 {
			end.Col = v.prevCol(end.Line, end.Col)
		} else if end.Line > s.start.Line {
			end = Pos{end.Line - 1, v.lineLen(end.Line - 1)}
		}
	}
	if s.kind == linewise {
		e.vmode = 'V'
		p := s.start
		e.visual = &p
		e.cur = Pos{end.Line, 0}
		e.setWant(v)
		return
	}
	if e.vmode == 'V' {
		e.vmode = 'v'
	}
	p := s.start
	e.visual = &p
	e.cur = end
	e.setWant(v)
}

func blockPair(k byte) (rune, rune, bool) {
	switch k {
	case '(', ')', 'b':
		return '(', ')', true
	case '{', '}', 'B':
		return '{', '}', true
	case '[', ']':
		return '[', ']', true
	case '<', '>':
		return '<', '>', true
	}
	return 0, 0, false
}

// bckendWord is vim's bckend_word() with eol: back to the end of the previous word, stopping at a line start.
func bckendWord(v *view, p *Pos, big bool) bool {
	sclass := v.cls(*p, big)
	i := v.dec(p)
	if i == -1 {
		return false
	}
	if i == 1 {
		return true
	}
	if sclass != 0 {
		for v.cls(*p, big) == sclass {
			if i = v.dec(p); i == -1 || i == 1 {
				return true
			}
		}
	}
	for v.cls(*p, big) == 0 {
		if p.Col == 0 && v.lineLen(p.Line) == 0 {
			break
		}
		if i = v.dec(p); i == -1 || i == 1 {
			return true
		}
	}
	return true
}

func (e *Engine) extendWord(v *view, count int, include, big bool) {
	p := e.cur
	for ; count > 0; count-- {
		if less(p, *e.visual) {
			if v.decl(&p) == -1 {
				return
			}
			if include != (v.cls(p, big) != 0) {
				if !bckWord(v, &p, 1, big, true) {
					return
				}
			} else {
				if !bckendWord(v, &p, big) {
					return
				}
				v.incl(&p)
			}
			continue
		}
		if v.incl(&p) == -1 {
			return
		}
		if include != (v.cls(p, big) == 0) {
			if !fwdWord(v, &p, 1, big, true) && count > 1 {
				return
			}
			v.oneleft(&p)
		} else if !endWord(v, &p, 1, big, true, true) {
			return
		}
	}
	if e.vmode == 'V' {
		e.vmode = 'v'
	}
	e.cur = p
	e.setWant(v)
}

func (e *Engine) extendPar(v *view, count int, include bool) {
	line := e.cur.Line
	dir := 1
	last := v.lines() - 1
	if line < e.visual.Line {
		dir = -1
	}
	edge := last
	if dir < 0 {
		edge = 0
	}
	for ; count > 0; count-- {
		if line == edge {
			break
		}
		prevWhite := -1
		for t := 0; t < 2; t++ {
			line += dir
			white := 0
			if v.lineWhite(line) {
				white = 1
			}
			if prevWhite == white {
				line -= dir
				break
			}
			for line != edge {
				next := line + dir
				nw := v.lineWhite(next)
				ps := false
				if white == 0 {
					l := line
					if dir > 0 {
						l = line + 1
					}
					ps = v.startPS(l, 0)
				}
				if nw != (white == 1) || ps {
					break
				}
				line += dir
			}
			if !include || line == edge {
				break
			}
			prevWhite = white
		}
	}
	e.vmode = 'V'
	e.cur = Pos{line, 0}
	e.setWant(v)
}

func (e *Engine) extendBlock(v *view, count int, include bool, open, close rune) {
	oldStart, oldEnd := *e.visual, e.cur
	cur := e.cur
	if less(*e.visual, e.cur) {
		cur = *e.visual
	} else {
		oldStart, oldEnd = e.cur, *e.visual
	}
	var start Pos
	found := false
	p := cur
	for ; count > 0; count-- {
		q, ok := findUnmatched(v, p, open, close, false)
		if !ok {
			return
		}
		p, start, found = q, q, true
	}
	if !found {
		return
	}
	end, ok := findUnmatched(v, start, open, close, true)
	if !ok {
		return
	}
	sol := false
	for !include {
		s, en := start, end
		v.incl(&s)
		sol = en.Col == 0
		v.decl(&en)
		for v.inindent(en, 1) {
			sol = true
			if v.decl(&en) != 0 {
				break
			}
		}
		if !less(s, oldStart) && !less(oldEnd, en) && s != en {
			q := oldStart
			v.decl(&q)
			ns, ok := findUnmatched(v, q, open, close, false)
			if !ok {
				return
			}
			ne, ok := findUnmatched(v, ns, open, close, true)
			if !ok {
				return
			}
			start, end, oldStart = ns, ne, ns
			continue
		}
		start, end = s, en
		break
	}
	if sol && v.gchar(end) != 0 {
		v.inc(&end)
	}
	e.visual = &start
	e.vmode = 'v'
	e.cur = end
	e.setWant(v)
}

// visualSentence is vim's current_sent() with a Visual area.
func (e *Engine) visualSentence(v *view, count int, include bool) {
	startPos := e.cur
	pos := startPos
	cur, _ := findSent(v, e.cur, 1, 1)
	extend := *e.visual != startPos
	for {
		if extend {
			if less(startPos, *e.visual) {
				atStart := true
				v.decl(&pos)
				for less(pos, cur) {
					if !isWhite(v.gchar(pos)) {
						atStart = false
						break
					}
					v.incl(&pos)
				}
				if !atStart {
					cur, _ = findSent(v, cur, -1, 1)
					if cur == startPos {
						atStart = true
					} else {
						cur, _ = findSent(v, cur, 1, 1)
					}
				}
				n := count
				if include {
					n *= 2
				}
				for ; n > 0; n-- {
					if atStart {
						findFirstBlank(v, &cur)
					}
					if !atStart || !include && !isWhite(v.gchar(cur)) {
						cur, _ = findSent(v, cur, -1, 1)
					}
					atStart = !atStart
				}
			} else {
				v.incl(&pos)
				atStart := true
				if pos != cur {
					atStart = false
					for less(pos, cur) {
						if !isWhite(v.gchar(pos)) {
							atStart = true
							break
						}
						v.incl(&pos)
					}
					if atStart {
						cur, _ = findSent(v, cur, -1, 1)
					} else {
						cur = startPos
					}
				}
				n := count
				if include {
					n *= 2
				}
				cur = findSentForward(v, cur, n, atStart)
			}
			e.cur = cur
			e.vmode = 'v'
			e.setWant(v)
			return
		}
		// One character selected: select whole sentences, as for an operator.
		p := startPos
		for isWhite(v.gchar(p)) {
			v.incl(&p)
		}
		var start Pos
		startBlank := false
		if p == cur {
			startBlank = true
			start = startPos
			findFirstBlank(v, &start)
		} else {
			cur, _ = findSent(v, cur, -1, 1)
			start = cur
		}
		ncount := count
		if include {
			ncount = count * 2
		} else if startBlank {
			ncount--
		}
		if ncount > 0 {
			cur = findSentForward(v, cur, ncount, true)
		} else {
			v.decl(&cur)
		}
		if include {
			if startBlank {
				findFirstBlank(v, &cur)
				if isWhite(v.gchar(cur)) {
					v.decl(&cur)
				}
			} else if !isWhite(v.gchar(cur)) {
				findFirstBlank(v, &start)
			}
		}
		if start == cur {
			extend = true
			pos = startPos
			continue
		}
		e.visual = &start
		e.cur = cur
		e.vmode = 'v'
		e.setWant(v)
		return
	}
}

// visualQuote is vim's current_quote() with a Visual area on one line.
func (e *Engine) visualQuote(v *view, count int, include bool, q byte) {
	if e.visual.Line != e.cur.Line {
		return
	}
	line := v.line(e.cur.Line)
	at := func(i int) byte {
		if i >= 0 && i < len(line) {
			return line[i]
		}
		return 0
	}
	vis := *e.visual
	befCurs := less(vis, e.cur)
	empty := vis == e.cur
	insideQuotes, selectedQuote := false, false
	colStart, colEnd := e.cur.Col, 0
	if !empty {
		var i int
		if befCurs {
			insideQuotes = vis.Col > 0 && at(vis.Col-1) == q && at(e.cur.Col) != 0 && at(e.cur.Col+1) == q
			i, colEnd = vis.Col, e.cur.Col
		} else {
			insideQuotes = e.cur.Col > 0 && at(e.cur.Col-1) == q && at(vis.Col) != 0 && at(vis.Col+1) == q
			i, colEnd = e.cur.Col, vis.Col
		}
		for ; i <= colEnd && at(i) != 0; i++ {
			if at(i) == q {
				selectedQuote = true
				break
			}
		}
	}
	switch {
	case !empty && at(colStart) == q:
		if befCurs {
			colStart = findNextQuote(line, colStart+1, q, false)
			if colStart < 0 {
				return
			}
			colEnd = findNextQuote(line, colStart+1, q, true)
			if colEnd < 0 {
				colEnd = colStart
				colStart = e.cur.Col
			}
		} else {
			colEnd = findPrevQuote(line, colStart, q)
			if at(colEnd) != q {
				return
			}
			colStart = findPrevQuote(line, colEnd, q)
			if at(colStart) != q {
				colStart = colEnd
				colEnd = e.cur.Col
			}
		}
	case at(colStart) == q || !empty:
		first := colStart
		if !empty {
			if befCurs {
				first = findNextQuote(line, colStart, q, false)
			} else {
				first = findPrevQuote(line, colStart, q)
			}
		}
		colStart = 0
		for {
			colStart = findNextQuote(line, colStart, q, false)
			if colStart < 0 || colStart > first {
				return
			}
			colEnd = findNextQuote(line, colStart+1, q, true)
			if colEnd < 0 {
				return
			}
			if colStart <= first && first <= colEnd {
				break
			}
			colStart = colEnd + 1
		}
	default:
		colStart = findPrevQuote(line, colStart, q)
		if at(colStart) != q {
			colStart = findNextQuote(line, colStart, q, false)
			if colStart < 0 {
				return
			}
		}
		colEnd = findNextQuote(line, colStart+1, q, true)
		if colEnd < 0 {
			return
		}
	}
	if include {
		if isWhite(rune(at(colEnd + 1))) {
			for isWhite(rune(at(colEnd + 1))) {
				colEnd++
			}
		} else {
			for colStart > 0 && isWhite(rune(at(colStart-1))) {
				colStart--
			}
		}
	}
	if !include && count < 2 && (empty || !insideQuotes) {
		colStart++
	}
	l := e.cur.Line
	if empty || befCurs && !selectedQuote && (insideQuotes || at(vis.Col) != q && (vis.Col == 0 || at(vis.Col-1) != q)) {
		e.visual = &Pos{l, colStart}
	}
	cur := Pos{l, colEnd}
	if include || count > 1 || !empty && insideQuotes {
		v.inc(&cur)
	}
	if empty || befCurs {
		v.dec(&cur)
		e.cur = cur
	} else {
		if insideQuotes || !selectedQuote && at(vis.Col) != q && (at(vis.Col) == 0 || at(vis.Col+1) != q) {
			v.dec(&cur)
			e.visual = &cur
		}
		e.cur = Pos{l, colStart}
	}
	e.vmode = 'v'
	e.setWant(v)
}
