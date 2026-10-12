package vimregex

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// hasUpper reports whether a pattern has an uppercase letter for 'smartcase', skipping the letters of
// backslash items (as vim's pat_has_uppercase does, at the magic level the pattern ends with).
func hasUpper(pat string, magic int) bool {
	magic = endMagic(pat, magic)
	for i := 0; i < len(pat); {
		r, l := decodeAt(pat, i)
		switch {
		case l > 1:
			if isUpperVim(r) {
				return true
			}
			i += l
		case pat[i] == '\\' && magic <= magicOn:
			switch {
			case i+2 < len(pat) && (pat[i+1] == '_' || pat[i+1] == '%'):
				i += 3
			case i+1 < len(pat):
				i += 2
			default:
				i++
			}
		case (pat[i] == '%' || pat[i] == '_') && magic == magicAll:
			i += 2
		case r >= 'A' && r <= 'Z':
			return true
		default:
			i++
		}
	}
	return false
}

// endMagic is the magic level in effect at the end of a pattern.
func endMagic(pat string, magic int) int {
	for i := 0; i < len(pat); i++ {
		switch {
		case pat[i] == '[' && magic >= magicOn:
			if end := skipAnyof(pat, i+1); end < len(pat) {
				i = end
			}
		case pat[i] == '\\' && i+1 < len(pat):
			i++
			switch pat[i] {
			case 'v':
				magic = magicAll
			case 'm':
				magic = magicOn
			case 'M':
				magic = magicOff
			case 'V':
				magic = magicNone
			case '[':
				if magic < magicOn {
					if end := skipAnyof(pat, i+1); end < len(pat) {
						i = end
					}
				}
			}
		}
	}
	return magic
}

// Tilde replaces ~ in a :s replacement (\~ with 'nomagic') with the previous replacement string, and
// returns the result, which is the "previous" string for the next :s.
func Tilde(rep, prev string, magic bool) string {
	var b strings.Builder
	for i := 0; i < len(rep); {
		switch {
		case magic && rep[i] == '~':
			b.WriteString(prev)
			i++
		case !magic && rep[i] == '\\' && i+1 < len(rep) && rep[i+1] == '~':
			b.WriteString(prev)
			i += 2
		case rep[i] == '\\' && i+1 < len(rep):
			_, l := decodeAt(rep, i+1)
			b.WriteString(rep[i : i+1+l])
			i += 1 + l
		default:
			_, l := decodeAt(rep, i)
			b.WriteString(rep[i : i+l])
			i += l
		}
	}
	return b.String()
}

// Expand builds a :s replacement per *sub-replace-special*. ~ must already be expanded (Tilde).
// In the result a line break is '\n' and \n gives a NUL byte; a <CR> from \<CR> or from the text stays '\r'.
func Expand(rep string, m Match, in Input, magic bool) string {
	var b strings.Builder
	var one, all byte
	emitRaw := func(r rune, raw []byte) {
		switch {
		case one != 0:
			r = caseOf(one, r)
			one = 0
		case all != 0:
			r = caseOf(all, r)
		default:
			if raw != nil {
				b.Write(raw)
				return
			}
		}
		b.WriteRune(r)
	}
	emit := func(r rune) { emitRaw(r, nil) }
	for i := 0; i < len(rep); {
		c, l := decodeAt(rep, i)
		group := -1
		var next byte
		if i+1 < len(rep) {
			next = rep[i+1]
		}
		switch {
		case c == '&' && magic:
			group = 0
			i++
		case c == '\\' && i+1 < len(rep) && next == '&' && !magic:
			group = 0
			i += 2
		case c == '\\' && i+1 < len(rep) && next >= '0' && next <= '9':
			group = int(next - '0')
			i += 2
		case c == '\\' && i+1 < len(rep) && strings.IndexByte("uUlLeE", next) >= 0:
			switch next {
			case 'u', 'l':
				one = next
			case 'U', 'L':
				all = next
			default:
				one, all = 0, 0
			}
			i += 2
			continue
		}
		if group < 0 {
			if c == '\\' && i+1 < len(rep) {
				switch next {
				case 'r':
					c, l = '\n', 2
				case 'n':
					c, l = 0, 2
				case 't':
					c, l = '\t', 2
				case 'b':
					c, l = '\b', 2
				default:
					r, rl := decodeAt(rep, i+1)
					c, l = r, 1+rl
				}
			} else if c == '\r' {
				c = '\n'
			}
			i += l
			emit(c)
			continue
		}
		g := m.Groups[group]
		if !g.Set {
			continue
		}
		forText(in, g.Start, g.End, emitRaw)
	}
	return b.String()
}

func caseOf(mode byte, r rune) rune {
	if mode == 'u' || mode == 'U' {
		return unicode.ToUpper(r)
	}
	return unicode.ToLower(r)
}

// forText calls f for each character from a to b, with its bytes, and with '\n' between lines.
func forText(in Input, a, b Pos, f func(rune, []byte)) {
	for p := a; p.before(b); {
		var line []byte
		if p.Line < in.Lines() {
			line = in.Line(p.Line)
		}
		end := len(line)
		if p.Line == b.Line {
			end = min(b.Col, len(line))
		}
		for i := p.Col; i < end; {
			r, l := utf8.DecodeRune(line[i:])
			if r == utf8.RuneError && l <= 1 {
				r = rune(line[i])
				l = 1
			}
			f(r, line[i:i+l])
			i += l
		}
		if p.Line == b.Line || p.Line >= in.Lines() {
			break
		}
		f('\n', nil)
		p = Pos{p.Line + 1, 0}
	}
}
