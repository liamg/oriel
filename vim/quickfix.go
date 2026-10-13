package vim

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
)

// qfEntry is one quickfix entry: a place in a buffer and its message. A line that didn't parse as an error
// is kept as an entry that isn't valid, as vim keeps it.
type qfEntry struct {
	name  string
	line  int // 1-based
	col   int // 1-based byte column, 0 for none
	text  string
	valid bool
}

type qfList struct {
	entries []qfEntry
	idx     int
	title   string
}

// qfStack is a quickfix list or a window's location list, with the older lists kept for :colder.
type qfStack struct {
	lists []*qfList
	cur   int
	buf   *buffer // the quickfix window's buffer, while it has one
}

func (s *qfStack) list() *qfList {
	if s == nil || len(s.lists) == 0 {
		return nil
	}
	return s.lists[s.cur]
}

func (s *qfStack) clone() *qfStack {
	c := &qfStack{cur: s.cur}
	for _, l := range s.lists {
		cl := *l
		cl.entries = slices.Clone(l.entries)
		c.lists = append(c.lists, &cl)
	}
	return c
}

// push adds a new list, dropping lists newer than the current one, keeping ten, as vim does.
func (s *qfStack) push(l *qfList) {
	if len(s.lists) > 0 {
		s.lists = s.lists[:s.cur+1]
	}
	s.lists = append(s.lists, l)
	if len(s.lists) > 10 {
		s.lists = s.lists[1:]
	}
	s.cur = len(s.lists) - 1
}

// errorFormat is the part of the default 'errorformat' that applies without files: file:line:col:message
// and file:line:message.
var errorFormat = regexp.MustCompile(`^([^:]+):(\d+):(?:(\d+):)?\s*(.*)$`)

func parseEntry(s string) qfEntry {
	m := errorFormat.FindStringSubmatch(s)
	if m == nil {
		return qfEntry{text: s}
	}
	line, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])
	return qfEntry{name: m[1], line: line, col: col, text: m[4], valid: true}
}

// stack is the quickfix list (loc false) or the current window's location list. A location-list window
// uses the list of the window it belongs to.
func (e *Engine) stack(loc bool, create bool) *qfStack {
	if !loc {
		return &e.qf
	}
	w := e.window
	if w.locOwner != nil {
		w = w.locOwner
	}
	if w.loc == nil && create {
		w.loc = &qfStack{}
	}
	return w.loc
}

// exQuickfix runs the quickfix and location-list commands; name has its leading c or l.
func (e *Engine) exQuickfix(name, args string, r exRange, bang bool, line string) error {
	loc := name[0] == 'l'
	cmd := name[1:]
	if name == "vimgrep" || name == "lvimgrep" {
		return e.vimgrep(loc, args, line)
	}
	count := 0
	if r.given > 0 {
		count = r.to + 1
	}
	if n, err := strconv.Atoi(strings.TrimSpace(args)); err == nil {
		count = n
	}
	switch cmd {
	case "expr", "getexpr", "addexpr":
		items, err := evalList(args)
		if err != nil {
			return err
		}
		return e.fillList(loc, cmd, items, line)
	case "buffer", "getbuffer", "addbuffer":
		v := e.view()
		from, to := 0, v.lines()-1
		if r.given > 0 {
			from, to = r.from, r.to
		}
		var items []string
		for l := from; l <= to; l++ {
			items = append(items, string(v.line(l)))
		}
		return e.fillList(loc, strings.TrimSuffix(cmd, "buffer")+"expr", items, line)
	case "open", "window", "close":
		return e.qfWindow(loc, cmd)
	case "older", "newer", "history":
		return e.qfHistory(loc, cmd, count, r.given > 0)
	case "do":
		return e.qfDo(loc, args)
	}
	s := e.stack(loc, false)
	l := s.list()
	if l == nil || len(l.entries) == 0 {
		if loc && s == nil {
			return errNoLocList.with()
		}
		return errListEmpty.with()
	}
	idx := l.idx
	switch cmd {
	case "c", "l":
		if count > 0 {
			idx = min(count, len(l.entries)) - 1
		}
	case "first", "rewind":
		idx = max(min(count, len(l.entries)), 1) - 1
	case "last":
		idx = len(l.entries) - 1
		if count > 0 {
			idx = min(count, len(l.entries)) - 1
		}
	case "next", "previous", "Next":
		n, ok := l.step(idx, max(count, 1), cmd == "next")
		if !ok {
			return errNoMoreItems.with()
		}
		idx = n
	case "nfile", "pfile", "Nfile":
		n, ok := l.stepFile(idx, max(count, 1), cmd == "nfile")
		if !ok {
			return errNoMoreItems.with()
		}
		idx = n
	case "above", "below", "before", "after":
		n, ok := e.nearEntry(l, cmd, max(count, 1))
		if !ok {
			return errNoMoreItems.with()
		}
		idx = n
	default:
		return errUnknownCommand.with(name)
	}
	return e.qfJump(loc, idx)
}

// step moves n valid entries on from idx, stopping at the last one there is.
func (l *qfList) step(idx, n int, fwd bool) (int, bool) {
	d := 1
	if !fwd {
		d = -1
	}
	moved := false
	for i := idx + d; i >= 0 && i < len(l.entries) && n > 0; i += d {
		if l.entries[i].valid {
			idx = i
			n--
			moved = true
		}
	}
	return idx, moved
}

// stepFile moves to the first entry of the next file, or the last entry of the previous one.
func (l *qfList) stepFile(idx, n int, fwd bool) (int, bool) {
	moved := false
	for ; n > 0; n-- {
		name := l.entries[idx].name
		found := -1
		if fwd {
			for i := idx + 1; i < len(l.entries); i++ {
				if l.entries[i].valid && l.entries[i].name != name {
					found = i
					break
				}
			}
		} else {
			for i := idx - 1; i >= 0; i-- {
				if l.entries[i].valid && l.entries[i].name != name {
					found = i
					break
				}
			}
		}
		if found < 0 {
			break
		}
		idx, moved = found, true
	}
	return idx, moved
}

// nearEntry is :cabove, :cbelow, :cbefore and :cafter: the count-th entry in the current buffer above,
// below, before or after the cursor.
func (e *Engine) nearEntry(l *qfList, cmd string, count int) (int, bool) {
	cur := e.cur
	var cands []int
	for i, en := range l.entries {
		if !en.valid || en.name != e.buffer.name {
			continue
		}
		p := Pos{en.line - 1, max(en.col-1, 0)}
		var ok bool
		switch cmd {
		case "above":
			ok = p.Line < cur.Line
		case "below":
			ok = p.Line > cur.Line
		case "before":
			ok = less(p, cur)
		case "after":
			ok = less(cur, p)
		}
		if ok {
			cands = append(cands, i)
		}
	}
	if len(cands) == 0 {
		return 0, false
	}
	if cmd == "above" || cmd == "before" {
		// The nearest first; entries on one line count once going up.
		slices.Reverse(cands)
		if cmd == "above" {
			var lines []int
			seen := map[int]bool{}
			for _, i := range cands {
				if !seen[l.entries[i].line] {
					seen[l.entries[i].line] = true
					lines = append(lines, i)
				}
			}
			cands = lines
		}
	}
	return cands[min(count, len(cands))-1], true
}

// fillList makes a new list from items (expr), or adds them to the current one (addexpr), jumping to the
// first valid entry for expr.
func (e *Engine) fillList(loc bool, kind string, items []string, title string) error {
	s := e.stack(loc, true)
	var entries []qfEntry
	for _, it := range items {
		entries = append(entries, parseEntry(it))
	}
	if kind == "addexpr" && s.list() != nil {
		l := s.list()
		l.entries = append(l.entries, entries...)
		e.refreshQfBuf(s)
		return nil
	}
	l := &qfList{entries: entries, title: ":" + strings.TrimLeft(title, ": ")}
	s.push(l)
	e.refreshQfBuf(s)
	if kind != "expr" || len(entries) == 0 {
		return nil
	}
	first := 0
	for i, en := range entries {
		if en.valid {
			first = i
			break
		}
	}
	return e.qfJump(loc, first)
}

// qfJump makes entry idx current and goes to it, in the window the list belongs to.
func (e *Engine) qfJump(loc bool, idx int) error {
	s := e.stack(loc, false)
	l := s.list()
	l.idx = idx
	en := l.entries[idx]
	e.leaveQfWindow(loc)
	msg := msgQfEntry.with(idx+1, len(l.entries), en.text)
	if en.valid {
		var b *buffer
		for _, x := range e.buffers {
			if x.name == en.name && x.qf == nil {
				b = x
				break
			}
		}
		if b == nil {
			var err error
			if b, err = e.openFile(en.name, false); err != nil {
				return err
			}
		}
		e.setPcmark()
		if b != e.buffer {
			b.unlisted = false
			if err := e.switchBuffer(b, false); err != nil {
				return err
			}
		}
		v := e.view()
		line := min(max(en.line-1, 0), v.lines()-1)
		if en.col > 0 {
			e.cur = Pos{line, min(en.col-1, v.lineLen(line))}
		} else {
			e.cur = Pos{line, v.firstNonBlank(line)}
		}
		e.clamp(v)
		e.setWant(v)
		e.openCursorFold()
	}
	e.refreshQfCursor(s)
	e.notify(msg)
	return nil
}

// leaveQfWindow moves out of a quickfix or location-list window to the window its jumps go to.
func (e *Engine) leaveQfWindow(loc bool) {
	w := e.window
	if w.buf.qf == nil {
		return
	}
	if w.locOwner != nil {
		e.enter(w.locOwner)
		return
	}
	if e.prevWin != nil && e.prevWin.buf.qf == nil && slices.Contains(e.order(), e.prevWin) {
		e.enter(e.prevWin)
		return
	}
	for _, o := range e.order() {
		if o.buf.qf == nil {
			e.enter(o)
			return
		}
	}
}

// qfLines is what the quickfix window shows for a list.
func qfLines(l *qfList) string {
	if l == nil {
		return ""
	}
	var lines []string
	for _, en := range l.entries {
		switch {
		case !en.valid:
			lines = append(lines, "|| "+en.text)
		case en.col > 0:
			lines = append(lines, fmt.Sprintf("%s|%d col %d| %s", en.name, en.line, en.col, en.text))
		default:
			lines = append(lines, fmt.Sprintf("%s|%d| %s", en.name, en.line, en.text))
		}
	}
	return strings.Join(lines, "\n")
}

func (e *Engine) refreshQfBuf(s *qfStack) {
	if s.buf == nil {
		return
	}
	t := s.buf.text
	t.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: 0, End: t.Snapshot().Len()}, Text: []byte(qfLines(s.list()))}}})
	s.buf.modified = false
	e.refreshQfCursor(s)
}

// refreshQfCursor puts the quickfix window's cursor on the current entry.
func (e *Engine) refreshQfCursor(s *qfStack) {
	if s.buf == nil || s.list() == nil {
		return
	}
	for _, w := range e.windows {
		if w.buf == s.buf {
			w.cur = Pos{s.list().idx, 0}
		}
	}
}

// qfWindow is :copen, :cwindow and :cclose, and their location-list forms.
func (e *Engine) qfWindow(loc bool, cmd string) error {
	s := e.stack(loc, false)
	if loc && s == nil {
		if cmd == "close" {
			return nil
		}
		return errNoLocList.with()
	}
	var shown *window
	for _, w := range e.order() {
		if s.buf != nil && w.buf == s.buf {
			shown = w
		}
	}
	hasValid := false
	if l := s.list(); l != nil {
		for _, en := range l.entries {
			hasValid = hasValid || en.valid
		}
	}
	if cmd == "close" || cmd == "window" && !hasValid {
		if shown != nil {
			e.closeWindow(shown)
		}
		return nil
	}
	if shown != nil {
		e.enter(shown)
		return nil
	}
	if s.buf == nil {
		s.buf = e.emptyBuffer()
		s.buf.unlisted = true
		s.buf.qf = s
	}
	owner := e.window
	w := e.newWindow(e.ed.Open(s.buf.text), s.buf)
	if loc {
		w.locOwner = owner
		if owner.locOwner != nil {
			w.locOwner = owner.locOwner
		}
	}
	// The quickfix window goes below every other window, across the whole screen.
	e.layout.Bottom(WindowID(w.id))
	e.refreshQfBuf(s)
	e.enter(w)
	return nil
}

// qfHistory is :colder, :cnewer and :chistory.
func (e *Engine) qfHistory(loc bool, cmd string, count int, given bool) error {
	s := e.stack(loc, false)
	if s == nil || len(s.lists) == 0 {
		if loc {
			return errNoLocList.with()
		}
		if cmd == "history" {
			e.notify(msgNoLists.with())
			return nil
		}
		return errListEmpty.with()
	}
	switch cmd {
	case "older":
		if s.cur == 0 {
			return errOldestList.with()
		}
		s.cur = max(s.cur-max(count, 1), 0)
	case "newer":
		if s.cur == len(s.lists)-1 {
			return errNewestList.with()
		}
		s.cur = min(s.cur+max(count, 1), len(s.lists)-1)
	case "history":
		if given {
			s.cur = min(max(count, 1), len(s.lists)) - 1
			e.refreshQfBuf(s)
			return nil
		}
		for i := range s.lists {
			mark := " "
			if i == s.cur {
				mark = ">"
			}
			e.notify(qfMsg(s, i, mark))
		}
		return nil
	}
	e.refreshQfBuf(s)
	e.notify(qfMsg(s, s.cur, ""))
	return nil
}

// qfMsg is which list this is, its size and its title. In a history, mark is ">" for the current list
// and " " for the others.
func qfMsg(s *qfStack, i int, mark string) edit.Notice {
	return msgQfList.with(mark, i+1, len(s.lists), len(s.lists[i].entries), s.lists[i].title)
}

// qfDo is :cdo and :ldo: go to each valid entry and run cmd there.
func (e *Engine) qfDo(loc bool, cmd string) error {
	s := e.stack(loc, false)
	l := s.list()
	if l == nil {
		return nil
	}
	for i, en := range l.entries {
		if !en.valid {
			continue
		}
		if err := e.qfJump(loc, i); err != nil {
			return err
		}
		if err := e.exec1(e.view(), cmd); err != nil {
			return err
		}
	}
	return nil
}

// vimgrep is :vimgrep /{pattern}/[g][j] {file}, searching buffers already open.
func (e *Engine) vimgrep(loc bool, args, title string) error {
	args = strings.TrimSpace(args)
	var pat, flags, rest string
	if args != "" && !isKeyword(rune(args[0])) {
		d := args[0]
		end := strings.IndexByte(args[1:], d)
		if end < 0 {
			return errGrepDelim.with()
		}
		pat = args[1 : end+1]
		rest = args[end+2:]
		for rest != "" && (rest[0] == 'g' || rest[0] == 'j') {
			flags += rest[:1]
			rest = rest[1:]
		}
	} else {
		pat, rest, _ = strings.Cut(args, " ")
	}
	files := strings.Fields(rest)
	if len(files) == 0 {
		return errGrepArgs.with()
	}
	re, err := e.compile(pat)
	if err != nil {
		return err
	}
	var entries []qfEntry
	for _, f := range files {
		b := e.buffer
		if f != "%" {
			if b, err = e.findBuffer(f, exRange{}); err != nil {
				return err
			}
		}
		v := newView(b.text.Snapshot())
		in := input{v}
		for l := range v.lines() {
			col := 0
			for col <= v.lineLen(l) {
				m, ok := re.Exec(in, l, col, e.regexState(v))
				if !ok || m.Start.Line != l {
					break
				}
				entries = append(entries, qfEntry{name: b.name, line: l + 1, col: m.Start.Col + 1, text: string(v.line(l)), valid: true})
				if !strings.Contains(flags, "g") {
					break
				}
				col = max(m.End.Col, m.Start.Col+1)
				if m.End.Line != l {
					break
				}
			}
		}
	}
	if len(entries) == 0 {
		return errNoMatch.with(pat)
	}
	s := e.stack(loc, true)
	s.push(&qfList{entries: entries, title: ":" + strings.TrimLeft(title, ": ")})
	e.refreshQfBuf(s)
	if strings.Contains(flags, "j") {
		return nil
	}
	return e.qfJump(loc, 0)
}

// qfEnter is <CR> in a quickfix or location-list window: jump to the entry under the cursor.
func (e *Engine) qfEnter() {
	s := e.window.buf.qf
	if s.list() == nil || len(s.list().entries) == 0 {
		return
	}
	loc := e.window.locOwner != nil
	if err := e.qfJump(loc, min(e.cur.Line, len(s.list().entries)-1)); err != nil {
		e.fail(err)
	}
}

// evalList evaluates :cexpr's argument: a string, or a list of strings.
func evalList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		v, err := evalStrings(s)
		if err != nil {
			return nil, err
		}
		return []string{v}, nil
	}
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"))
	var out []string
	for s != "" {
		end := 0
		q := s[0]
		if q != '"' && q != '\'' {
			return nil, errBadExpr.with(s)
		}
		for end = 1; end < len(s); end++ {
			if q == '"' && s[end] == '\\' {
				end++
				continue
			}
			if s[end] == q {
				if q == '\'' && end+1 < len(s) && s[end+1] == '\'' {
					end++
					continue
				}
				break
			}
		}
		v, err := evalStrings(s[:min(end+1, len(s))])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		s = strings.TrimLeft(strings.TrimSpace(s[min(end+1, len(s)):]), ",")
		s = strings.TrimSpace(s)
	}
	return out, nil
}
