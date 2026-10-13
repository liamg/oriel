package vim

import (
	"strings"
)

type motionKind int

const (
	exclusive motionKind = iota
	inclusive
	linewise
)

// target is where a motion goes and how an operator treats the text up to it.
type target struct {
	pos      Pos
	kind     motionKind
	jump     bool // sets the previous context mark and a jumplist entry
	keepWant bool // a vertical move: the remembered column stays
	wantEnd  bool // $: the remembered column becomes the end of every line
	keepCol  bool // linewise with 'nostartofline': land on the remembered column
}

// motionCtx is what a motion knows about how it was typed.
type motionCtx struct {
	count  int // 0 when none was typed
	arg    rune
	op     string // the pending operator, if any
	key    string
	search *target // a / or ? search already run from the command line
}

func (m motionCtx) count1() int { return max(m.count, 1) }

// motion moves from the cursor. It reports false when it can't move at all, which cancels an operator.
type motion func(e *Engine, v *view, m motionCtx) (target, bool)

var motions = map[string]motion{}

func init() {
	for k, m := range map[string]motion{
		"h": left, "<Left>": left, "<BS>": left, "<C-h>": left,
		"l": right, "<Right>": right, " ": right,
		"j": down, "<Down>": down, "<C-j>": down, "<C-n>": down, "gj": down, "<NL>": down,
		"k": up, "<Up>": up, "<C-p>": up, "gk": up,
		"+": lineDown, "<CR>": lineDown, "<C-m>": lineDown, "-": lineUp, "_": lineCurrent,
		"0": col0, "<Home>": col0, "^": firstNonBlank, "$": dollar, "<End>": dollar, "g_": lastNonBlank,
		"|": column, "gg": gotoLine, "G": gotoLine, "go": gotoByte,
		"w": word, "W": word, "e": wordEnd, "E": wordEnd, "b": wordBack, "B": wordBack, "ge": wordEndBack, "gE": wordEndBack,
		"f": findChar, "F": findChar, "t": findChar, "T": findChar, ";": findRepeat, ",": findRepeat,
		"}": paragraph, "{": paragraph, ")": sentence, "(": sentence, "%": percent,
		"'": markLine, "`": markChar, "g'": markLine, "g`": markChar,
		"[[": section, "]]": section, "[]": section, "][": section,
		"[(": bracketBlock, "[{": bracketBlock, "])": bracketBlock, "]}": bracketBlock,
		"[m": bracketBlock, "]m": bracketBlock, "[M": bracketBlock, "]M": bracketBlock,
		"H": screenLine, "M": screenLine, "L": screenLine, "*": starSearch, "#": starSearch, "g*": starSearch, "g#": starSearch, "n": searchNext, "N": searchNext,
		"/": searchTyped, "?": searchTyped, "]`": nextMark, "[`": nextMark, "]'": nextMark, "['": nextMark,
	} {
		motions[k] = m
	}
}

// RegisterMotion adds a motion that plain keys and operators both use. A plugin's motion shadows a built-in one.
func RegisterMotion(key string, m func(e *Engine, from Pos, count int) (Pos, bool), linewiseMotion, inclusiveMotion bool) {
	kind := exclusive
	if linewiseMotion {
		kind = linewise
	} else if inclusiveMotion {
		kind = inclusive
	}
	motions[key] = func(e *Engine, v *view, mc motionCtx) (target, bool) {
		p, ok := m(e, e.cur, mc.count)
		return target{pos: p, kind: kind}, ok
	}
}

// move runs a motion without an operator.
func (e *Engine) move(v *view, c cmd) {
	t, ok := motions[c.key](e, v, motionCtx{count: c.count, arg: c.arg, key: c.key})
	if !ok {
		e.failed = true
		return
	}
	if t.jump {
		e.setPcmark()
	}
	e.cur = t.pos
	switch {
	case t.keepWant || t.keepCol:
		e.cur.Col = v.colAt(e.cur.Line, e.want, false)
	case t.wantEnd:
		e.want = maxCol
	}
	e.clamp(v)
	if !t.keepWant && !t.keepCol && !t.wantEnd {
		e.setWant(v)
	}
	if foldOpeners[c.key] && e.feeding == 0 {
		e.openCursorFold()
	}
}

// foldOpeners are the motions that open a closed fold they move into, as the default 'foldopen'
// (block,hor,mark,percent,quickfix,search,tag,undo) has it.
var foldOpeners = map[string]bool{
	"h": true, "l": true, "<Left>": true, "<Right>": true, " ": true, "<BS>": true, "<C-h>": true, "0": true,
	"<Home>": true, "^": true, "$": true, "<End>": true, "g_": true, "|": true, "f": true, "F": true, "t": true,
	"T": true, ";": true, ",": true,
	"(": true, ")": true, "{": true, "}": true, "[[": true, "]]": true, "[]": true, "][": true, "[(": true,
	"[{": true, "])": true, "]}": true, "%": true,
	"'": true, "`": true, "g'": true, "g`": true, "]`": true, "[`": true, "]'": true, "['": true,
	"n": true, "N": true, "*": true, "#": true, "g*": true, "g#": true, "/": true, "?": true,
}

func left(e *Engine, v *view, m motionCtx) (target, bool) {
	p := e.cur
	moved := false
	for range m.count1() {
		if p.Col > 0 {
			p.Col = v.prevCol(p.Line, p.Col)
			moved = true
			continue
		}
		// <BS> wraps to the end of the previous line ('whichwrap' has "b").
		if m.key == "<BS>" && p.Line > 0 {
			p.Line--
			p.Col = v.lineLen(p.Line)
			if m.op == "" {
				p.Col = v.lastCol(p.Line)
			}
			moved = true
			continue
		}
		break
	}
	return target{pos: p}, moved
}

func right(e *Engine, v *view, m motionCtx) (target, bool) {
	p := e.cur
	t := target{pos: p}
	moved := false
	for n := m.count1(); n > 0; n-- {
		l := v.lineLen(p.Line)
		_, size := v.char(p)
		if p.Col+size < l {
			p.Col += size
			moved = true
			continue
		}
		// <Space> wraps to the next line ('whichwrap' has "s").
		if m.key == " " && p.Line < v.lines()-1 {
			if m.op != "" && t.kind == exclusive && l > 0 {
				t.kind = inclusive
			} else {
				p.Line++
				p.Col = 0
				t.kind = exclusive
			}
			moved = true
			continue
		}
		if m.op != "" && l > 0 {
			t.kind = inclusive
			moved = true
		}
		break
	}
	t.pos = p
	return t, moved || m.op != ""
}

func down(e *Engine, v *view, m motionCtx) (target, bool) {
	return vertical(e, v, m.count1())
}

func up(e *Engine, v *view, m motionCtx) (target, bool) {
	return vertical(e, v, -m.count1())
}

func vertical(e *Engine, v *view, n int) (target, bool) {
	if n > 0 && e.cur.Line >= v.lines()-1 || n < 0 && e.cur.Line == 0 {
		return target{}, false
	}
	line, ok := foldStep(v, e.cur.Line, n)
	if !ok {
		return target{}, false
	}
	return target{pos: Pos{Line: line, Col: v.colAt(line, e.want, false)}, kind: linewise, keepWant: true}, true
}

// foldStep is vim's cursor_down() and cursor_up(): n lines on, a closed fold counting as one.
func foldStep(v *view, line, n int) (int, bool) {
	if v.folds == nil || !v.folds.any() {
		return min(max(line+n, 0), v.lines()-1), true
	}
	last := v.lines() - 1
	if n > 0 {
		if _, end, _ := v.folded(line); end >= last {
			return line, false
		}
		for ; n > 0; n-- {
			_, end, _ := v.folded(line)
			line = end + 1
			if line >= last {
				break
			}
		}
		return min(line, last), true
	}
	line, _, _ = v.folded(line)
	for n = -n; n > 0; n-- {
		line--
		if line <= 0 {
			break
		}
		line, _, _ = v.folded(line)
	}
	return max(line, 0), true
}

func lineDown(e *Engine, v *view, m motionCtx) (target, bool) {
	line := e.cur.Line + m.count1()
	if line >= v.lines() {
		return target{}, false
	}
	line, _ = foldStep(v, e.cur.Line, m.count1())
	return target{pos: Pos{line, v.firstNonBlank(line)}, kind: linewise}, true
}

func lineUp(e *Engine, v *view, m motionCtx) (target, bool) {
	line := e.cur.Line - m.count1()
	if line < 0 {
		return target{}, false
	}
	line, _ = foldStep(v, e.cur.Line, -m.count1())
	return target{pos: Pos{line, v.firstNonBlank(line)}, kind: linewise}, true
}

func lineCurrent(e *Engine, v *view, m motionCtx) (target, bool) {
	line := e.cur.Line + m.count1() - 1
	if line >= v.lines() {
		return target{}, false
	}
	return target{pos: Pos{line, v.firstNonBlank(line)}, kind: linewise}, true
}

func col0(e *Engine, v *view, m motionCtx) (target, bool) {
	return target{pos: Pos{e.cur.Line, 0}}, true
}

func firstNonBlank(e *Engine, v *view, m motionCtx) (target, bool) {
	return target{pos: Pos{e.cur.Line, v.firstNonBlank(e.cur.Line)}}, true
}

func dollar(e *Engine, v *view, m motionCtx) (target, bool) {
	line := e.cur.Line + m.count1() - 1
	if line >= v.lines() {
		if e.cur.Line == v.lines()-1 || m.op != "" {
			return target{}, false
		}
		line = v.lines() - 1
	}
	return target{pos: Pos{line, v.lastCol(line)}, kind: inclusive, wantEnd: true}, true
}

func lastNonBlank(e *Engine, v *view, m motionCtx) (target, bool) {
	line := e.cur.Line + m.count1() - 1
	if line >= v.lines() {
		return target{}, false
	}
	l := v.line(line)
	end := len(l)
	for end > 0 && isWhite(rune(l[end-1])) {
		end--
	}
	col := v.prevCol(line, end)
	if end == 0 {
		col = 0
	}
	return target{pos: Pos{line, col}, kind: inclusive}, true
}

func column(e *Engine, v *view, m motionCtx) (target, bool) {
	col := v.colAt(e.cur.Line, m.count1()-1, false)
	e.want = m.count1() - 1
	return target{pos: Pos{e.cur.Line, col}, keepWant: true}, true
}

func gotoLine(e *Engine, v *view, m motionCtx) (target, bool) {
	line := 0
	switch {
	case m.count > 0:
		line = min(m.count, v.lines()) - 1
	case m.key == "G":
		line = v.lines() - 1
	}
	return target{pos: Pos{line, 0}, kind: linewise, jump: true, keepCol: true}, true
}

func gotoByte(e *Engine, v *view, m motionCtx) (target, bool) {
	off := min(m.count1()-1, v.s.Len())
	p := v.pos(off)
	if p.Col >= v.lineLen(p.Line) && p.Col > 0 {
		p.Col = v.lastCol(p.Line)
	}
	return target{pos: p, jump: true}, true
}

// Word motions follow vim's fwd_word, end_word, bck_word and bckend_word.

func (v *view) cls(p Pos, big bool) int { return charClass(v.gchar(p), big) }

func word(e *Engine, v *view, m motionCtx) (target, bool) {
	big := m.key == "W"
	p := e.cur
	start := p
	t := target{}
	if m.op == "c" {
		// "cw" on a word changes to its end, like "ce".
		if r := v.gchar(p); r != 0 && !isWhite(r) {
			ok := endWord(v, &p, m.count1(), big, true, false)
			t.kind = inclusive
			if !ok {
				return target{}, false
			}
			t.pos = p
			return t, true
		}
	}
	ok := fwdWord(v, &p, m.count1(), big, m.op != "")
	if start.Line < p.Line || start.Line == p.Line && start.Col < p.Col {
		if p.Col > 0 && p.Col >= v.lineLen(p.Line) {
			p.Col = v.lastCol(p.Line)
			t.kind = inclusive
		}
	}
	t.pos = p
	return t, ok || m.op != ""
}

func fwdWord(v *view, p *Pos, count int, big, eol bool) bool {
	for count--; count >= 0; count-- {
		if _, end, ok := v.folded(p.Line); ok {
			*p = Pos{end, v.lastCol(end)}
		}
		sclass := v.cls(*p, big)
		last := p.Line == v.lines()-1
		i := v.inc(p)
		if i == -1 || i >= 1 && last {
			return false
		}
		if i >= 1 && eol && count == 0 {
			return true
		}
		if sclass != 0 {
			for v.cls(*p, big) == sclass {
				i = v.inc(p)
				if i == -1 || i >= 1 && eol && count == 0 {
					return true
				}
			}
		}
		for v.cls(*p, big) == 0 {
			if p.Col == 0 && v.lineLen(p.Line) == 0 {
				break
			}
			i = v.inc(p)
			if i == -1 || i >= 1 && eol && count == 0 {
				return true
			}
		}
	}
	return true
}

func endWord(v *view, p *Pos, count int, big, stop, empty bool) bool {
	for ; count > 0; count-- {
		if _, end, ok := v.folded(p.Line); ok {
			*p = Pos{end, v.lastCol(end)}
		}
		sclass := v.cls(*p, big)
		if v.inc(p) == -1 {
			return false
		}
		if v.cls(*p, big) == sclass && sclass != 0 {
			if skipChars(v, p, sclass, big, true) {
				return false
			}
		} else if !stop || sclass == 0 {
			finished := false
			for v.cls(*p, big) == 0 {
				if p.Col == 0 && v.lineLen(p.Line) == 0 && empty {
					finished = true
					break
				}
				if v.inc(p) == -1 {
					return false
				}
			}
			if finished {
				stop = false
				continue
			}
			if skipChars(v, p, v.cls(*p, big), big, true) {
				return false
			}
		}
		v.dec(p)
		stop = false
	}
	return true
}

func skipChars(v *view, p *Pos, class int, big, fwd bool) bool {
	for v.cls(*p, big) == class {
		var r int
		if fwd {
			r = v.inc(p)
		} else {
			r = v.dec(p)
		}
		if r == -1 {
			return true
		}
	}
	return false
}

func bckWord(v *view, p *Pos, count int, big, stop bool) bool {
	for ; count > 0; count-- {
		if top, _, ok := v.folded(p.Line); ok {
			*p = Pos{top, 0}
		}
		sclass := v.cls(*p, big)
		if v.dec(p) == -1 {
			return false
		}
		finished := false
		if !stop || sclass == v.cls(*p, big) || sclass == 0 {
			for v.cls(*p, big) == 0 {
				if p.Col == 0 && v.lineLen(p.Line) == 0 {
					finished = true
					break
				}
				if v.dec(p) == -1 {
					return true
				}
			}
			if !finished && skipChars(v, p, v.cls(*p, big), big, false) {
				return true
			}
		}
		if !finished {
			v.inc(p)
		}
		stop = false
	}
	return true
}

func wordEnd(e *Engine, v *view, m motionCtx) (target, bool) {
	p := e.cur
	if !endWord(v, &p, m.count1(), m.key == "E", false, false) {
		return target{}, false
	}
	return target{pos: p, kind: inclusive}, true
}

func wordBack(e *Engine, v *view, m motionCtx) (target, bool) {
	p := e.cur
	if !bckWord(v, &p, m.count1(), m.key == "B", false) {
		return target{}, false
	}
	return target{pos: p}, true
}

func wordEndBack(e *Engine, v *view, m motionCtx) (target, bool) {
	p := e.cur
	big := m.key == "gE"
	for count := m.count1(); count > 0; count-- {
		sclass := v.cls(p, big)
		if v.dec(&p) == -1 {
			return target{}, false
		}
		done := false
		if sclass != 0 {
			for v.cls(p, big) == sclass {
				if v.dec(&p) == -1 {
					done = true
					break
				}
			}
		}
		for !done && v.cls(p, big) == 0 {
			if p.Col == 0 && v.lineLen(p.Line) == 0 {
				break
			}
			if v.dec(&p) == -1 {
				break
			}
		}
	}
	return target{pos: p, kind: inclusive}, true
}

// findCmd is the last f, F, t or T, for ; and ,.
type findCmd struct {
	key string
	r   rune
}

func findChar(e *Engine, v *view, m motionCtx) (target, bool) {
	e.find = findCmd{m.key, m.arg}
	return searchChar(e, v, m.key, m.arg, m.count1(), false)
}

func findRepeat(e *Engine, v *view, m motionCtx) (target, bool) {
	if e.find.key == "" {
		return target{}, false
	}
	key := e.find.key
	if m.key == "," {
		key = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[key]
	}
	return searchChar(e, v, key, e.find.r, m.count1(), true)
}

func searchChar(e *Engine, v *view, key string, r rune, count int, repeat bool) (target, bool) {
	l := v.line(e.cur.Line)
	col := e.cur.Col
	fwd := key == "f" || key == "t"
	till := key == "t" || key == "T"
	for n := count; n > 0; n-- {
		start := col
		for {
			if fwd {
				_, size := v.char(Pos{e.cur.Line, col})
				if col+size >= len(l) {
					return target{}, false
				}
				col += size
			} else {
				if col == 0 {
					return target{}, false
				}
				col = v.prevCol(e.cur.Line, col)
			}
			if v.gchar(Pos{e.cur.Line, col}) != r {
				continue
			}
			// Without "," in 'cpoptions', ; after t skips a match right next to the cursor.
			if till && repeat && n == count {
				adj := col
				if fwd {
					adj = v.prevCol(e.cur.Line, col)
				} else {
					_, size := v.char(Pos{e.cur.Line, col})
					adj = col + size
				}
				if adj == start && count == 1 {
					continue
				}
			}
			break
		}
	}
	if till {
		if fwd {
			col = v.prevCol(e.cur.Line, col)
		} else {
			_, size := v.char(Pos{e.cur.Line, col})
			col += size
		}
	}
	kind := exclusive
	if fwd {
		kind = inclusive
	}
	return target{pos: Pos{e.cur.Line, col}, kind: kind}, true
}

// Default 'paragraphs' and 'sections': nroff macros that start a paragraph or section.
const (
	paraMacros    = "IPLPPPQPP TPHPLIPpLpItpplpipbp"
	sectionMacros = "SHNHH HUnhsh"
)

// startPS is vim's startPS(): whether a line starts a paragraph (para 0: an empty line) or a section
// (para '{' or '}' in the first column), or holds a form feed or one of the macros.
func (v *view) startPS(line int, para rune) bool {
	l := v.line(line)
	var c rune
	if len(l) > 0 {
		c = rune(l[0])
	}
	if c == para || c == '\f' {
		return true
	}
	return c == '.' && (inMacro(sectionMacros, l[1:]) || para == 0 && inMacro(paraMacros, l[1:]))
}

// inMacro is vim's inmacro(): whether s starts with one of the two-letter macros in opt.
// A space in opt matches a space in s or its end.
func inMacro(opt string, s []byte) bool {
	at := func(b []byte, i int) byte {
		if i < len(b) {
			return b[i]
		}
		return 0
	}
	for i := 0; i < len(opt); i += 2 {
		m0, m1 := opt[i], at([]byte(opt), i+1)
		s0, s1 := at(s, 0), at(s, 1)
		if (m0 == s0 || m0 == ' ' && (s0 == 0 || s0 == ' ')) &&
			(m1 == s1 || (m1 == 0 || m1 == ' ') && (s0 == 0 || s1 == 0 || s1 == ' ')) {
			return true
		}
	}
	return false
}

// section is [[, ]], [] and ][: vim's findpar() looking for a brace in the first column.
func section(e *Engine, v *view, m motionCtx) (target, bool) {
	dir := 1
	if m.key[0] == '[' {
		dir = -1
	}
	what := '{'
	if m.key == "[]" || m.key == "][" {
		what = '}'
	}
	curr := e.cur.Line
	for count := m.count1(); count > 0; count-- {
		for first := true; ; first = false {
			if !first && v.startPS(curr, what) {
				break
			}
			curr += dir
			if curr < 0 || curr >= v.lines() {
				if count > 1 {
					return target{}, false
				}
				curr -= dir
				break
			}
		}
	}
	t := target{pos: Pos{curr, 0}, jump: true}
	if curr == v.lines()-1 && what != '}' && dir == 1 {
		if n := v.lineLen(curr); n > 0 {
			t.pos.Col = v.lastCol(curr)
			t.kind = inclusive
		}
	}
	return t, true
}

// unmatched finds the unmatched brace c from p: backwards for an opening one, forwards for a closing one.
func unmatched(v *view, p Pos, c rune) (Pos, bool) {
	switch c {
	case '{', '}':
		return findUnmatched(v, p, '{', '}', c == '}')
	case '(', ')':
		return findUnmatched(v, p, '(', ')', c == ')')
	}
	return p, false
}

// bracketBlock is [( [{ ]) ]} and the method motions [m ]m [M ]M, after vim's nv_brackets().
func bracketBlock(e *Engine, v *view, m motionCtx) (target, bool) {
	back := m.key[0] == '['
	ch := rune(m.key[1])
	method := ch == 'm' || ch == 'M'
	findc := ch
	n := m.count1()
	if method {
		findc = '}'
		if back {
			findc = '{'
		}
		n = 9999
	}
	cur := e.cur
	var pos, newPos, prevPos *Pos
	for ; n > 0; n-- {
		q, ok := unmatched(v, cur, findc)
		if !ok {
			if newPos == nil {
				if !method {
					return target{}, false
				}
			} else {
				pos = newPos
			}
			break
		}
		prevPos = newPos
		cur = q
		newPos = &q
		pos = newPos
	}
	if method {
		norm := (findc == '{') == (ch == 'm')
		n = m.count1()
		if prevPos != nil {
			pos = prevPos
			cur = *prevPos
			if norm {
				n--
			}
		} else {
			pos = nil
			cur = e.cur
		}
		for ; n > 0; n-- {
			for {
				var r int
				if findc == '{' {
					r = v.dec(&cur)
				} else {
					r = v.inc(&cur)
				}
				if r < 0 {
					if pos == nil {
						return target{}, false
					}
					n = 0
					break
				}
				c := v.gchar(cur)
				if c != '{' && c != '}' {
					continue
				}
				switch {
				case c == findc && norm || n == 1 && !norm:
					p := cur
					newPos, pos = &p, &p
					n = 0
				case newPos == nil:
					p := cur
					newPos, pos = &p, &p
				default:
					q, ok := unmatched(v, cur, findc)
					if !ok {
						pos = nil
						n = 0
					} else {
						pos, cur = &q, q
					}
				}
				break
			}
			if n == 0 {
				break
			}
		}
		if pos == nil && newPos != nil {
			return target{}, false
		}
	}
	if pos == nil {
		return target{}, false
	}
	return target{pos: *pos, jump: true}, true
}

func paragraph(e *Engine, v *view, m motionCtx) (target, bool) {
	dir := 1
	if m.key == "{" {
		dir = -1
	}
	curr := e.cur.Line
	for count := m.count1(); count > 0; count-- {
		didSkip := false
		for first := true; ; first = false {
			if v.lineLen(curr) > 0 {
				didSkip = true
			}
			if !first && didSkip && v.startPS(curr, 0) {
				break
			}
			curr += dir
			if curr < 0 || curr >= v.lines() {
				if count > 1 {
					return target{}, false
				}
				curr -= dir
				break
			}
		}
	}
	t := target{pos: Pos{curr, 0}, jump: true}
	if curr == v.lines()-1 && dir == 1 {
		if n := v.lineLen(curr); n > 0 {
			t.pos.Col = v.lastCol(curr)
			t.kind = inclusive
		}
	}
	return t, true
}

func sentence(e *Engine, v *view, m motionCtx) (target, bool) {
	dir := 1
	if m.key == "(" {
		dir = -1
	}
	p, ok := findSent(v, e.cur, dir, m.count1())
	if !ok {
		return target{}, false
	}
	return target{pos: p, jump: true}, true
}

// findSent is vim's findsent(): the start of the count-th sentence in direction dir.
func findSent(v *view, pos Pos, dir, count int) (Pos, bool) {
	step := v.incl
	if dir < 0 {
		step = v.decl
	}
	noskip := false
	for ; count > 0; count-- {
		found := false
		if v.gchar(pos) == 0 {
			for {
				if step(&pos) == -1 {
					break
				}
				if v.gchar(pos) != 0 {
					break
				}
			}
			if dir > 0 {
				found = true
			}
		} else if dir > 0 && pos.Col == 0 && v.startPS(pos.Line, 0) {
			if pos.Line == v.lines()-1 {
				return pos, false
			}
			pos.Line++
			found = true
		} else if dir < 0 {
			v.decl(&pos)
		}
		if !found {
			foundDot := false
			for {
				c := v.gchar(pos)
				if !(isWhite(c) || strings.ContainsRune(".!?)]\"'", c) && c != 0) {
					break
				}
				tpos := pos
				if v.decl(&tpos) == -1 || v.lineLen(tpos.Line) == 0 && dir > 0 {
					break
				}
				if foundDot {
					break
				}
				if strings.ContainsRune(".!?", c) {
					foundDot = true
				}
				if strings.ContainsRune(")]\"'", c) && !strings.ContainsRune(".!?)]\"'", v.gchar(tpos)) {
					break
				}
				v.decl(&pos)
			}
			startLine := pos.Line
			for {
				c := v.gchar(pos)
				if c == 0 || pos.Col == 0 && v.startPS(pos.Line, 0) {
					if dir < 0 && pos.Line != startLine {
						pos.Line++
						pos.Col = 0
					}
					break
				}
				if c == '.' || c == '!' || c == '?' {
					tpos := pos
					r := 0
					var tc rune
					for {
						if r = v.inc(&tpos); r == -1 {
							break
						}
						tc = v.gchar(tpos)
						if !strings.ContainsRune(")]\"'", tc) || tc == 0 {
							break
						}
					}
					if r == -1 || tc == ' ' || tc == '\t' || tc == 0 {
						pos = tpos
						if v.gchar(pos) == 0 {
							v.inc(&pos)
						}
						break
					}
				}
				if step(&pos) == -1 {
					if count > 1 {
						return pos, false
					}
					noskip = true
					break
				}
			}
		}
		for !noskip {
			c := v.gchar(pos)
			if c != ' ' && c != '\t' {
				break
			}
			if v.incl(&pos) == -1 {
				break
			}
		}
	}
	return pos, true
}

func percent(e *Engine, v *view, m motionCtx) (target, bool) {
	if m.count > 0 {
		if m.count > 100 {
			return target{}, false
		}
		line := (m.count*v.lines() + 99) / 100
		line = max(line, 1) - 1
		return target{pos: Pos{line, 0}, kind: linewise, jump: true, keepCol: true}, true
	}
	p, ok := matchPair(v, e.cur)
	if !ok {
		return target{}, false
	}
	return target{pos: p, kind: inclusive, jump: true}, true
}

var pairs = map[rune]rune{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}

// matchPair finds the bracket or comment end matching the first one at or after p on its line.
func matchPair(v *view, p Pos) (Pos, bool) {
	l := v.line(p.Line)
	col := p.Col
	for ; col < len(l); col++ {
		c := rune(l[col])
		if _, ok := pairs[c]; ok {
			break
		}
		if c == '/' && col+1 < len(l) && l[col+1] == '*' || c == '*' && col+1 < len(l) && l[col+1] == '/' {
			break
		}
		if c == '*' && col > 0 && l[col-1] == '/' && col == p.Col {
			col--
			break
		}
	}
	if col >= len(l) {
		return p, false
	}
	start := Pos{p.Line, col}
	c := rune(l[col])
	if c == '/' || c == '*' {
		return matchComment(v, start, c == '/')
	}
	open := c == '(' || c == '[' || c == '{'
	return findMatch(v, start, c, pairs[c], open)
}

// findMatch walks from p, which holds c, to its unmatched partner, skipping pairs inside double quotes
// on the starting line the way vim does when the cursor isn't inside a string.
func findMatch(v *view, p Pos, c, partner rune, fwd bool) (Pos, bool) {
	depth := 0
	q := p
	for {
		var r int
		if fwd {
			r = v.inc(&q)
		} else {
			r = v.dec(&q)
		}
		if r == -1 {
			return p, false
		}
		ch := v.gchar(q)
		if ch == '"' && inQuotes(v, q) {
			continue
		}
		if inQuotes(v, q) && !inQuotes(v, p) {
			continue
		}
		switch ch {
		case c:
			depth++
		case partner:
			if depth == 0 {
				return q, true
			}
			depth--
		}
	}
}

// inQuotes reports whether p sits between a pair of unescaped double quotes on its line.
func inQuotes(v *view, p Pos) bool {
	l := v.line(p.Line)
	in := false
	for i := 0; i < p.Col && i < len(l); i++ {
		switch l[i] {
		case '\\':
			i++
		case '"':
			in = !in
		}
	}
	return in
}

func matchComment(v *view, p Pos, open bool) (Pos, bool) {
	s := v.s.String()
	off := v.offset(p)
	if open {
		i := strings.Index(s[off+2:], "*/")
		if i < 0 {
			return p, false
		}
		return v.pos(off + 2 + i + 1), true
	}
	i := strings.LastIndex(s[:off], "/*")
	if i < 0 {
		return p, false
	}
	return v.pos(i), true
}

func markLine(e *Engine, v *view, m motionCtx) (target, bool) {
	p, ok := e.markPos(m.arg)
	if !ok || p.Line >= v.lines() {
		e.notify(errMarkNotSet.with())
		return target{}, false
	}
	return target{pos: Pos{p.Line, v.firstNonBlank(p.Line)}, kind: linewise, jump: m.key[0] != 'g'}, true
}

func markChar(e *Engine, v *view, m motionCtx) (target, bool) {
	p, ok := e.markPos(m.arg)
	if !ok || p.Line >= v.lines() {
		e.notify(errMarkNotSet.with())
		return target{}, false
	}
	p.Col = min(p.Col, v.lineLen(p.Line))
	return target{pos: p, jump: m.key[0] != 'g'}, true
}

// nextMark is ]` and friends: the next or previous lowercase mark.
func nextMark(e *Engine, v *view, m motionCtx) (target, bool) {
	fwd := m.key[0] == ']'
	byLine := m.key[1] == '\''
	p := e.cur
	for n := m.count1(); n > 0; n-- {
		best, found := Pos{}, false
		for r := 'a'; r <= 'z'; r++ {
			q, ok := e.marks[r]
			if !ok {
				continue
			}
			after := q.Line > p.Line || !byLine && q.Line == p.Line && q.Col > p.Col
			before := q.Line < p.Line || !byLine && q.Line == p.Line && q.Col < p.Col
			if fwd && after && (!found || q.Line < best.Line || q.Line == best.Line && q.Col < best.Col) ||
				!fwd && before && (!found || q.Line > best.Line || q.Line == best.Line && q.Col > best.Col) {
				best, found = q, true
			}
		}
		if !found {
			break
		}
		p = best
	}
	if m.key[1] == '\'' {
		return target{pos: Pos{p.Line, v.firstNonBlank(p.Line)}, kind: linewise}, true
	}
	return target{pos: p}, true
}

// searchTyped is / and ?: the command line has already been typed, and the search's target rides on the command.
func searchTyped(e *Engine, v *view, m motionCtx) (target, bool) {
	if m.search == nil {
		return target{}, false
	}
	return *m.search, true
}
