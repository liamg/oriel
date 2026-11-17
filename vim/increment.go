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

// nrFormats is 'nrformats': the kinds of number CTRL-A and CTRL-X read.
type nrFormats struct{ hex, oct, bin, alpha, unsigned, blank bool }

func (e *Engine) nrFormats() nrFormats {
	nf := strings.Split(e.opts.NrFormats, ",")
	return nrFormats{hex: slices.Contains(nf, "hex"), oct: slices.Contains(nf, "octal"), bin: slices.Contains(nf, "bin"),
		alpha: slices.Contains(nf, "alpha"), unsigned: slices.Contains(nf, "unsigned"), blank: slices.Contains(nf, "blank")}
}

// numberAt is where addSub looks: from col on the line, or in Visual mode only in [col, end).
type numberAt struct {
	line, col, end int
	visual         bool
}

// byteAt reads l at i, with 0 off either end.
func byteAt(l string, i int) byte {
	if i >= 0 && i < len(l) {
		return l[i]
	}
	return 0
}

// numberStart is where the number at or after col begins in normal mode, its hex or bin prefix included.
func (nf nrFormats) numberStart(l string, col int) int {
	at := func(i int) byte { return byteAt(l, i) }
	// hexAt and binAt: c is the x or b of a 0x or 0b prefix.
	hexAt := func(c int) bool { return c > 0 && (at(c) == 'X' || at(c) == 'x') && at(c-1) == '0' && isHex(at(c+1)) }
	binAt := func(c int) bool { return c > 0 && (at(c) == 'B' || at(c) == 'b') && at(c-1) == '0' && isBin(at(c+1)) }
	c := col
	if nf.bin {
		for c > 0 && isBin(at(c)) {
			c--
		}
	}
	if nf.hex {
		for c > 0 && isHex(at(c)) {
			c--
		}
	}
	if nf.bin && nf.hex && !hexAt(c) {
		c = col
		for c > 0 && isDec(at(c)) {
			c--
		}
	}
	if nf.hex && hexAt(c) || nf.bin && binAt(c) {
		return c - 1
	}
	c = col
	for c < len(l) && !nf.startsNumber(at(c)) {
		c++
	}
	for c > 0 && isDec(at(c-1)) && (!nf.alpha || !isAlpha(at(c))) {
		c--
	}
	return c
}

// startsNumber says c can begin what CTRL-A changes: a digit, or a letter with alpha.
func (nf nrFormats) startsNumber(c byte) bool { return isDec(c) || nf.alpha && isAlpha(c) }

// stepLetter is CTRL-A or CTRL-X on a letter: it moves through the alphabet, stopping at either end.
func stepLetter(first byte, amount uint64, sub bool) byte {
	base := byte('a')
	if first < 'a' {
		base = 'A'
	}
	if sub {
		if uint64(first-base) < amount {
			return base
		}
		return first - byte(amount)
	}
	if 25-uint64(first-base) < amount {
		return base + 25
	}
	return first + byte(amount)
}

// stepNumber adds amount to n, signed by negative, or subtracts it, as vim does: a decimal crosses zero by
// flipping its sign, a prefixed one wraps, and an unsigned one stops at either end.
func stepNumber(n, amount uint64, pre byte, negative, subtract, overflow, unsigned bool) (uint64, bool) {
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
	if unsigned && negative {
		if subtract {
			n = 0
		} else {
			n = ^uint64(0)
		}
		negative = false
	}
	return n, negative
}

// formatNumber writes n back with its sign and prefix, in its base and letter case, padded with zeros to width
// when pad is set.
func formatNumber(n uint64, pre byte, sign, upper, pad bool, width int) string {
	var b strings.Builder
	if sign {
		b.WriteByte('-')
	}
	if pre != 0 {
		b.WriteByte('0')
		width--
	}
	if pre == 'b' || pre == 'B' || pre == 'x' || pre == 'X' {
		b.WriteByte(pre)
		width--
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
	width -= len(digits)
	if pad {
		for ; width > 0; width-- {
			b.WriteByte('0')
		}
	}
	b.WriteString(digits)
	return b.String()
}

// lastLetterUpper says the last letter in s is upper case: how a hex number's digits are written back.
func lastLetterUpper(s string) bool {
	upper := false
	for i := 0; i < len(s); i++ {
		if c := s[i]; isAlpha(c) {
			upper = c < 'a'
		}
	}
	return upper
}

// addSub is vim's do_addsub() for CTRL-A (add) and CTRL-X (sub) on one line, following 'nrformats'. It returns
// where the changed text ends, or false when there was nothing to change.
func (e *Engine) addSub(v *view, where numberAt, amount uint64, sub bool) (int, bool) {
	nf := e.nrFormats()
	line, col, visual := where.line, where.col, where.visual
	l := string(v.line(line))
	at := func(i int) byte { return byteAt(l, i) }
	if len(l) == 0 || col >= len(l) {
		return 0, false
	}
	start, length := col, where.end-col
	negative, wasPositive := false, true
	if visual {
		for start < len(l) && length > 0 && !nf.startsNumber(at(start)) {
			start++
			length--
		}
		if length <= 0 {
			return 0, false
		}
		if start > col && at(start-1) == '-' && !nf.unsigned {
			negative, wasPositive = true, false
		}
	} else {
		start = nf.numberStart(l, col)
	}
	first := at(start)
	if !nf.startsNumber(first) {
		return 0, false
	}
	ls := v.s.LineStart(line)
	if nf.alpha && isAlpha(first) {
		c := stepLetter(first, amount, sub)
		e.do(e.group, text.Replace{Range: text.Range{Start: ls + start, End: ls + start + 1}, Text: []byte{c}})
		return start, true
	}
	blankUnsigned := false
	if start > 0 && at(start-1) == '-' && !visual && !nf.unsigned {
		if nf.blank && start >= 2 && at(start-2) != ' ' && at(start-2) != '\t' {
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
	pre, n, length, overflow := readNumber(l[start:], nf.hex, nf.bin, nf.oct, maxlen)
	if pre != 0 && negative {
		start++
		length--
		negative = false
	}
	subtract := sub != negative
	n, negative = stepNumber(n, amount, pre, negative, subtract, overflow, nf.unsigned || blankUnsigned)
	if visual && !wasPositive && !negative && start > 0 {
		start--
		length++
	}
	todel := length
	orig := l[start : start+todel]
	if at(start) == '-' {
		length--
	}
	repl := formatNumber(n, pre, negative && (!visual || wasPositive), lastLetterUpper(orig),
		first == '0' && (!nf.oct || pre != 0), length)
	e.cur = Pos{line, start}
	e.do(e.group, text.Replace{Range: text.Range{Start: ls + start, End: ls + start + todel}, Text: []byte(repl)})
	return start + max(len(repl)-1, 0), true
}

// increment is CTRL-A and CTRL-X in normal mode.
func (e *Engine) increment(v *view, c cmd, sub bool) {
	endCol, ok := e.addSub(v, numberAt{line: e.cur.Line, col: e.cur.Col, end: -1}, uint64(c.count1()), sub)
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
		if _, ok := e.addSub(e.view(), numberAt{line: l, col: from, end: to, visual: true}, amount, sub); ok {
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
