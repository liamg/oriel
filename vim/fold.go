package vim

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/text"
)

// A fold's state, as vim's fd_flags: open, closed, or following 'foldlevel'. A fold following 'foldlevel'
// makes every fold inside it follow it too.
type foldFlag uint8

const (
	foldLevel foldFlag = iota
	foldOpen
	foldClosed
)

// fold is a range of lines the window can show as one, with the folds nested in it.
type fold struct {
	top, end int // lines, end included
	nested   []*fold
	flags    foldFlag
}

func (f *fold) clone() *fold {
	c := *f
	c.nested = cloneFolds(f.nested)
	return &c
}

func cloneFolds(fs []*fold) []*fold {
	out := make([]*fold, len(fs))
	for i, f := range fs {
		out[i] = f.clone()
	}
	return out
}

// foldState is a window's folds and fold options.
type foldState struct {
	folds  []*fold
	method string // 'foldmethod': manual, indent or marker
	level  int    // 'foldlevel'
	enable bool   // 'foldenable'
	manual bool   // folds were opened or closed by hand since 'foldlevel' was last applied
}

const foldStart, foldEnd = "{{{", "}}}"

// findFold finds the fold in fs holding line, or the index where one would go.
func findFold(fs []*fold, line int) (int, bool) {
	i, _ := slices.BinarySearchFunc(fs, line, func(f *fold, l int) int {
		switch {
		case f.end < l:
			return -1
		case f.top > l:
			return 1
		}
		return 0
	})
	return i, i < len(fs) && fs[i].top <= line && line <= fs[i].end
}

// checkClosed is vim's check_closed(): whether f, at nesting level, is closed. useLevel carries down that an
// enclosing fold follows 'foldlevel'. A fold of one line is never closed ('foldminlines').
func (s *foldState) checkClosed(f *fold, useLevel *bool, level int) bool {
	closed := false
	if *useLevel || f.flags == foldLevel {
		*useLevel = true
		closed = level >= s.level
	} else {
		closed = f.flags == foldClosed
	}
	return closed && f.end > f.top
}

func (s *foldState) any() bool { return s.enable && (s.method != "manual" || len(s.folds) > 0) }

// closedAt is vim's hasFolding(): the lines of the closed fold holding line, if there is one.
func (s *foldState) closedAt(line int) (int, int, bool) {
	if !s.any() {
		return line, line, false
	}
	fs, level, useLevel := s.folds, 0, false
	for {
		i, ok := findFold(fs, line)
		if !ok {
			return line, line, false
		}
		f := fs[i]
		if s.checkClosed(f, &useLevel, level) {
			return f.top, f.end, true
		}
		fs = f.nested
		level++
	}
}

func (e *Engine) foldStart(line int) int { s, _, _ := e.window.fold.closedAt(line); return s }
func (e *Engine) foldEndOf(line int) int { _, l, _ := e.window.fold.closedAt(line); return l }

// deepest is how deep folds nest.
func deepest(fs []*fold) int {
	d := 0
	for _, f := range fs {
		d = max(d, 1+deepest(f.nested))
	}
	return d
}

// setManual is vim's setManualFoldWin(): open or close the fold at line. It reports whether a fold was
// found, and whether one changed state.
func (s *foldState) setManual(line int, opening, recurse bool) (found, acted bool) {
	fs, level, useLevel := s.folds, 0, false
	var deepestOpen *fold
	for {
		i, ok := findFold(fs, line)
		if !ok {
			break
		}
		f := fs[i]
		found = true
		if useLevel || f.flags == foldLevel {
			useLevel = true
			if level >= s.level {
				f.flags = foldClosed
			} else {
				f.flags = foldOpen
			}
			for _, n := range f.nested {
				n.flags = foldLevel
			}
		}
		if !opening && recurse {
			if f.flags != foldClosed {
				f.flags = foldClosed
			}
		} else if f.flags == foldClosed {
			if opening {
				f.flags = foldOpen
				acted = true
				if recurse {
					openNested(f)
				}
			}
			break
		}
		deepestOpen = f
		fs = f.nested
		level++
	}
	if found {
		if !opening && deepestOpen != nil {
			deepestOpen.flags = foldClosed
			acted = true
		}
		s.manual = true
	}
	return found, acted
}

func openNested(f *fold) {
	for _, n := range f.nested {
		n.flags = foldOpen
		openNested(n)
	}
}

// repeatFold opens or closes count levels of folds at line.
func (e *Engine) repeatFold(line, count int, opening bool) {
	s := &e.window.fold
	for i := range count {
		found, acted := s.setManual(line, opening, false)
		if !found && i == 0 {
			e.notify(errNoFold.with())
			return
		}
		if !acted {
			return
		}
	}
}

func (e *Engine) recurseFold(line int, opening bool) {
	if found, _ := e.window.fold.setManual(line, opening, true); !found {
		e.notify(errNoFold.with())
	}
}

// openCursorFold is vim's foldOpenCursor(): open folds until the cursor line shows.
func (e *Engine) openCursorFold() {
	s := &e.window.fold
	if !s.any() {
		return
	}
	for {
		if _, acted := s.setManual(e.cur.Line, true, false); !acted {
			return
		}
	}
}

// applyLevel is vim's newFoldLevel(): forget folds opened and closed by hand, so 'foldlevel' decides again.
func (s *foldState) applyLevel() {
	if s.manual {
		for _, f := range s.folds {
			f.flags = foldLevel
		}
		s.manual = false
	}
}

// createFold is vim's foldCreate() for lines [start, end]: the new fold is closed, and folds inside it
// nest in it.
func (e *Engine) createFold(start, end int) {
	s := &e.window.fold
	if s.method == "marker" {
		e.addMarkers(start, end)
		return
	}
	fs := &s.folds
	level, useLevel, closed := 0, false, false
	for {
		i, ok := findFold(*fs, start)
		if !ok || (*fs)[i].end < end {
			break
		}
		f := (*fs)[i]
		if s.checkClosed(f, &useLevel, level) {
			closed = true
		}
		fs = &f.nested
		level++
	}
	nf := &fold{top: start, end: end, flags: foldClosed}
	i, _ := findFold(*fs, start)
	j := i
	for j < len(*fs) && (*fs)[j].top <= end {
		j++
	}
	// Folds that the new one overlaps go inside it.
	nf.nested = slices.Clone((*fs)[i:j])
	*fs = slices.Replace(*fs, i, j, nf)
	if useLevel && !closed && level < s.level {
		s.setManual(start, false, false)
	}
	if !useLevel {
		s.manual = true
	}
	nf.flags = foldClosed
	s.enable = true
}

// deleteFolds is vim's deleteFold() over lines [start, end]: the deepest fold at each line, stopping at a
// closed one, goes; its nested folds move up a level unless recursive.
func (e *Engine) deleteFolds(start, end int, recursive bool) {
	s := &e.window.fold
	did := false
	for line := start; line <= end; {
		var holder *[]*fold
		var at int
		fs, level, useLevel := &s.folds, 0, false
		for {
			i, ok := findFold(*fs, line)
			if !ok {
				break
			}
			holder, at = fs, i
			f := (*fs)[i]
			if s.checkClosed(f, &useLevel, level) {
				break
			}
			fs = &f.nested
			level++
		}
		if holder == nil {
			line++
			continue
		}
		f := (*holder)[at]
		line = f.end + 1
		did = true
		if s.method == "marker" {
			e.deleteMarkers(f, recursive)
			continue
		}
		var up []*fold
		if !recursive {
			for _, n := range f.nested {
				if f.flags == foldLevel {
					n.flags = foldLevel
				}
				up = append(up, n)
			}
		}
		*holder = slices.Replace(*holder, at, at+1, up...)
	}
	if !did {
		e.notify(errNoFold.with())
	}
}

// moveFold is vim's foldMoveTo(): zj and zk (updown) move to the start of the next fold or the end of the
// previous one; [z and ]z to the start or end of the open fold holding the cursor.
func (e *Engine) moveFold(updown, forward bool, count int) (int, bool) {
	s := &e.window.fold
	cur := e.cur.Line
	moved := false
	for range count {
		fs := s.folds
		if len(fs) == 0 || !s.enable {
			break
		}
		useLevel, level, found := false, 0, cur
		for {
			i, ok := findFold(fs, cur)
			last := false
			if !ok {
				if !updown {
					break
				}
				if forward {
					if i >= len(fs) {
						break
					}
					i--
				} else if i == 0 {
					break
				}
				last = true
			}
			if !last && i >= 0 {
				if s.checkClosed(fs[i], &useLevel, level) {
					last = true
				}
				if last && !updown {
					break
				}
			}
			if updown {
				if forward {
					if i+1 < len(fs) && fs[i+1].top > cur {
						found = fs[i+1].top
					}
				} else if !ok {
					if i > 0 && fs[i-1].end < cur {
						found = fs[i-1].end
					}
				} else if i > 0 && fs[i-1].end < cur {
					found = fs[i-1].end
				}
			} else {
				if forward {
					if fs[i].end > cur {
						found = fs[i].end
					}
				} else if fs[i].top < cur {
					found = fs[i].top
				}
			}
			if last {
				break
			}
			fs = fs[i].nested
			level++
		}
		if found == cur {
			break
		}
		cur = found
		moved = true
	}
	return cur, moved
}

// foldCmd runs the z commands that work on folds. It reports whether k was one.
func (e *Engine) foldCmd(v *view, k string, count int) bool {
	s := &e.window.fold
	n := max(count, 1)
	line := e.cur.Line
	switch k {
	case "zF":
		if !e.foldManualAllowed(true) {
			return true
		}
		e.createFold(line, min(line+n-1, v.lines()-1))
	case "zd", "zD":
		if !e.foldManualAllowed(false) {
			return true
		}
		e.deleteFolds(line, line, k == "zD")
	case "zE":
		if m := s.method; m != "manual" && m != "marker" {
			e.notify(errFoldErase.with())
			return true
		}
		if s.method == "marker" {
			e.deleteFolds(0, v.lines()-1, true)
		} else {
			s.folds = nil
		}
	case "zo":
		e.repeatFold(line, n, true)
	case "zO":
		e.recurseFold(line, true)
	case "zc":
		e.repeatFold(line, n, false)
		s.enable = true
	case "zC":
		e.recurseFold(line, false)
		s.enable = true
	case "za":
		if _, _, closed := s.closedAt(line); closed {
			e.repeatFold(line, n, true)
		} else {
			e.repeatFold(line, n, false)
			s.enable = true
		}
	case "zA":
		if _, _, closed := s.closedAt(line); closed {
			e.recurseFold(line, true)
		} else {
			e.recurseFold(line, false)
			s.enable = true
		}
	case "zv":
		e.openCursorFold()
	case "zx":
		s.enable = true
		e.updateFolds(e.window)
		s.manual = true
		s.applyLevel()
		e.openCursorFold()
	case "zX":
		s.enable = true
		e.updateFolds(e.window)
		s.manual = true
		s.applyLevel()
	case "zm":
		s.level = max(s.level-n, 0)
		s.applyLevel()
		s.enable = true
	case "zM":
		s.level = 0
		s.applyLevel()
		s.enable = true
	case "zr":
		s.level = min(s.level+n, deepest(s.folds))
		s.applyLevel()
	case "zR":
		s.level = deepest(s.folds)
		s.applyLevel()
	case "zn":
		s.enable = false
	case "zN":
		s.enable = true
	case "zi":
		s.enable = !s.enable
	default:
		return false
	}
	return true
}

// foldManualAllowed is whether folds can be made or deleted by hand: with 'foldmethod' manual or marker.
func (e *Engine) foldManualAllowed(create bool) bool {
	switch e.window.fold.method {
	case "manual", "marker":
		return true
	}
	if create {
		e.notify(errFoldCreate.with())
	} else {
		e.notify(errFoldDelete.with())
	}
	return false
}

// exFold runs :fold, :foldopen and :foldclose over a range.
func (e *Engine) exFold(name string, r exRange, bang bool) error {
	switch name {
	case "fold":
		if !e.foldManualAllowed(true) {
			return nil
		}
		e.createFold(r.from, r.to)
	case "foldopen", "foldclose":
		e.foldRange(r.from, r.to, name == "foldopen", bang)
	}
	return nil
}

// foldRange is vim's opFoldRange(): open or close folds on lines [first, last].
func (e *Engine) foldRange(first, last int, opening, recurse bool) {
	s := &e.window.fold
	any := false
	for l := first; l <= last; {
		next := l
		if opening && !recurse {
			_, next, _ = s.closedAt(l)
		}
		if found, _ := s.setManual(l, opening, recurse); found {
			any = true
		}
		if !opening && !recurse {
			_, next, _ = s.closedAt(l)
		}
		l = next + 1
	}
	if !any {
		e.notify(errNoFold.with())
	}
	if !opening {
		s.enable = true
	}
}

// adjustFolds moves a window's folds through lines [first, first+removed) going and added lines coming at first.
func (s *foldState) adjustFolds(first, removed, added int) {
	if removed > 0 {
		s.folds = deleteFoldLines(s.folds, first, first+removed-1)
	}
	if added > 0 {
		insertFoldLines(s.folds, first, added)
	}
}

func deleteFoldLines(fs []*fold, l1, l2 int) []*fold {
	n := l2 - l1 + 1
	out := fs[:0]
	for _, f := range fs {
		switch {
		case f.end < l1:
		case f.top > l2:
			shiftFold(f, -n)
		case f.top >= l1 && f.end <= l2:
			continue
		default:
			f.nested = deleteFoldLines(f.nested, l1, l2)
			if f.top >= l1 {
				f.top = l1
				f.end -= n
			} else if f.end <= l2 {
				f.end = l1 - 1
			} else {
				f.end -= n
			}
		}
		out = append(out, f)
	}
	return out
}

func insertFoldLines(fs []*fold, at, n int) {
	for _, f := range fs {
		switch {
		case f.top >= at:
			shiftFold(f, n)
		case f.end >= at:
			f.end += n
			insertFoldLines(f.nested, at, n)
		}
	}
}

func shiftFold(f *fold, n int) {
	f.top += n
	f.end += n
	for _, c := range f.nested {
		shiftFold(c, n)
	}
}

// updateFolds recomputes a window's folds from the text for 'foldmethod' indent and marker, keeping the state
// of folds that start where they did.
func (e *Engine) updateFolds(w *window) {
	s := &w.fold
	if s.method == "manual" {
		return
	}
	v := newView(w.buf.text.Snapshot())
	var levels, starts []int
	if s.method == "indent" {
		levels, starts = e.indentLevels(v)
	} else {
		levels, starts = markerLevels(v)
	}
	fresh := buildFolds(levels, starts, 1, 0, len(levels)-1)
	keepFlags(fresh, s.folds)
	s.folds = fresh
}

func keepFlags(fresh, old []*fold) {
	for _, f := range fresh {
		for _, o := range old {
			if o.top == f.top {
				f.flags = o.flags
				keepFlags(f.nested, o.nested)
				break
			}
		}
	}
}

// buildFolds makes the folds of depth d and deeper over lines [from, to] from each line's fold level and the
// lowest level a fold starts at on it.
func buildFolds(levels, starts []int, d, from, to int) []*fold {
	var out []*fold
	for l := from; l <= to; {
		if levels[l] < d {
			l++
			continue
		}
		top := l
		l++
		for l <= to && levels[l] >= d && starts[l] > d {
			l++
		}
		f := &fold{top: top, end: l - 1}
		f.nested = buildFolds(levels, starts, d+1, top, l-1)
		out = append(out, f)
	}
	return out
}

// indentLevels is each line's fold level for 'foldmethod' indent: its indent in shiftwidths, with blank lines
// taking the lower level of the lines around them.
func (e *Engine) indentLevels(v *view) ([]int, []int) {
	n := v.lines()
	sw := e.opts.shiftWidth()
	levels := make([]int, n)
	starts := make([]int, n)
	for l := range n {
		starts[l] = 1 << 30
		if v.lineWhite(l) {
			levels[l] = -1
			continue
		}
		levels[l] = v.vcol(l, v.indentEnd(l)) / sw
	}
	for l := range n {
		if levels[l] >= 0 {
			continue
		}
		prev, next := -1, -1
		for p := l - 1; p >= 0; p-- {
			if levels[p] >= 0 {
				prev = levels[p]
				break
			}
		}
		for q := l + 1; q < n; q++ {
			if levels[q] >= 0 {
				next = levels[q]
				break
			}
		}
		levels[l] = max(min(prev, next), 0)
	}
	return levels, starts
}

var markerRe = regexp.MustCompile(`(\{\{\{|\}\}\})(\d*)`)

// markerLevels is each line's fold level for 'foldmethod' marker, and the lowest level a fold starts at on it.
func markerLevels(v *view) ([]int, []int) {
	n := v.lines()
	levels := make([]int, n)
	starts := make([]int, n)
	cur := 0
	for l := range n {
		starts[l] = 1 << 30
		lvl, after := cur, cur
		for _, m := range markerRe.FindAllStringSubmatch(string(v.line(l)), -1) {
			num, hasNum := 0, m[2] != ""
			if hasNum {
				num, _ = strconv.Atoi(m[2])
			}
			if m[1] == foldStart {
				if hasNum && num == 0 {
					continue
				}
				if !hasNum {
					num = after + 1
				}
				lvl, after = num, num
				starts[l] = min(starts[l], num)
			} else {
				if hasNum && num == 0 {
					continue
				}
				if !hasNum {
					num = after
				}
				after = min(after, num-1)
			}
		}
		levels[l] = max(lvl, 0)
		cur = max(after, 0)
	}
	return levels, starts
}

// addMarkers is zf with 'foldmethod' marker: a start marker on the first line and an end marker on the last.
func (e *Engine) addMarkers(start, end int) {
	v := e.view()
	g := e.newGroup()
	e.do(g, text.Replace{Range: text.Range{Start: v.s.LineStart(end) + v.lineLen(end), End: v.s.LineStart(end) + v.lineLen(end)}, Text: []byte(foldEnd)})
	v = e.view()
	e.do(g, text.Replace{Range: text.Range{Start: v.s.LineStart(start) + v.lineLen(start), End: v.s.LineStart(start) + v.lineLen(start)}, Text: []byte(foldStart)})
}

// deleteMarkers is zd with 'foldmethod' marker: the fold's markers go from its first and last lines.
func (e *Engine) deleteMarkers(f *fold, recursive bool) {
	if recursive {
		for _, n := range f.nested {
			e.deleteMarkers(n, true)
		}
	}
	g := e.newGroup()
	for _, item := range []struct {
		line   int
		marker string
	}{{f.end, foldEnd}, {f.top, foldStart}} {
		v := e.view()
		l := string(v.line(item.line))
		i := strings.Index(l, item.marker)
		if i < 0 {
			continue
		}
		j := i + len(item.marker)
		for j < len(l) && l[j] >= '0' && l[j] <= '9' {
			j++
		}
		ls := v.s.LineStart(item.line)
		e.do(g, text.Replace{Range: text.Range{Start: ls + i, End: ls + j}})
	}
}

func init() {
	for _, k := range []string{"zj", "zk", "[z", "]z"} {
		motions[k] = foldMotion
	}
}

// foldMotion is zj, zk, [z and ]z.
func foldMotion(e *Engine, v *view, m motionCtx) (target, bool) {
	line, ok := e.moveFold(m.key[0] == 'z', m.key == "zj" || m.key == "]z", m.count1())
	if !ok {
		return target{}, false
	}
	return target{pos: Pos{line, 0}, jump: true}, true
}
