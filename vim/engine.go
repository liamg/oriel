package vim

import (
	"strconv"
	"unicode/utf8"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
)

// Mode is the engine's input mode.
type Mode int

const (
	Normal Mode = iota
	Insert
	Replace
)

const maxCol = 1 << 30 // curswant after $: the end of every line

// Engine is vim's modal editing over an edit.Editor. Keys come in as vim key notation tokens,
// one per key press ("x", "<Esc>", "<C-r>"), as registry/keymap.Parse returns them.
type Engine struct {
	ed   *edit.Editor
	mode Mode
	cur  Pos
	want int // curswant: the screen column vertical moves aim for

	pend    []string // the normal-mode command typed so far
	find    findCmd  // the last f, F, t or T
	unnamed rune     // the register "" reads

	marks      map[rune]Pos
	pcmark     *Pos
	prevPcmark *Pos
	jumps      []Pos
	jumpIdx    int
	undoCur    map[int]Pos          // the cursor before each undo step
	undoMarks  map[int]map[rune]Pos // marks each undo step deleted, which undoing it restores

	dot      *dotCmd // the last change, for "."
	rec      *dotCmd // the change being recorded
	ins      *insertState
	inserted string // the last inserted text, for ". and gi
	msg      string

	aiPending    int // the line o or O just autoindented, or -1
	uLine        int // the line U restores, or -1
	uText        string
	uCol         int
	inU          bool
	visual       *Pos // the start of the Visual area, while in Visual mode
	vmode        rune // 'v', 'V' or Ctrl-V
	lastVisual   *visualArea
	lastVisSize  *visualSize
	search       lastSearch
	lastSub      string // the last :s replacement, for ~
	lastSubFlags string
	cmdline      *cmdline
	opts         Options
	gstack       []*lineMarks // lines :g and :normal still have to visit, innermost last
	inGlobal     bool
	silent       bool
	confirm      *confirmation
	recReg       rune     // the register q is recording into, or 0
	recKeys      []string // keys recorded so far
	lastPlayed   rune
	group        text.UndoGroup // the undo step the current change goes into
	changeCursor Pos            // the cursor when the current command started
}

// dotCmd is a change as "." repeats it: the keys after any count and register, the count, and
// the keys typed in insert mode if it entered it.
type dotCmd struct {
	count int
	reg   rune
	body  []string
	ins   []string
	vis   *visualSize // a Visual operator: repeat on the same amount of text
}

// New returns an engine editing through ed, in normal mode.
func New(ed *edit.Editor) *Engine {
	e := &Engine{ed: ed, marks: map[rune]Pos{}, undoCur: map[int]Pos{}, undoMarks: map[int]map[rune]Pos{}, aiPending: -1, uLine: -1, opts: DefaultOptions()}
	ed.Subscribe(e.adjustMarks)
	e.sync()
	return e
}

// Mode reports the current mode; op-pending while an operator waits for its motion.
func (e *Engine) Mode() string {
	switch e.mode {
	case Insert:
		return "insert"
	case Replace:
		return "replace"
	}
	if e.cmdline != nil {
		return "cmdline"
	}
	if e.visual != nil {
		return map[rune]string{'v': "visual", 'V': "visual-line", 0x16: "visual-block"}[e.vmode]
	}
	if c, st := parse(e.pend, false); e.visual == nil && st == incomplete && c.op != "" {
		return "op-pending"
	}
	return "normal"
}

// Pending is the normal-mode command typed so far, for which-key and showcmd.
func (e *Engine) Pending() []string { return append([]string(nil), e.pend...) }

// Cursor returns the cursor.
func (e *Engine) Cursor() Pos { return e.cur }

// SetCursor moves the cursor, as clicking would.
func (e *Engine) SetCursor(p Pos) {
	e.cur = p
	v := e.view()
	e.clamp(v)
	e.want = v.vcol(e.cur.Line, e.cur.Col)
	e.sync()
}

// LastMessage is the last message shown, as :messages ends with.
func (e *Engine) LastMessage() string { return e.msg }

func (e *Engine) view() *view { return newView(e.ed.Buf()) }

func (e *Engine) echo(msg string) {
	e.msg = msg
	e.ed.Echo(msg)
}

// sync tells the editor where the cursor is.
func (e *Engine) sync() {
	v := e.view()
	e.ed.SetCursors([]edit.Cursor{{Off: v.offset(e.cur)}})
}

// clamp keeps the cursor in the buffer and, in normal mode, on a character rather than past the end.
func (e *Engine) clamp(v *view) {
	e.cur.Line = min(max(e.cur.Line, 0), v.lines()-1)
	n := v.lineLen(e.cur.Line)
	if e.mode == Normal && n > 0 && e.cur.Col >= n {
		e.cur.Col = v.lastCol(e.cur.Line)
	}
	e.cur.Col = min(max(e.cur.Col, 0), n)
	if e.cur.Col < n && !utf8.RuneStart(v.line(e.cur.Line)[e.cur.Col]) {
		e.cur.Col = v.prevCol(e.cur.Line, e.cur.Col+1)
	}
}

func (e *Engine) setWant(v *view) { e.want = v.vcol(e.cur.Line, e.cur.Col) }

// Key handles one key press.
func (e *Engine) Key(k string) {
	if c := e.confirm; c != nil {
		e.confirm = nil
		if k == "y" || k == "Y" {
			c.yes()
			e.clamp(e.view())
		}
		e.sync()
		return
	}
	if e.recReg != 0 {
		e.recKeys = append(e.recKeys, k)
	}
	if e.cmdline != nil {
		e.cmdlineKey(k)
	} else if e.mode != Normal {
		e.insertKey(k)
	} else {
		e.normalKey(k)
	}
	e.sync()
}

func (e *Engine) normalKey(k string) {
	if e.recReg != 0 && len(e.pend) == 0 && k == "q" {
		e.stopRecording()
		return
	}
	e.pend = append(e.pend, k)
	c, st := parse(e.pend, e.visual != nil)
	switch st {
	case incomplete:
		return
	case invalid:
		e.pend = nil
		return
	}
	e.pend = nil
	if e.visual != nil {
		e.execVisual(c)
		return
	}
	e.exec(c)
}

type status int

const (
	complete status = iota
	incomplete
	invalid
)

// cmd is a parsed normal-mode command.
type cmd struct {
	count int    // 0 when none was typed; an operator's and its motion's counts multiplied
	reg   rune   // 0 when none was given
	op    string // an operator: "d", "c", "y", "<", ">", "g~", "gu", "gU", "g?"
	line  bool   // the operator doubled: dd, cc, >>…
	obj   string // a text object: "iw", "a("…
	key   string // a command or motion
	arg   rune   // the character after f, t, r, m, ' and the like
	body  []string

	searchTarget *target // for / and ?: the search the command line ran
}

func (c cmd) count1() int { return max(c.count, 1) }

var operators = map[string]string{"d": "d", "c": "c", "y": "y", "<": "<", ">": ">", "g~": "~", "gu": "u", "gU": "U", "g?": "?"}

// takesArg lists the keys whose next key is a character argument.
var takesArg = map[string]bool{"q": true, "@": true, "f": true, "F": true, "t": true, "T": true, "r": true, "m": true, "'": true, "`": true, "g'": true, "g`": true}

var prefixes = map[string]bool{"g": true, "z": true, "[": true, "]": true, "<C-w>": true}

func isDigit(k string) bool { return len(k) == 1 && k[0] >= '0' && k[0] <= '9' }

// argRune turns a key into the character it types, for f{char} and r{char}.
func argRune(k string) (rune, bool) {
	switch k {
	case "<Tab>":
		return '\t', true
	case "<CR>", "<NL>":
		return '\r', true
	case "<Esc>":
		return 0, false
	}
	r, n := utf8.DecodeRuneInString(k)
	if n != len(k) || r == utf8.RuneError {
		return 0, false
	}
	return r, true
}

func parse(keys []string, visual bool) (c cmd, st status) {
	i := 0
	readCount := func() (int, bool) {
		n, any := 0, false
		for i < len(keys) && isDigit(keys[i]) && (keys[i] != "0" || any) {
			n = n*10 + int(keys[i][0]-'0')
			any = true
			i++
		}
		return n, any
	}
	for {
		if n, ok := readCount(); ok {
			c.count = max(c.count, 1) * n
			continue
		}
		if i < len(keys) && keys[i] == `"` {
			if i+1 >= len(keys) {
				return c, incomplete
			}
			r, ok := argRune(keys[i+1])
			if !ok || !validReg(r) {
				return c, invalid
			}
			c.reg = r
			i += 2
			continue
		}
		break
	}
	key := func() (string, status) {
		if i >= len(keys) {
			return "", incomplete
		}
		k := keys[i]
		i++
		if prefixes[k] {
			if i >= len(keys) {
				return "", incomplete
			}
			k += keys[i]
			i++
		}
		return k, complete
	}
	k, st := key()
	if st != complete {
		return c, st
	}
	if visual {
		_, isOp := operators[k]
		switch {
		case k == "i" || k == "a":
			if i >= len(keys) {
				return c, incomplete
			}
			c.obj = k + keys[i]
			if _, ok := objects[c.obj]; !ok {
				return c, invalid
			}
			return c, complete
		case isOp || visualCmds[k]:
			c.key = k
			c.body = append(c.body, k)
			if takesArg[k] {
				if i >= len(keys) {
					return c, incomplete
				}
				r, ok := argRune(keys[i])
				if !ok {
					return c, invalid
				}
				c.arg = r
				c.body = append(c.body, keys[i])
			}
			return c, complete
		}
	}
	if second, ok := operators[k]; ok {
		c.op = k
		c.body = append(c.body, k)
		n2, got := readCount()
		if got {
			c.count = max(c.count, 1) * n2
		}
		m, st := key()
		if st != complete {
			return c, st
		}
		c.body = append(c.body, m)
		switch {
		case m == second || m == k:
			c.line = true
			return c, complete
		case m == "i" || m == "a":
			if i >= len(keys) {
				return c, incomplete
			}
			c.obj = m + keys[i]
			c.body = append(c.body, keys[i])
			if _, ok := objects[c.obj]; !ok {
				return c, invalid
			}
			return c, complete
		}
		k = m
		if _, ok := motions[k]; !ok {
			return c, invalid
		}
	} else {
		c.body = append(c.body, k)
		if _, ok := motions[k]; !ok && !commands[k] {
			return c, invalid
		}
	}
	c.key = k
	if takesArg[k] {
		if i >= len(keys) {
			return c, incomplete
		}
		r, ok := argRune(keys[i])
		if !ok {
			return c, invalid
		}
		c.arg = r
		c.body = append(c.body, keys[i])
		i++
	}
	return c, complete
}

// commands are the normal-mode keys that aren't motions or operators.
var commands = map[string]bool{
	"x": true, "X": true, "D": true, "C": true, "Y": true, "s": true, "S": true, "r": true, "J": true, "gJ": true,
	"p": true, "P": true, "gp": true, "gP": true, "i": true, "a": true, "I": true, "A": true, "o": true, "O": true,
	"gi": true, "gI": true, "R": true, "~": true, "u": true, "<C-r>": true, "U": true, ".": true, "m": true,
	"<C-o>": true, "<C-i>": true, "<Tab>": true, "<Esc>": true, "g-": true, "g+": true, "<Del>": true,
	"v": true, "V": true, "<C-v>": true, "gv": true, ":": true, "&": true, "g&": true, "q": true, "@": true,
	"]p": true, "[p": true, "]P": true, "[P": true,
}

// changes are the commands "." repeats.
var changes = map[string]bool{
	"]p": true, "[p": true, "]P": true, "[P": true,
	"x": true, "X": true, "D": true, "C": true, "s": true, "S": true, "r": true, "J": true, "gJ": true,
	"p": true, "P": true, "gp": true, "gP": true, "i": true, "a": true, "I": true, "A": true, "o": true, "O": true,
	"gi": true, "gI": true, "R": true, "~": true, "<Del>": true,
}

func (e *Engine) exec(c cmd) {
	v := e.view()
	e.changeCursor = e.cur
	if (c.key == "/" || c.key == "?") && c.searchTarget == nil {
		e.startCmdline(c.key[0], c, "")
		return
	}
	switch {
	case c.op != "":
		e.startChange(c)
		e.operator(v, c)
	case motions[c.key] != nil:
		e.move(v, c)
	default:
		if changes[c.key] {
			e.startChange(c)
		}
		e.command(v, c)
	}
	if e.mode == Normal {
		e.endChange()
		e.clamp(e.view())
	}
}

// startChange begins recording a change for ".".
func (e *Engine) startChange(c cmd) {
	if c.op == "y" {
		e.rec = nil
		return
	}
	e.rec = &dotCmd{count: c.count, reg: c.reg, body: c.body}
	e.group = e.ed.NewGroup()
}

func (e *Engine) endChange() {
	if e.rec != nil {
		e.dot = e.rec
		e.rec = nil
	}
}

// repeat runs the last change again, with count replacing its count when one is given.
func (e *Engine) repeat(c cmd) {
	d := e.dot
	if d == nil {
		return
	}
	n := d.count
	if c.count > 0 && d.vis == nil {
		n = c.count
	}
	if d.vis != nil {
		e.restoreArea(e.view(), d.vis)
	}
	reg := d.reg
	if reg >= '1' && reg < '9' {
		reg++
	}
	var keys []string
	if reg != 0 {
		keys = append(keys, `"`, string(reg))
	}
	if n > 0 {
		for _, r := range strconv.Itoa(n) {
			keys = append(keys, string(r))
		}
	}
	keys = append(append(keys, d.body...), d.ins...)
	for _, k := range keys {
		e.Key(k)
	}
}

// do applies replaces as part of the current change's undo step.
func (e *Engine) do(group text.UndoGroup, rs ...text.Replace) text.Change {
	before := e.ed.UndoSeq()
	ch, err := e.ed.Do(rs, group)
	if err != nil {
		panic(err)
	}
	if seq := e.ed.UndoSeq(); seq != before {
		if _, ok := e.undoCur[seq]; !ok {
			e.undoCur[seq] = e.cur
		}
	}
	e.saveULine(ch)
	if n := len(ch.Replaces); n > 0 {
		e.marks['.'] = newView(ch.After).pos(min(ch.Replaces[n-1].Range.Start, ch.After.Len()))
	}
	return ch
}
