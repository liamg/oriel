package vim

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/liamg/oriel/regex/vimregex"
	"github.com/liamg/oriel/text"
)

// subFlags are a :s command's flags.
type subFlags struct {
	global, ignoreErr, count bool
	ic, noic                 bool
}

func parseSubFlags(s string, prev string) (subFlags, string, string) {
	var f subFlags
	used := ""
	if strings.HasPrefix(s, "&") {
		s = s[1:]
		s = prev + s
	}
	i := 0
	for ; i < len(s); i++ {
		switch s[i] {
		case 'g':
			f.global = !f.global
		case 'e':
			f.ignoreErr = true
		case 'n':
			f.count = true
		case 'i':
			f.ic = true
		case 'I':
			f.noic = true
		case 'c', 'p', '#', 'l', 'r':
		default:
			return f, s[i:], used
		}
		used += string(s[i])
	}
	return f, s[i:], used
}

// exSubstitute is :s. With repeat (:&) the args are only flags and a count.
func (e *Engine) exSubstitute(v *view, r exRange, args string, repeat bool) error {
	var pat, rep, rest string
	useSearch := false
	prevSub := e.lastSub
	if !repeat && args != "" && !unicode.IsLetter(rune(args[0])) && !unicode.IsDigit(rune(args[0])) && args[0] != '"' && args[0] != '|' && args[0] != ' ' && args[0] != '&' {
		delim := args[0]
		p, after, ok := parseSearch(args[1:], delim)
		pat = p
		if ok {
			rp, after2, _ := cutUnescaped(after, delim)
			rep, rest = rp, after2
		}
		if pat == "" {
			if !e.search.set {
				return errNoPrevPattern.with()
			}
			pat = e.search.pat
		}
		prevSub = e.lastSub
		rep = vimregex.Tilde(rep, e.lastSub, e.opts.Magic)
		e.lastSub = rep
		e.search.subPat = pat
		e.search.pat, e.search.set = pat, true
	} else {
		if e.search.subPat == "" && !e.search.set {
			return errNoPrevPattern.with()
		}
		pat = e.search.subPat
		if useSearch || strings.HasPrefix(args, "r") {
			pat = e.search.pat
			args = strings.TrimPrefix(args, "r")
		}
		rep = e.lastSub
		rest = args
	}
	rest = strings.TrimLeft(rest, " ")
	flags, rest, used := parseSubFlags(rest, e.lastSubFlags)
	if !repeat || strings.HasPrefix(args, "&") {
		e.lastSubFlags = used
	}
	rest = strings.TrimSpace(rest)
	if rest != "" {
		n, err := strconv.Atoi(rest)
		if err != nil {
			return errTrailing.with(rest)
		}
		if n <= 0 {
			return errPositiveCount.with()
		}
		r = withCount(v, r, n)
	}
	o := vimregex.Options{IgnoreCase: e.opts.IgnoreCase, SmartCase: e.opts.SmartCase, Magic: e.opts.Magic, LastSub: prevSub}
	if flags.ic {
		o.IgnoreCase, o.SmartCase = true, false
	}
	if flags.noic {
		o.IgnoreCase = false
	}
	re, err := vimregex.Compile(pat, o)
	if err != nil {
		return err
	}
	return e.substitute(v, r, re, rep, flags, pat)
}

// cutUnescaped splits s at the first delim not escaped by a backslash.
func cutUnescaped(s string, delim byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == delim {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func (e *Engine) exTilde(v *view, r exRange, args string) error {
	if !e.search.set {
		return errNoPrevPattern.with()
	}
	e.search.subPat = e.search.pat
	return e.exSubstitute(v, r, args, true)
}

// substitute runs a compiled :s over the range, as vim's do_sub(): the matches in a line are found in the
// line as it was and the new line is built from them; after a match that joins lines, the search carries on
// in the joined line.
func (e *Engine) substitute(v *view, r exRange, re *vimregex.Regex, rep string, f subFlags, pat string) error {
	subs, lines := 0, 0
	lastLine := -1
	orig := e.cur
	end := r.to
	for line := r.from; line <= end && line < v.lines(); line++ {
		col, copied, prevEnd := 0, 0, -1
		var out strings.Builder
		pending := false
		lineHad := false
		for {
			in := input{v}
			m, ok := re.Exec(in, line, col, e.regexState(v))
			if !ok || m.Start.Line != line {
				break
			}
			empty := m.Start == m.End
			if empty && m.Start.Col == prevEnd {
				if col >= v.lineLen(line) {
					break
				}
				col = nextCol(v, line, col)
				prevEnd = -1
				continue
			}
			subs++
			if !lineHad {
				lineHad = true
				lines++
			}
			endsBuffer := m.End.Line >= v.lines()
			joined := min(m.End.Line, v.lines()-1) - line
			if f.count {
				if joined > 0 || endsBuffer || !f.global {
					break
				}
				col = nextColAfter(v, m, line)
				prevEnd = m.End.Col
				if col > v.lineLen(line) {
					break
				}
				continue
			}
			repl := vimregex.Expand(rep, m, in, e.opts.Magic)
			l := v.line(line)
			if joined > 0 || endsBuffer {
				// Put in what's built so far plus this match, then search on in the line it leaves.
				out.Write(l[copied:m.Start.Col])
				out.WriteString(repl)
				start := v.s.LineStart(line)
				to := v.s.Len()
				if !endsBuffer {
					to = v.offset(toPos(m.End))
				}
				e.cur = Pos{line, 0}
				e.do(e.group, text.Replace{Range: text.Range{Start: start, End: to}, Text: []byte(out.String())})
				v = e.view()
				after := v.pos(start + out.Len())
				lastLine = after.Line
				end += strings.Count(out.String(), "\n") - strings.Count(string(l), "\n") - joined
				out.Reset()
				pending = false
				if endsBuffer {
					end = -1
					break
				}
				// As vim's do_again: an empty match right where the replacement ends doesn't count.
				line, col, copied, prevEnd = after.Line, after.Col, after.Col, after.Col
				lineHad = false
				// Keep what's before the replacement as it is now.
				out.Write(v.line(line)[:col])
				continue
			}
			pending = true
			out.Write(l[copied:m.Start.Col])
			out.WriteString(repl)
			copied = m.End.Col
			col = m.End.Col
			prevEnd = col
			if empty {
				if col >= len(l) {
					break
				}
				_, n := v.char(Pos{line, col})
				out.Write(l[col : col+n])
				copied = col + n
				col = copied
				if col >= len(l) {
					break
				}
			}
			if !f.global {
				break
			}
		}
		if pending {
			l := v.line(line)
			out.Write(l[copied:])
			start := v.s.LineStart(line)
			e.cur = Pos{line, 0}
			e.do(e.group, text.Replace{Range: text.Range{Start: start, End: start + len(l)}, Text: []byte(out.String())})
			added := strings.Count(out.String(), "\n")
			v = e.view()
			line += added
			end += added
			lastLine = line
		}
	}
	if subs == 0 {
		if f.ignoreErr || e.inGlobal {
			return nil
		}
		return errPatternNotFound.with(pat)
	}
	if f.count {
		e.notify(msgMatchCount.with(subs, lines))
		return nil
	}
	if !e.inGlobal {
		e.cur = orig
		e.setPcmark()
	}
	lastLine = min(max(lastLine, 0), v.lines()-1)
	e.cur = Pos{lastLine, v.firstNonBlank(lastLine)}
	e.setWant(v)
	if !e.inGlobal && (subs > e.opts.Report || lines > e.opts.Report) {
		e.notify(msgSubstituted.with(subs, lines))
	}
	return nil
}

func nextColAfter(v *view, m vimregex.Match, line int) int {
	if m.End.Line > line {
		return v.lineLen(line) + 1
	}
	if m.End == m.Start {
		return nextCol(v, line, m.Start.Col)
	}
	return m.End.Col
}
