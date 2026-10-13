package vim

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

// command runs a normal-mode command that isn't a motion or an operator. Several are shorthands that
// run as their operator and motion would.
func (e *Engine) command(v *view, c cmd) {
	short := func(op, key string) {
		c2 := c
		c2.op, c2.key = op, key
		e.operator(v, c2)
	}
	if e.scrollCmd(v, c.key, c.count) {
		return
	}
	if strings.HasPrefix(c.key, "z") && e.foldCmd(v, c.key, c.count) {
		e.rec = nil
		return
	}
	if k, ok := strings.CutPrefix(c.key, "<C-w>"); ok {
		e.windowCmd(k, c.count)
		return
	}
	switch c.key {
	case "x", "<Del>":
		if v.lineLen(e.cur.Line) == 0 {
			e.rec = nil
			return
		}
		short("d", "l")
	case "X":
		if e.cur.Col == 0 {
			e.rec = nil
			return
		}
		short("d", "h")
	case "D":
		short("d", "$")
	case "C":
		short("c", "$")
	case "Y":
		c2 := c
		c2.op, c2.line = "y", true
		e.operator(v, c2)
	case "s":
		short("c", "l")
	case "S":
		c2 := c
		c2.op, c2.line = "c", true
		e.operator(v, c2)
	case "r":
		e.replaceChars(v, c)
	case "J", "gJ":
		e.join(v, max(c.count, 2), c.key == "J")
	case "p", "P", "gp", "gP":
		e.put(v, c)
	case "<C-^>", "<C-6>":
		e.alternate(c.count)
	case "<C-g>":
		e.fileInfo()
	case "<C-a>", "<C-x>":
		e.increment(v, c, c.key == "<C-x>")
	case "]p", "[p", "]P", "[P":
		e.putIndented(v, c)
	case "~":
		e.tilde(v, c)
	case "i", "a", "I", "A", "o", "O", "gi", "gI", "R":
		e.insertCmd(v, c)
	case "u":
		for range c.count1() {
			e.undo(false)
		}
	case "<C-r>":
		for range c.count1() {
			e.undo(true)
		}
	case "U":
		e.undoLine()
	case "v", "V", "<C-v>":
		e.startVisual(map[string]rune{"v": 'v', "V": 'V', "<C-v>": 0x16}[c.key], c.count)
	case "gv":
		e.reselect()
	case ":":
		pre := ""
		if c.count > 0 {
			pre = "."
			if c.count > 1 {
				pre = ".,.+" + strconv.Itoa(c.count-1)
			}
		}
		e.startCmdline(':', c, pre)
	case "q":
		e.startRecording(c.arg)
	case "@":
		e.playRegister(c.arg, c.count1())
	case "ZZ":
		e.ex(v, "x")
	case "ZQ":
		e.ex(v, "q!")
	case "Q":
		if e.lastRecorded == 0 {
			e.failed = true
			return
		}
		e.playRegister(e.lastRecorded, c.count1())
	case "&":
		e.ex(v, "s")
	case "g&":
		e.ex(v, "%s//~/&")
	case "g-", "g+":
		e.undoTime(c.key == "g+", c.count1())
	case ".":
		e.repeat(c)
	case "m":
		e.setMark(c.arg)
	case "<C-o>":
		e.jump(v, -c.count1())
	case "<C-i>", "<Tab>":
		e.jump(v, c.count1())
	}
	switch c.key {
	case "u", "<C-r>", "U", "g-", "g+", "<C-o>", "<C-i>", "<Tab>":
		if e.feeding == 0 {
			e.openCursorFold()
		}
	}
}

func (e *Engine) replaceChars(v *view, c cmd) {
	n := c.count1()
	p := e.cur
	end := p
	for range n {
		_, size := v.char(end)
		if size == 0 {
			e.rec = nil
			return
		}
		end.Col += size
	}
	from, to := v.offset(p), v.offset(end)
	if c.arg == '\r' {
		// r<CR> replaces the characters with one line break, autoindented.
		ind := string(v.line(p.Line)[:v.indentEnd(p.Line)])
		rest := v.line(p.Line)[end.Col:]
		trim := 0
		for trim < len(rest) && isWhite(rune(rest[trim])) {
			trim++
		}
		e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to + trim}, Text: []byte("\n" + ind)})
		e.cur = Pos{p.Line + 1, len(ind)}
		if len(ind) > 0 {
			e.cur.Col = len(ind) - 1
		}
		return
	}
	body := strings.Repeat(string(c.arg), n)
	e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}, Text: []byte(body)})
	_, last := utf8.DecodeLastRuneInString(body)
	e.cur = e.view().pos(from + len(body) - last)
	e.setWant(e.view())
}

// join joins count lines starting at the cursor: J removes leading white space and puts one space
// between the lines, gJ joins them as they are. The cursor goes where the last line was joined.
func (e *Engine) join(v *view, count int, spaces bool) {
	if e.cur.Line+1 >= v.lines() {
		e.rec = nil
		return
	}
	last := min(e.cur.Line+count-1, v.lines()-1)
	result := string(v.line(e.cur.Line))
	col := 0
	for l := e.cur.Line + 1; l <= last; l++ {
		next := string(v.line(l))
		sp := ""
		if spaces {
			next = strings.TrimLeft(next, " \t")
			if next != "" && next[0] != ')' && result != "" {
				if c := result[len(result)-1]; c != ' ' && c != '\t' {
					sp = " "
				}
			}
		}
		col = len(result)
		result += sp + next
	}
	from := v.s.LineStart(e.cur.Line)
	to := v.s.LineStart(last) + v.lineLen(last)
	e.do(e.group, text.Replace{Range: text.Range{Start: from, End: to}, Text: []byte(result)})
	nv := e.view()
	e.cur.Col = col
	e.clamp(nv)
	e.setWant(nv)
	e.marks['['] = Pos{e.cur.Line, 0}
	e.marks[']'] = Pos{e.cur.Line, col}
}

func (e *Engine) tilde(v *view, c cmd) {
	l := v.line(e.cur.Line)
	if len(l) == 0 {
		e.rec = nil
		return
	}
	from := e.cur.Col
	to := from
	for range c.count1() {
		if to >= len(l) {
			break
		}
		_, n := utf8.DecodeRune(l[to:])
		to += n
	}
	old := string(l[from:to])
	nw := strings.Map(caseFunc("g~"), old)
	start := v.s.LineStart(e.cur.Line)
	if nw != old {
		e.do(e.group, text.Replace{Range: text.Range{Start: start + from, End: start + to}, Text: []byte(nw)})
	}
	e.cur.Col = from + len(nw)
	e.clamp(e.view())
	e.setWant(e.view())
}
