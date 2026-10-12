package syntax

// A matcher for Lua patterns, which nvim-treesitter's #lua-match? predicates use. It follows lstrlib.c's
// match(): classes %a %c %d %g %l %p %s %u %w %x and their capitals, sets [...], anchors, the quantifiers
// * + - ?, %b and %f. Captures aren't needed, so they only group.

type luaPattern struct {
	p      string
	anchor bool
}

func compileLuaPattern(p string) *luaPattern {
	if len(p) > 0 && p[0] == '^' {
		return &luaPattern{p: p[1:], anchor: true}
	}
	return &luaPattern{p: p}
}

// find reports whether the pattern matches anywhere in s (or at its start, when anchored).
func (lp *luaPattern) find(s string) bool {
	for i := 0; i <= len(s); i++ {
		if _, ok := lmatch(s, i, lp.p, 0, 0); ok {
			return true
		}
		if lp.anchor {
			break
		}
	}
	return false
}

func lclass(c byte, cl byte) bool {
	var res bool
	lower := cl | 0x20
	switch lower {
	case 'a':
		res = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	case 'c':
		res = c < 32 || c == 127
	case 'd':
		res = c >= '0' && c <= '9'
	case 'g':
		res = c > 32 && c < 127
	case 'l':
		res = c >= 'a' && c <= 'z'
	case 'p':
		res = c > 32 && c < 127 && !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9')
	case 's':
		res = c == ' ' || c >= '\t' && c <= '\r'
	case 'u':
		res = c >= 'A' && c <= 'Z'
	case 'w':
		res = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	case 'x':
		res = c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
	default:
		return cl == c
	}
	if cl >= 'A' && cl <= 'Z' {
		return !res
	}
	return res
}

// classEnd returns the index after the single-character class at p[i].
func classEnd(p string, i int) int {
	c := p[i]
	i++
	switch c {
	case '%':
		return i + 1
	case '[':
		if i < len(p) && p[i] == '^' {
			i++
		}
		for {
			if i >= len(p) {
				return i
			}
			cc := p[i]
			i++
			if cc == '%' && i < len(p) {
				i++
			}
			if i >= len(p) || p[i] == ']' {
				return i + 1
			}
		}
	}
	return i
}

// matchSet is lstrlib's matchbracketclass: p[start] is '[' and p[end-1] its ']'.
func matchSet(c byte, p string, start, end int) bool {
	ec := end - 1
	sig := true
	i := start
	if i+1 < len(p) && p[i+1] == '^' {
		sig = false
		i++
	}
	for i++; i < ec; i++ {
		switch {
		case p[i] == '%':
			i++
			if i < len(p) && lclass(c, p[i]) {
				return sig
			}
		case i+1 < len(p) && p[i+1] == '-' && i+2 < ec:
			i += 2
			if p[i-2] <= c && c <= p[i] {
				return sig
			}
		case p[i] == c:
			return sig
		}
	}
	return !sig
}

func singleMatch(s string, si int, p string, pi, ep int) bool {
	if si >= len(s) {
		return false
	}
	c := s[si]
	switch p[pi] {
	case '.':
		return true
	case '%':
		return lclass(c, p[pi+1])
	case '[':
		return matchSet(c, p, pi, ep)
	}
	return p[pi] == c
}

// lmatch matches p[pi:] at s[si:], returning where the match ends.
func lmatch(s string, si int, p string, pi int, depth int) (int, bool) {
	if depth > 200 {
		return 0, false
	}
	for pi < len(p) {
		switch p[pi] {
		case '(', ')':
			pi++
			continue
		case '$':
			if pi+1 == len(p) {
				return si, si == len(s)
			}
		case '%':
			if pi+1 < len(p) {
				switch p[pi+1] {
				case 'b':
					if pi+3 >= len(p) || si >= len(s) || s[si] != p[pi+2] {
						return 0, false
					}
					open, close := p[pi+2], p[pi+3]
					cont := 1
					j := si + 1
					for ; j < len(s); j++ {
						if s[j] == close {
							cont--
							if cont == 0 {
								break
							}
						} else if s[j] == open {
							cont++
						}
					}
					if j >= len(s) {
						return 0, false
					}
					si = j + 1
					pi += 4
					continue
				case 'f':
					pi += 2
					if pi >= len(p) || p[pi] != '[' {
						return 0, false
					}
					ep := classEnd(p, pi)
					var prev, cur byte
					if si > 0 {
						prev = s[si-1]
					}
					if si < len(s) {
						cur = s[si]
					}
					if matchSet(prev, p, pi, ep) || !matchSet(cur, p, pi, ep) {
						return 0, false
					}
					pi = ep
					continue
				}
			}
		}
		ep := classEnd(p, pi)
		var q byte
		if ep < len(p) {
			q = p[ep]
		}
		switch q {
		case '?':
			if singleMatch(s, si, p, pi, ep) {
				if r, ok := lmatch(s, si+1, p, ep+1, depth+1); ok {
					return r, true
				}
			}
			pi = ep + 1
			continue
		case '+', '*':
			n := 0
			for singleMatch(s, si+n, p, pi, ep) {
				n++
			}
			min := 0
			if q == '+' {
				min = 1
			}
			for ; n >= min; n-- {
				if r, ok := lmatch(s, si+n, p, ep+1, depth+1); ok {
					return r, true
				}
			}
			return 0, false
		case '-':
			for {
				if r, ok := lmatch(s, si, p, ep+1, depth+1); ok {
					return r, true
				}
				if !singleMatch(s, si, p, pi, ep) {
					return 0, false
				}
				si++
			}
		}
		if !singleMatch(s, si, p, pi, ep) {
			return 0, false
		}
		si++
		pi = ep
	}
	return si, true
}
