package vim

import (
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/text"
)

func isHex(c byte) bool   { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func isBin(c byte) bool   { return c == '0' || c == '1' }
func isDec(c byte) bool   { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// readNumber is vim's vim_str2nr(): the number at the start of s with its prefix ('x', 'b', '0' for octal,
// or 0), how many bytes it takes, and whether it overflowed 64 bits (the value is then the largest there is).
// maxlen, when above zero, limits how far it reads.
func readNumber(s string, hex, bin, oct bool, maxlen int) (pre byte, n uint64, length int, overflow bool) {
	if maxlen > 0 && maxlen < len(s) {
		s = s[:maxlen]
	}
	at := func(i int) byte {
		if i < len(s) {
			return s[i]
		}
		return 0
	}
	i := 0
	if at(0) == '-' {
		i++
	}
	if at(i) == '0' && at(i+1) != '8' && at(i+1) != '9' {
		p := at(i + 1)
		switch {
		case hex && (p == 'x' || p == 'X') && isHex(at(i+2)):
			pre = p
			i += 2
		case bin && (p == 'b' || p == 'B') && isBin(at(i+2)):
			pre = p
			i += 2
		case oct:
			for j := i + 1; isDec(at(j)); j++ {
				if at(j) > '7' {
					pre = 0
					break
				}
				pre = '0'
			}
		}
	}
	const max = ^uint64(0)
	switch pre {
	case 'x', 'X':
		for ; isHex(at(i)); i++ {
			d, _ := strconv.ParseUint(string(at(i)), 16, 8)
			if n <= (max-d)/16 {
				n = n*16 + d
			} else {
				n, overflow = max, true
			}
		}
	case 'b', 'B':
		for ; isBin(at(i)); i++ {
			d := uint64(at(i) - '0')
			if n <= (max-d)/2 {
				n = n*2 + d
			} else {
				n, overflow = max, true
			}
		}
	case '0':
		for ; at(i) >= '0' && at(i) <= '7'; i++ {
			d := uint64(at(i) - '0')
			if n <= (max-d)/8 {
				n = n*8 + d
			} else {
				n, overflow = max, true
			}
		}
	default:
		for ; isDec(at(i)); i++ {
			d := uint64(at(i) - '0')
			if n <= (max-d)/10 {
				n = n*10 + d
			} else {
				n, overflow = max, true
			}
		}
	}
	return pre, n, i, overflow
}

// addSub is vim's do_addsub() for CTRL-A (add) and CTRL-X on one line, following 'nrformats'. From normal
// mode it finds the number at or after col; in Visual mode (visual set) it only looks in [col, end). It
// returns where the changed text ends, or false when there was nothing to change.
func (e *Engine) addSub(v *view, line, col, end int, amount uint64, sub, visual bool) (int, bool) {
	nf := strings.Split(e.opts.NrFormats, ",")
	dohex, dooct, dobin := slices.Contains(nf, "hex"), slices.Contains(nf, "octal"), slices.Contains(nf, "bin")
	doalp, unsigned, blank := slices.Contains(nf, "alpha"), slices.Contains(nf, "unsigned"), slices.Contains(nf, "blank")
	l := string(v.line(line))
	at := func(i int) byte {
		if i >= 0 && i < len(l) {
			return l[i]
		}
		return 0
	}
	if len(l) == 0 || col >= len(l) {
		return 0, false
	}
	start := col
	length := end - col
	if !visual {
		c := col
		if dobin {
			for c > 0 && isBin(at(c)) {
				c--
			}
		}
		if dohex {
			for c > 0 && isHex(at(c)) {
				c--
			}
		}
		if dobin && dohex && !(c > 0 && (at(c) == 'X' || at(c) == 'x') && at(c-1) == '0' && isHex(at(c+1))) {
			c = col
			for c > 0 && isDec(at(c)) {
				c--
			}
		}
		if dohex && c > 0 && (at(c) == 'X' || at(c) == 'x') && at(c-1) == '0' && isHex(at(c+1)) ||
			dobin && c > 0 && (at(c) == 'B' || at(c) == 'b') && at(c-1) == '0' && isBin(at(c+1)) {
			c--
		} else {
			c = col
			for c < len(l) && !isDec(at(c)) && !(doalp && isAlpha(at(c))) {
				c++
			}
			for c > 0 && isDec(at(c-1)) && !(doalp && isAlpha(at(c))) {
				c--
			}
		}
		start = c
	}
	negative, wasPositive := false, true
	if visual {
		for start < len(l) && length > 0 && !isDec(at(start)) && !(doalp && isAlpha(at(start))) {
			start++
			length--
		}
		if length <= 0 {
			return 0, false
		}
		if start > col && at(start-1) == '-' && !unsigned {
			negative, wasPositive = true, false
		}
	}
	first := at(start)
	if !isDec(first) && !(doalp && isAlpha(first)) {
		return 0, false
	}
	ls := v.s.LineStart(line)
	if doalp && isAlpha(first) {
		base := byte('a')
		if first < 'a' {
			base = 'A'
		}
		c := first
		if sub {
			if uint64(first-base) < amount {
				c = base
			} else {
				c -= byte(amount)
			}
		} else {
			if 25-uint64(first-base) < amount {
				c = base + 25
			} else {
				c += byte(amount)
			}
		}
		e.do(e.group, text.Replace{Range: text.Range{Start: ls + start, End: ls + start + 1}, Text: []byte{c}})
		return start, true
	}
	blankUnsigned := false
	if start > 0 && at(start-1) == '-' && !visual && !unsigned {
		if blank && start >= 2 && at(start-2) != ' ' && at(start-2) != '\t' {
			blankUnsigned = true
		} else {
			start--
			negative = true
		}
	}
	maxlen := 0
	if visual && e.vmodeAtOp != 'V' {
		maxlen = length
		if e.wantAtOp == maxCol {
			maxlen = len(l) - start
		}
	}
	pre, n, length, overflow := readNumber(l[start:], dohex, dobin, dooct, maxlen)
	if pre != 0 && negative {
		start++
		length--
		negative = false
	}
	subtract := sub != negative
	old := n
	if !overflow {
		if subtract {
			n -= amount
		} else {
			n += amount
		}
	}
	if pre == 0 {
		if subtract {
			if n > old {
				n = 1 + ^n
				negative = !negative
			}
		} else if n < old {
			n = ^n
			negative = !negative
		}
		if n == 0 {
			negative = false
		}
	}
	if (unsigned || blankUnsigned) && negative {
		if subtract {
			n = 0
		} else {
			n = ^uint64(0)
		}
		negative = false
	}
	if visual && !wasPositive && !negative && start > 0 {
		start--
		length++
	}
	todel := length
	orig := l[start : start+todel]
	if at(start) == '-' {
		length--
	}
	upper := false
	for i := 0; i < len(orig); i++ {
		if c := orig[i]; isAlpha(c) {
			upper = c < 'a'
		}
	}
	var b strings.Builder
	if negative && (!visual || wasPositive) {
		b.WriteByte('-')
	}
	if pre != 0 {
		b.WriteByte('0')
		length--
	}
	if pre == 'b' || pre == 'B' || pre == 'x' || pre == 'X' {
		b.WriteByte(pre)
		length--
	}
	var digits string
	switch pre {
	case 'b', 'B':
		digits = strconv.FormatUint(n, 2)
	case 0:
		digits = strconv.FormatUint(n, 10)
	case '0':
		digits = strconv.FormatUint(n, 8)
	default:
		digits = strconv.FormatUint(n, 16)
		if upper {
			digits = strings.ToUpper(digits)
		}
	}
	length -= len(digits)
	if first == '0' && !(dooct && pre == 0) {
		for ; length > 0; length-- {
			b.WriteByte('0')
		}
	}
	b.WriteString(digits)
	repl := b.String()
	e.cur = Pos{line, start}
	e.do(e.group, text.Replace{Range: text.Range{Start: ls + start, End: ls + start + todel}, Text: []byte(repl)})
	return start + max(len(repl)-1, 0), true
}

// increment is CTRL-A and CTRL-X in normal mode.
func (e *Engine) increment(v *view, c cmd, sub bool) {
	endCol, ok := e.addSub(v, e.cur.Line, e.cur.Col, -1, uint64(c.count1()), sub, false)
	if !ok {
		e.rec = nil
		e.failed = true
		return
	}
	e.cur.Col = endCol
	e.setWant(e.view())
}

// visualIncrement is v_CTRL-A and v_CTRL-X: the first number in the area on each line changes by count, or
// with g by count times how many numbers have changed so far, making a sequence.
func (e *Engine) visualIncrement(v *view, c cmd, sub, seq bool) {
	start, end := *e.visual, e.cur
	if less(end, start) {
		start, end = end, start
	}
	mode := e.vmode
	e.vmodeAtOp, e.wantAtOp = mode, e.want
	var left, right int
	if mode == ctrlV {
		left, right = e.blockCols(v)
	}
	e.endVisual()
	n := uint64(0)
	for l := start.Line; l <= end.Line; l++ {
		from, to := 0, v.lineLen(l)
		switch mode {
		case 'v':
			if l == start.Line {
				from = start.Col
			}
			if l == end.Line {
				_, w := v.char(end)
				to = min(end.Col+max(w, 1), to)
			}
		case ctrlV:
			var ok bool
			if from, to, ok = lineSpan(v, l, left, right); !ok {
				continue
			}
		}
		amount := uint64(c.count1())
		if seq {
			amount *= n + 1
		}
		if _, ok := e.addSub(e.view(), l, from, to, amount, sub, true); ok {
			n++
		}
	}
	e.cur = start
	switch mode {
	case 'V':
		e.cur.Col = 0
	case ctrlV:
		e.cur.Col = v.colAt(start.Line, left, false)
	}
	e.clamp(e.view())
	e.setWant(e.view())
}
