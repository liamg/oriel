package vim

import (
	"strconv"
	"strings"
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
	// The current window and its buffer; their fields read as the engine's own.
	*window
	*buffer
	windows []*window
	buffers []*buffer
	layout  Layout
	prevWin *window
	nextBuf int
	nextWin int

	mode Mode

	pend    []string // the normal-mode command typed so far
	find    findCmd  // the last f, F, t or T
	unnamed rune     // the register "" reads

	dot      *dotCmd // the last change, for "."
	rec      *dotCmd // the change being recorded
	ins      *insertState
	inserted string // the last inserted text, for ". and gi
	notice   edit.Notice

	aiPending    int // the line o or O just autoindented, or -1
	inU          bool
	visual       *Pos // the start of the Visual area, while in Visual mode
	vmode        rune // 'v', 'V' or Ctrl-V
	lastVisSize  *visualSize
	search       lastSearch
	lastSub      string // the last :s replacement, for ~
	lastSubFlags string
	cmdline      *cmdline
	opts         Options
	settings     Settings
	gstack       []*lineMarks // lines :g and :normal still have to visit, innermost last
	inGlobal     bool
	silent       bool
	confirm      *confirmation
	recReg       rune     // the register q is recording into, or 0
	recKeys      []string // keys recorded so far
	lastPlayed   rune
	lastRecorded rune
	vmodeAtOp    rune // the Visual mode and curswant a Visual CTRL-A started in
	wantAtOp     int
	failed       bool // a command failed: a playing register stops, as vim flushes typeahead on a beep
	playing      int
	feeding      int            // keys come from a register or :normal, not the user
	outerGroup   text.UndoGroup // while set, every change joins it: a register's or :normal's changes undo as one
	lastCmdline  string
	qf           qfStack
	exLine       string // the Ex command running, as typed
	maps         map[byte]map[string]mapping
	mapBuf       []string
	noremap      int
	mapDepth     int
	abbrs        map[byte]map[string]abbrev
	noAbbr       int
	lastExReg    rune
	host         Host
	group        text.UndoGroup // the undo step the current change goes into
	changeCursor Pos            // the cursor when the current command started
	multi        *multi         // the cursors, while there are several (multi.go)
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
	e := &Engine{aiPending: -1, opts: DefaultOptions(), host: Disk{}}
	b := e.newBuffer(ed.Buffer())
	w := e.newWindow(ed, b)
	e.layout = newFrames(WindowID(w.id))
	e.window, e.buffer = w, b
	e.sync()
	return e
}

// buffer is what the engine keeps per buffer: its text, marks and undo cursors.
type buffer struct {
	text       *text.Buffer
	num        int
	name       string
	marks      map[rune]Pos
	undoCur    map[int]Pos          // the cursor before each undo step
	undoMarks  map[int]map[rune]Pos // marks each undo step deleted, which undoing it restores
	uLine      int                  // the line U restores, or -1
	uText      string
	uCol       int
	lastVisual *visualArea
	last       Pos  // where the cursor was when the buffer was last left, the '" mark
	unlisted   bool // deleted with :bdelete
	modified   bool
	savedSeq   int        // the undo step the file holds
	noLines    bool       // no lines at all, as vim's ML_EMPTY, rather than one empty line: written as nothing
	file       *edit.File // the file it was read from or written to; nil until then
	notEdited  bool       // named with :file rather than read from its file
	qf         *qfStack   // the list a quickfix or location-list window's buffer shows
	unloaded   bool       // its text was dropped (:bdelete, :bunload); its jumps are forgotten
}

// window is what the engine keeps per window: its editor (cursor) on a buffer, and its jumplist.
type window struct {
	ed         *edit.Editor
	buf        *buffer
	id         int
	cur        Pos
	want       int // curswant: the screen column vertical moves aim for
	pcmark     *Pos
	prevPcmark *Pos
	jumps      []jump
	jumpIdx    int
	alt        *buffer // the alternate buffer, for CTRL-^
	fold       foldState
	loc        *qfStack // the location list
	locOwner   *window  // for a location-list window, the window whose list it shows
	top        int      // the first line shown
	left       int      // the first screen column shown, with 'nowrap'
	width      int      // screen columns for text; 0 for the window's share of the default screen
	height     int      // rows shown; 0 for the window's share of the default screen
	scroll     int      // 'scroll', set by a count to CTRL-D or CTRL-U; 0 for half the window
	scrolled   *Pos     // the cursor as the wheel left it: while it's still there, the window leads
}

func (e *Engine) newBuffer(t *text.Buffer) *buffer {
	e.nextBuf++
	b := &buffer{text: t, num: e.nextBuf, marks: map[rune]Pos{}, undoCur: map[int]Pos{}, undoMarks: map[int]map[rune]Pos{}, uLine: -1,
		noLines: t.Snapshot().Len() == 0}
	e.buffers = append(e.buffers, b)
	t.Subscribe(func(ch text.Change) {
		b.changed()
		e.adjustMarks(b, ch)
	})
	return b
}

func (e *Engine) newWindow(ed *edit.Editor, b *buffer) *window {
	e.nextWin++
	w := &window{ed: ed, buf: b, id: e.nextWin, fold: foldState{method: "manual", enable: true}}
	e.windows = append(e.windows, w)
	return w
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

// LastNotice is the last notice the engine gave.
func (e *Engine) LastNotice() edit.Notice { return e.notice }

func (e *Engine) view() *view {
	v := newView(e.ed.Buf())
	v.folds = &e.window.fold
	v.ts = e.opts.TabStop
	return v
}

func (e *Engine) notify(n edit.Notice) {
	e.notice = n
	e.ed.Notify(n)
}

// fail shows an error as a notice; nil shows nothing.
func (e *Engine) fail(err error) {
	if err != nil {
		e.notify(asNotice(err))
	}
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
	if e.multi != nil && e.feeding == 0 && e.multiKey(k) {
		return
	}
	if e.recReg != 0 && e.feeding == 0 && e.confirm == nil {
		e.recKeys = append(e.recKeys, k)
	}
	e.loadOptions()
	e.updateTop(e.view())
	e.input(k)
	if e.mode == Normal || e.mode == Insert || e.mode == Replace {
		e.updateTop(e.view())
	}
	e.sync()
}

// dispatch runs a key after mappings.
func (e *Engine) dispatch(k string) {
	if c := e.confirm; c != nil {
		e.confirm = nil
		if k == "y" || k == "Y" {
			c.yes()
			e.clamp(e.view())
		}
		e.sync()
		return
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
	if k == "<CR>" && len(e.pend) == 0 && e.window.buf.qf != nil && e.visual == nil {
		e.qfEnter()
		return
	}
	if k == "q" && onlyCount(e.pend) {
		if e.recReg != 0 {
			e.pend = nil
			e.stopRecording()
			return
		}
		if e.playing > 0 {
			e.pend = nil
			return
		}
	}
	e.pend = append(e.pend, k)
	if n := len(e.pend); n >= 2 && e.pend[n-2] == "z" && k == "f" && onlyCount(e.pend[:n-2]) && !e.foldManualAllowed(true) {
		e.pend = nil
		return
	}
	c, st := parse(e.pend, e.visual != nil)
	switch st {
	case incomplete:
		return
	case invalid:
		e.pend = nil
		e.failed = true
		return
	}
	e.pend = nil
	if c.reg == 0 && e.ed.HasClipboard() && e.opts.Clipboard == "system" && (c.op != "" || registerCmds[c.key] || e.visual != nil && visualRegisterCmds[c.key]) {
		c.reg = '+' // 'clipboard' unnamedplus: the system clipboard is the default register
	}
	if e.visual != nil {
		e.execVisual(c)
		return
	}
	e.exec(c)
}

// registerCmds are the normal-mode commands that read or write the default register.
var registerCmds = map[string]bool{"x": true, "X": true, "D": true, "C": true, "s": true, "S": true, "Y": true,
	"<Del>": true, "p": true, "P": true, "gp": true, "gP": true, "]p": true, "[p": true, "]P": true, "[P": true}

// visualRegisterCmds are the Visual-mode commands that do.
var visualRegisterCmds = map[string]bool{"d": true, "x": true, "X": true, "D": true, "y": true, "Y": true, "c": true,
	"C": true, "s": true, "S": true, "R": true, "p": true, "P": true, "<Del>": true}

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

var operators = map[string]string{"zf": "zf", "d": "d", "c": "c", "y": "y", "<": "<", ">": ">", "g~": "~", "gu": "u", "gU": "U", "g?": "?"}

// takesArg lists the keys whose next key is a character argument.
var takesArg = map[string]bool{"q": true, "@": true, "f": true, "F": true, "t": true, "T": true, "r": true, "m": true, "'": true, "`": true, "g'": true, "g`": true}

var prefixes = map[string]bool{"g": true, "z": true, "[": true, "]": true, "<C-w>": true, "Z": true}

func onlyCount(keys []string) bool {
	for _, k := range keys {
		if !isDigit(k) {
			return false
		}
	}
	return true
}

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

// maxCount is the largest count, as vim caps it.
const maxCount = 999999999

// mulCount is a count typed after another: their product, capped at maxCount.
func mulCount(a, b int) int {
	a = max(a, 1)
	if b > 0 && a >= maxCount/b {
		return maxCount
	}
	return a * b
}

func parse(keys []string, visual bool) (c cmd, st status) {
	i := 0
	readCount := func() (int, bool) {
		n, any := 0, false
		for i < len(keys) && isDigit(keys[i]) && (keys[i] != "0" || any) {
			if n >= maxCount/10 {
				n = maxCount
			} else {
				n = n*10 + int(keys[i][0]-'0')
			}
			any = true
			i++
		}
		return n, any
	}
	for {
		if n, ok := readCount(); ok {
			c.count = mulCount(c.count, n)
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
	if visual && strings.HasPrefix(k, "<C-w>") && commands[k] {
		c.key = k
		return c, complete
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
			c.count = mulCount(c.count, n2)
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
	"zF": true, "zd": true, "zD": true, "zE": true, "zo": true, "zO": true, "zc": true, "zC": true, "za": true,
	"zA": true, "zv": true, "zx": true, "zX": true, "zm": true, "zM": true, "zr": true, "zR": true, "zn": true,
	"zN": true, "zi": true,
	"<C-e>": true, "<C-y>": true, "<C-d>": true, "<C-u>": true, "<C-f>": true, "<C-b>": true, "<PageDown>": true,
	"<PageUp>": true, "<S-Down>": true, "<S-Up>": true, "zl": true, "z<Right>": true, "zh": true, "z<Left>": true, "zL": true,
	"zH": true, "zs": true, "ze": true, "zt": true, "z<CR>": true, "zz": true, "z.": true, "zb": true, "z-": true,
	"ZZ": true, "ZQ": true, "Q": true, "<C-g>": true, "<C-a>": true, "<C-x>": true, "<C-^>": true, "<C-6>": true,
}

func init() {
	for _, k := range []string{"s", "S", "<C-s>", "v", "<C-v>", "n", "<C-n>", "w", "<C-w>", "W", "p", "<C-p>",
		"t", "<C-t>", "b", "<C-b>", "h", "j", "k", "l", "<C-h>", "<C-j>", "<C-k>", "<C-l>", "<Left>", "<Down>",
		"<Up>", "<Right>", "^", "<C-^>", "<C-6>", "c", "<C-c>", "q", "<C-q>", "o", "<C-o>", "x", "<C-x>", "r", "<C-r>", "R"} {
		commands["<C-w>"+k] = true
	}
}

// changes are the commands "." repeats.
var changes = map[string]bool{
	"]p": true, "[p": true, "]P": true, "[P": true, "<C-a>": true, "<C-x>": true,
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
func (e *Engine) newGroup() text.UndoGroup {
	if e.outerGroup != 0 {
		return e.outerGroup
	}
	return e.ed.NewGroup()
}

func (e *Engine) startChange(c cmd) {
	if c.op == "y" {
		e.rec = nil
		return
	}
	e.rec = &dotCmd{count: c.count, reg: c.reg, body: c.body}
	e.group = e.newGroup()
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
	e.feeding++
	defer func() { e.feeding-- }()
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
