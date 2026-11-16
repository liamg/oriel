package vim

import (
	"strconv"
	"strings"

	"github.com/liamg/oriel/regex/vimregex"
)

// exRange is the lines an ex command works on, 0-based and inclusive.
type exRange struct {
	from, to int
	given    int // how many addresses were typed
}

// parseRange reads the addresses at the start of a command line.
func (e *Engine) parseRange(v *view, s string) (exRange, string, error) {
	cur := e.cur.Line
	r := exRange{from: cur, to: cur}
	if strings.HasPrefix(s, "%") {
		r = exRange{from: 0, to: v.lines() - 1, given: 2}
		return r, s[1:], nil
	}
	if strings.HasPrefix(s, "*") {
		a, b := e.marks['<'], e.marks['>']
		return exRange{from: a.Line, to: b.Line, given: 2}, s[1:], nil
	}
	var addrs []int
	for {
		s = strings.TrimLeft(s, " \t")
		n, rest, ok, err := e.parseAddress(v, s, cur)
		if err != nil {
			return r, s, err
		}
		if !ok {
			if len(addrs) > 0 && (strings.HasPrefix(s, ",") || strings.HasPrefix(s, ";")) {
				addrs = append(addrs, cur)
			}
			break
		}
		addrs = append(addrs, n)
		s = rest
		if strings.HasPrefix(s, ",") {
			s = s[1:]
			continue
		}
		if strings.HasPrefix(s, ";") {
			s = s[1:]
			cur = n
			if n >= 0 {
				e.cur.Line = min(n, v.lines()-1)
			}
			continue
		}
		break
	}
	switch len(addrs) {
	case 0:
	case 1:
		r = exRange{from: addrs[0], to: addrs[0], given: 1}
	default:
		r = exRange{from: addrs[len(addrs)-2], to: addrs[len(addrs)-1], given: 2}
	}
	return r, s, nil
}

// parseAddress reads one address and its offsets.
func (e *Engine) parseAddress(v *view, s string, cur int) (int, string, bool, error) {
	n := cur
	got := false
	switch {
	case s == "":
		return 0, s, false, nil
	case s[0] >= '0' && s[0] <= '9':
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		x, _ := strconv.Atoi(s[:i])
		n, s, got = x-1, s[i:], true
	case s[0] == '.':
		n, s, got = cur, s[1:], true
	case s[0] == '$':
		n, s, got = v.lines()-1, s[1:], true
	case s[0] == '\'' && len(s) > 1:
		p, ok := e.markPos(rune(s[1]))
		if !ok {
			return 0, s, false, errMarkNotSet.with()
		}
		n, s, got = p.Line, s[2:], true
	case s[0] == '/' || s[0] == '?':
		l, rest, err := e.patternAddress(v, s, cur)
		if err != nil {
			return 0, s, false, err
		}
		n, s, got = l, rest, true
		for len(s) > 0 && (s[0] == '/' || s[0] == '?') {
			l, rest, err := e.patternAddress(v, s, n)
			if err != nil {
				return 0, s, false, err
			}
			n, s = l, rest
		}
	case s[0] == '\\' && len(s) > 1 && (s[1] == '/' || s[1] == '?' || s[1] == '&'):
		if !e.search.set {
			return 0, s, false, errNoPrevPattern.with()
		}
		re, err := e.compile(e.search.pat)
		if err != nil {
			return 0, s, false, err
		}
		l, ok := e.searchLine(v, re, cur, s[1] != '?')
		if !ok {
			return 0, s, false, errPatternNotFound.with(e.search.pat)
		}
		n, s, got = l, s[2:], true
	}
	for len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		sign := 1
		if s[0] == '-' {
			sign = -1
		}
		i := 1
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		d := 1
		if i > 1 {
			d, _ = strconv.Atoi(s[1:i])
		}
		n += sign * d
		s = s[i:]
		got = true
	}
	return n, s, got, nil
}

// patternAddress is a /pat/ or ?pat? address: the next (or previous) line from line that matches.
func (e *Engine) patternAddress(v *view, s string, line int) (int, string, error) {
	pat, rest, _ := parseSearch(s[1:], s[0])
	if pat == "" {
		pat = e.search.pat
	}
	re, err := e.compile(pat)
	if err != nil {
		return 0, s, err
	}
	e.search = lastSearch{pat: pat, fwd: s[0] == '/', set: true, subPat: e.search.subPat}
	if line < 0 {
		// After "0;" the search starts before the first line, so the first line can match.
		if _, ok := re.Exec(input{v}, 0, 0, e.regexState(v)); ok && s[0] == '/' {
			return 0, rest, nil
		}
		line = 0
	}
	l, ok := e.searchLine(v, re, line, s[0] == '/')
	if !ok {
		return 0, s, errPatternNotFound.with(pat)
	}
	return l, rest, nil
}

// searchLine finds the first line after (or before) line that has a match, wrapping around.
func (e *Engine) searchLine(v *view, re *vimregex.Regex, line int, fwd bool) (int, bool) {
	n := v.lines()
	st := e.regexState(v)
	for i := 1; i <= n; i++ {
		l := line + i
		if !fwd {
			l = line - i
		}
		if l < 0 || l >= n {
			if !e.opts.WrapScan {
				return 0, false
			}
			l = (l%n + n) % n
		}
		if _, ok := re.Exec(input{v}, l, 0, st); ok {
			return l, true
		}
	}
	return 0, false
}
