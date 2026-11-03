package gitx

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DiffLine is one line of a unified diff: context (' '), removed ('-') or added ('+').
type DiffLine struct {
	Op       byte
	Text     string // with its line ending
	Old, New int    // 1-based line numbers on each side; 0 on a side the line isn't on
}

// DiffHunk is a hunk of a unified diff: changes with lines of context around them, numbered as git numbers them.
type DiffHunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []DiffLine
	Changes            []Hunk // its runs of changed lines, without context
}

// Header is the hunk's @@ line, as git writes it.
func (h DiffHunk) Header() string {
	return Hunk{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines}.Header()
}

// IDs are its changes' IDs, in order.
func (h DiffHunk) IDs() []string {
	out := make([]string, len(h.Changes))
	for i, c := range h.Changes {
		out[i] = c.ID()
	}
	return out
}

// ID names a change by what it removes and adds, not where: the same change keeps its ID while other parts of the
// file are edited.
func (h Hunk) ID() string {
	s := sha256.New()
	for _, l := range h.Old {
		s.Write([]byte{'-'})
		s.Write([]byte(l))
	}
	s.Write([]byte{0})
	for _, l := range h.New {
		s.Write([]byte{'+'})
		s.Write([]byte(l))
	}
	return hex.EncodeToString(s.Sum(nil)[:8])
}

// start is where a hunk's lines begin on each side, 0-based.
func (h Hunk) start() (old, new int) {
	old, new = h.OldStart-1, h.NewStart-1
	if h.OldLines == 0 {
		old = h.OldStart
	}
	if h.NewLines == 0 {
		new = h.NewStart
	}
	return old, new
}

// Unified is the diff from old to new as git diff -U<context> has it: changes closer than twice the context share
// a hunk.
func Unified(old, new []byte, context int) []DiffHunk {
	a, b := SplitLines(old), SplitLines(new)
	changes := diffLines(a, b)
	var out []DiffHunk
	for i := 0; i < len(changes); {
		j := i + 1
		for j < len(changes) {
			po, _ := changes[j-1].start()
			no, _ := changes[j].start()
			if no-(po+changes[j-1].OldLines) > 2*context {
				break
			}
			j++
		}
		out = append(out, hunkOf(a, b, changes[i:j], context))
		i = j
	}
	return out
}

func hunkOf(a, b []string, changes []Hunk, context int) DiffHunk {
	fo, fn := changes[0].start()
	lo, _ := changes[len(changes)-1].start()
	o0 := max(fo-context, 0)
	n0 := fn - (fo - o0)
	oe := min(lo+changes[len(changes)-1].OldLines+context, len(a))
	h := DiffHunk{Changes: append([]Hunk(nil), changes...)}
	i, j := o0, n0
	ctx := func(to int) {
		for ; i < to; i, j = i+1, j+1 {
			h.Lines = append(h.Lines, DiffLine{Op: ' ', Text: a[i], Old: i + 1, New: j + 1})
		}
	}
	for _, c := range changes {
		co, _ := c.start()
		ctx(co)
		for _, l := range c.Old {
			i++
			h.Lines = append(h.Lines, DiffLine{Op: '-', Text: l, Old: i})
		}
		for _, l := range c.New {
			j++
			h.Lines = append(h.Lines, DiffLine{Op: '+', Text: l, New: j})
		}
	}
	ctx(oe)
	h.OldLines, h.NewLines = i-o0, j-n0
	h.OldStart, h.NewStart = o0+1, n0+1
	if h.OldLines == 0 {
		h.OldStart = o0
	}
	if h.NewLines == 0 {
		h.NewStart = n0
	}
	return h
}

// Intraline pairs a change's removed and added lines in order and finds the words that differ in each pair: byte
// ranges of each old line and each new line. A pair too unlike to compare word by word has none.
func (h Hunk) Intraline() (old, new [][][2]int) {
	old, new = make([][][2]int, len(h.Old)), make([][][2]int, len(h.New))
	for k := range min(len(h.Old), len(h.New)) {
		old[k], new[k] = Intraline(h.Old[k], h.New[k])
	}
	return old, new
}

// maxIntraline is the longest line compared word by word.
const maxIntraline = 1000

// Intraline is the words that differ between two versions of a line, as byte ranges of each; nil when they share
// too little for that to help (under a third of the longer line's non-blank text).
func Intraline(a, b string) (del, add [][2]int) {
	a, b = strings.TrimRight(a, "\r\n"), strings.TrimRight(b, "\r\n")
	if a == b || len(a) > maxIntraline || len(b) > maxIntraline {
		return nil, nil
	}
	ta, tb := words(a), words(b)
	ops := myers(ta, tb)
	common, i, j, oa, ob := 0, 0, 0, 0, 0
	push := func(rs [][2]int, s, e int) [][2]int {
		if n := len(rs); n > 0 && rs[n-1][1] == s {
			rs[n-1][1] = e
			return rs
		}
		return append(rs, [2]int{s, e})
	}
	for _, op := range ops {
		switch op {
		case '=':
			if strings.TrimSpace(ta[i]) != "" {
				common += len(ta[i])
			}
			oa, ob = oa+len(ta[i]), ob+len(tb[j])
			i++
			j++
		case '-':
			del = push(del, oa, oa+len(ta[i]))
			oa += len(ta[i])
			i++
		case '+':
			add = push(add, ob, ob+len(tb[j]))
			ob += len(tb[j])
			j++
		}
	}
	solid := func(s string) int { return len(strings.Join(strings.Fields(s), "")) }
	if common*3 < max(solid(a), solid(b)) {
		return nil, nil
	}
	return del, add
}

// words splits a line into words (letters, digits and _), runs of space, and single other characters.
func words(s string) []string {
	var out []string
	class := func(r rune) int {
		switch {
		case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			return 1
		case unicode.IsSpace(r):
			return 2
		}
		return 0
	}
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		k := class(r)
		if k != 0 {
			for n < len(s) {
				r2, m := utf8.DecodeRuneInString(s[n:])
				if class(r2) != k {
					break
				}
				n += m
			}
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

// FuncLine is the line git puts after a hunk's @@ by default: the nearest one before line (1-based) that starts
// with a letter, _ or $, without its line ending; "" for none.
func FuncLine(lines []string, line int) string {
	for i := min(line-1, len(lines)) - 1; i >= 0; i-- {
		if l := lines[i]; l != "" && (l[0] == '_' || l[0] == '$' || unicode.IsLetter(rune(l[0]))) {
			return strings.TrimRight(l, "\r\n")
		}
	}
	return ""
}
