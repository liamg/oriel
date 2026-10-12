package vim

import (
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

// insertState is one insert-mode session.
type insertState struct {
	kind   string // the command that started it: "i", "a", "o", "c"…
	count  int
	keys   []string // typed, for "." and a count's repeats
	aiLine int      // the line holding only autoindent nothing has been typed on, or -1
	start  Pos

	block        *blockInsert
	pendingCtrlG bool
	pendingCtrlR bool
	pendingCtrlV bool
	replaced     []string // in Replace mode, what each typed character covered, for <BS> to put back
}

// insertCmd runs i, a, I, A, o, O, gi, gI and R.
func (e *Engine) insertCmd(v *view, c cmd) {
	switch c.key {
	case "a":
		if v.lineLen(e.cur.Line) > 0 {
			_, n := v.char(e.cur)
			e.cur.Col += n
		}
	case "A":
		e.cur.Col = v.lineLen(e.cur.Line)
	case "I":
		e.cur.Col = v.indentEnd(e.cur.Line)
	case "gI":
		e.cur.Col = 0
	case "gi":
		if p, ok := e.marks['^']; ok && p.Line < v.lines() {
			e.cur = Pos{p.Line, min(p.Col, v.lineLen(p.Line))}
		}
	case "o", "O":
		if top, end, ok := v.folded(e.cur.Line); ok {
			e.cur.Line = top
			if c.key == "o" {
				e.cur.Line = end
			}
		}
		e.openLine(v, c.key == "o")
	}
	e.startInsert(e.view(), c, c.key)
	if c.key == "R" {
		e.mode = Replace
	}
}

// openLine opens a new line below or above the cursor's, autoindented like it.
func (e *Engine) openLine(v *view, below bool) {
	ind := string(v.line(e.cur.Line)[:v.indentEnd(e.cur.Line)])
	if below {
		at := v.s.LineStart(e.cur.Line) + v.lineLen(e.cur.Line)
		e.do(e.group, text.Replace{Range: text.Range{Start: at, End: at}, Text: []byte("\n" + ind)})
		e.cur = Pos{e.cur.Line + 1, len(ind)}
	} else {
		at := v.s.LineStart(e.cur.Line)
		e.do(e.group, text.Replace{Range: text.Range{Start: at, End: at}, Text: []byte(ind + "\n")})
		e.cur = Pos{e.cur.Line, len(ind)}
	}
	if e.ins != nil {
		e.ins.aiLine = -1
	}
	if ind != "" {
		e.aiPending = e.cur.Line
	} else {
		e.aiPending = -1
	}
}

func (e *Engine) startInsert(v *view, c cmd, kind string) {
	e.openCursorFold()
	if e.group == 0 {
		e.group = e.newGroup()
	}
	e.mode = Insert
	e.ins = &insertState{kind: kind, count: c.count1(), aiLine: e.aiPending, start: e.cur}
	e.aiPending = -1
	if kind == "c" {
		e.ins.count = 1
	}
}

func (e *Engine) insertKey(k string) {
	if e.rec != nil {
		e.rec.ins = append(e.rec.ins, k)
	}
	in := e.ins
	if e.insertAbbrev(k) {
		return
	}
	if in.pendingCtrlV {
		in.pendingCtrlV = false
		in.keys = append(in.keys, "<C-v>", k)
		e.typeLiteral(k)
		return
	}
	switch k {
	case "<C-v>", "<C-q>":
		in.pendingCtrlV = true
		return
	case "<C-]>":
		return
	case "<Esc>", "<C-[>", "<C-c>":
		e.finishInsert()
		return
	}
	in.keys = append(in.keys, k)
	e.typeKey(k)
}

// typeKey does what one key does in insert or replace mode.
func (e *Engine) typeKey(k string) {
	v := e.view()
	in := e.ins
	off := v.offset(e.cur)
	switch k {
	case "<CR>", "<NL>", "<C-m>", "<C-j>":
		e.dropAutoindent(v)
		v = e.view()
		off = v.offset(e.cur)
		ind := string(v.line(e.cur.Line)[:min(v.indentEnd(e.cur.Line), e.cur.Col)])
		rest := v.line(e.cur.Line)[e.cur.Col:]
		skip := 0
		for skip < len(rest) && isWhite(rune(rest[skip])) {
			skip++
		}
		e.do(e.group, text.Replace{Range: text.Range{Start: off, End: off + skip}, Text: []byte("\n" + ind)})
		e.cur = Pos{e.cur.Line + 1, len(ind)}
		in.aiLine = -1
		if ind != "" {
			in.aiLine = e.cur.Line
		}
		return
	case "<BS>", "<C-h>":
		if e.cur.Col == 0 && e.cur.Line == 0 {
			return
		}
		prev := v.pos(off - 1)
		if e.cur.Col > 0 {
			prev = Pos{e.cur.Line, v.prevCol(e.cur.Line, e.cur.Col)}
		}
		if e.mode == Replace && e.cur.Line == in.start.Line && e.cur.Col > in.start.Col {
			e.cur = prev
			var orig string
			if n := len(in.replaced); n > 0 {
				orig = in.replaced[n-1]
				in.replaced = in.replaced[:n-1]
			}
			e.do(e.group, text.Replace{Range: text.Range{Start: v.offset(prev), End: off}, Text: []byte(orig)})
			return
		}
		if e.mode == Replace {
			e.cur = prev
			return
		}
		e.do(e.group, text.Replace{Range: text.Range{Start: v.offset(prev), End: off}})
		e.cur = prev
		return
	case "<Del>":
		if off < v.s.Len() {
			_, n := v.char(e.cur)
			if n == 0 {
				n = 1
			}
			e.do(e.group, text.Replace{Range: text.Range{Start: off, End: off + n}})
		}
		return
	case "<Left>":
		e.cur.Col = v.prevCol(e.cur.Line, e.cur.Col)
		return
	case "<Right>":
		if _, n := v.char(e.cur); n > 0 {
			e.cur.Col += n
		}
		return
	case "<Up>", "<Down>":
		d := -1
		if k == "<Down>" {
			d = 1
		}
		if l := e.cur.Line + d; l >= 0 && l < v.lines() {
			e.cur = Pos{l, v.colAt(l, v.vcol(e.cur.Line, e.cur.Col), true)}
		}
		return
	case "<C-g>":
		in.pendingCtrlG = true
		return
	case "<C-v>", "<C-q>":
		in.pendingCtrlV = true
		return
	case "<C-r>":
		in.pendingCtrlR = true
		return
	}
	if in.pendingCtrlR {
		in.pendingCtrlR = false
		if r, ok := argRune(k); ok {
			if t, ok := e.Register(r); ok {
				for _, c := range t {
					switch c {
					case '\n':
						e.typeKey("<CR>")
					case '\t':
						e.typeKey("<Tab>")
					default:
						e.typeKey(string(c))
					}
				}
			}
		}
		return
	}
	if in.pendingCtrlV {
		in.pendingCtrlV = false
		e.typeLiteral(k)
		return
	}
	if in.pendingCtrlG {
		in.pendingCtrlG = false
		if k == "u" {
			e.group = e.newGroup()
		}
		return
	}
	r, ok := argRune(k)
	if k == "<Tab>" {
		r, ok = '\t', true
	}
	if !ok || r == '\r' {
		return
	}
	in.aiLine = -1
	s := string(r)
	end := off
	if e.mode == Replace {
		if _, n := v.char(e.cur); n > 0 {
			end += n
		}
		in.replaced = append(in.replaced, string(v.s.Slice(text.Range{Start: off, End: end})))
	}
	e.do(e.group, text.Replace{Range: text.Range{Start: off, End: end}, Text: []byte(s)})
	e.cur.Col += len(s)
}

// dropAutoindent removes indent that autoindent added to a line nothing was typed on.
func (e *Engine) dropAutoindent(v *view) {
	in := e.ins
	if in.aiLine < 0 || in.aiLine != e.cur.Line || !v.lineWhite(e.cur.Line) {
		return
	}
	start := v.s.LineStart(e.cur.Line)
	e.do(e.group, text.Replace{Range: text.Range{Start: start, End: start + v.lineLen(e.cur.Line)}})
	e.cur.Col = 0
	in.aiLine = -1
}

func (e *Engine) finishInsert() {
	in := e.ins
	if in.block != nil {
		e.finishBlock(in.block)
		if !in.block.change {
			e.marks['^'] = e.cur
			e.cur = in.block.left
			e.mode = Normal
			e.ins = nil
			e.clamp(e.view())
			e.setWant(e.view())
			if e.rec != nil {
				e.endChange()
			}
			e.group = 0
			return
		}
	}
	for n := in.count - 1; n > 0; n-- {
		if in.kind == "o" || in.kind == "O" {
			e.openLine(e.view(), true)
			e.ins.aiLine = e.aiPending
			e.aiPending = -1
		}
		for _, k := range in.keys {
			e.typeKey(k)
		}
	}
	v := e.view()
	e.dropAutoindent(v)
	v = e.view()
	e.inserted = insertedText(in.keys)
	e.marks['^'] = e.cur
	if e.cur != in.start {
		e.marks['['] = in.start
		e.marks[']'] = e.cur
	}
	if e.cur != in.start || len(in.keys) > 0 {
		end := e.cur
		if end.Col > 0 {
			end.Col = v.prevCol(end.Line, end.Col)
		}
		e.marks['.'] = end
	}
	e.mode = Normal
	e.ins = nil
	if e.cur.Col > 0 {
		e.cur.Col = v.prevCol(e.cur.Line, e.cur.Col)
	}
	e.clamp(v)
	e.setWant(v)
	if e.rec != nil {
		e.endChange()
	}
	e.group = 0
}

// insertedText is the text keys typed, as the ". register holds it.
func insertedText(keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		switch k {
		case "<CR>", "<NL>":
			b.WriteByte('\n')
		case "<Tab>":
			b.WriteByte('\t')
		case "<BS>":
			s := b.String()
			if s != "" {
				_, n := utf8.DecodeLastRuneInString(s)
				b.Reset()
				b.WriteString(s[:len(s)-n])
			}
		default:
			if r, ok := argRune(k); ok && r != '\r' {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// typeLiteral is the key after CTRL-V: inserted as the character it is, a control key as its control character.
func (e *Engine) typeLiteral(k string) {
	raw := keysToRaw([]string{k})
	if r, _ := utf8.DecodeRuneInString(raw); r < 0x80 && len(raw) > 1 {
		return
	}
	v := e.view()
	off := v.offset(e.cur)
	end := off
	if e.mode == Replace {
		if _, n := v.char(e.cur); n > 0 {
			end += n
		}
	}
	e.do(e.group, text.Replace{Range: text.Range{Start: off, End: end}, Text: []byte(raw)})
	e.cur.Col += len(raw)
}
