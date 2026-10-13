// Package fuzzy is the pickers' matcher: fzf-style scoring of a pattern against text, and fzf's query syntax
// (ui.md 02b). A pattern matches when its characters appear in order; the score favours runs of them, starts of
// words and camel humps, and an early first match, as fzf's algorithm v2 does.
package fuzzy

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scores, as fzf's.
const (
	scoreMatch        = 16
	scoreGapStart     = -3
	scoreGapExtension = -1
	bonusBoundary     = scoreMatch / 2
	bonusNonWord      = scoreMatch / 2
	bonusCamel        = bonusBoundary + scoreGapExtension
	bonusConsecutive  = -(scoreGapStart + scoreGapExtension)
	bonusFirstChar    = 2 // the first pattern character's bonus counts this many times
	bonusBoundaryPath = bonusBoundary + 1
)

type class int

const (
	classNonWord class = iota
	classDelim         // / , : ; | and space
	classLower
	classUpper
	classNumber
)

func classOf(r rune) class {
	switch {
	case unicode.IsLower(r):
		return classLower
	case unicode.IsUpper(r):
		return classUpper
	case unicode.IsDigit(r):
		return classNumber
	case strings.ContainsRune("/,:;| ", r):
		return classDelim
	case unicode.IsLetter(r):
		return classLower
	}
	return classNonWord
}

// bonus is what matching a character of class cur after one of class prev earns.
func bonus(prev, cur class) int {
	switch {
	case cur > classDelim && prev == classDelim:
		return bonusBoundaryPath
	case cur > classDelim && prev == classNonWord:
		return bonusBoundary
	case prev == classLower && cur == classUpper, prev != classNumber && cur == classNumber:
		return bonusCamel
	case cur == classNonWord || cur == classDelim:
		return bonusNonWord
	}
	return 0
}

// Match scores pattern against text. Lowercase pattern characters match either case; uppercase ones only their
// own (smart case). It returns the best score, the rune indexes of text that matched, and whether it matched.
func Match(pattern, text string) (score int, pos []int, ok bool) {
	p := []rune(pattern)
	t := []rune(text)
	n := len(p)
	if n == 0 {
		return 0, nil, true
	}
	eq := func(a, b rune) bool {
		if unicode.IsUpper(a) {
			return a == b
		}
		return a == unicode.ToLower(b)
	}
	// Does it match at all, and between where?
	first, last := -1, -1
	for i, j := 0, 0; i < len(t) && j < n; i++ {
		if eq(p[j], t[i]) {
			if j == 0 {
				first = i
			}
			if j++; j == n {
				last = i
			}
		}
	}
	if last < 0 {
		return 0, nil, false
	}
	for j := last + 1; j < len(t); j++ { // the last character can match later; keep the text it could match in
		if eq(p[n-1], t[j]) {
			last = j
		}
	}
	t = t[:last+1]
	m := len(t)
	bonuses := make([]int, m)
	prev := classDelim
	for i, r := range t {
		c := classOf(r)
		bonuses[i] = bonus(prev, c)
		prev = c
	}
	// H[i][j] is the best score with p[i] matched at t[j]; run[i][j] how many matches end there in a row, and
	// from[i][j] where p[i-1] matched on the way.
	const neg = -1 << 30
	H, run, from := make([][]int, n), make([][]int, n), make([][]int, n)
	for i := range n {
		H[i], run[i], from[i] = make([]int, m), make([]int, m), make([]int, m)
		for j := range m {
			H[i][j] = neg
		}
	}
	for j := first; j < m; j++ {
		if eq(p[0], t[j]) {
			H[0][j], run[0][j] = scoreMatch+bonuses[j]*bonusFirstChar, 1
		}
	}
	for i := 1; i < n; i++ {
		gap, gapFrom := neg, -1 // the best H[i-1][k] for k <= j-2, less the gap from k to j
		for j := first + i; j < m; j++ {
			if gap > neg {
				gap += scoreGapExtension
			}
			if k := j - 2; k >= 0 && H[i-1][k] > neg && H[i-1][k]+scoreGapStart > gap {
				gap, gapFrom = H[i-1][k]+scoreGapStart, k
			}
			if !eq(p[i], t[j]) {
				continue
			}
			best, r, f := neg, 0, -1
			if c := H[i-1][j-1]; c > neg {
				b := max(bonuses[j], bonusConsecutive, bonuses[j-run[i-1][j-1]])
				best, r, f = c+scoreMatch+b, run[i-1][j-1]+1, j-1
			}
			if gap > neg && gap+scoreMatch+bonuses[j] > best {
				best, r, f = gap+scoreMatch+bonuses[j], 1, gapFrom
			}
			H[i][j], run[i][j], from[i][j] = best, r, f
		}
	}
	end := -1
	for j := range m {
		if H[n-1][j] > neg && (end < 0 || H[n-1][j] > H[n-1][end]) {
			end = j
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	pos = make([]int, n)
	pos[n-1] = end
	for i := n - 1; i > 0; i-- {
		pos[i-1] = from[i][pos[i]]
	}
	return H[n-1][end], pos, true
}

// Term is one part of a query: a fuzzy pattern, or an exact, prefix, suffix or negated one.
type Term struct {
	Text                  string
	Exact, Prefix, Suffix bool
	Not                   bool
}

// Parse reads fzf's query syntax: terms separated by spaces, all of which must match; 'exact, ^prefix, suffix$,
// and !not (which is exact).
func Parse(query string) []Term {
	var out []Term
	for _, f := range strings.Fields(query) {
		var t Term
		if s, ok := strings.CutPrefix(f, "!"); ok {
			t.Not, t.Exact, f = true, true, s
		}
		if s, ok := strings.CutPrefix(f, "'"); ok {
			t.Exact, f = true, s
		}
		if s, ok := strings.CutPrefix(f, "^"); ok {
			t.Prefix, f = true, s
		}
		if s, ok := strings.CutSuffix(f, "$"); ok && s != "" {
			t.Suffix, f = true, s
		}
		if f == "" {
			continue
		}
		t.Text = f
		out = append(out, t)
	}
	return out
}

// MatchQuery matches every term of a parsed query; the score is the terms' total.
func MatchQuery(terms []Term, text string) (score int, pos []int, ok bool) {
	for _, t := range terms {
		s, p, hit := t.match(text)
		if hit == t.Not {
			return 0, nil, false
		}
		if !t.Not {
			score += s
			pos = append(pos, p...)
		}
	}
	return score, pos, true
}

func (t Term) match(text string) (int, []int, bool) {
	fold := func(s string) string {
		if strings.ToLower(t.Text) != t.Text {
			return s
		}
		return strings.ToLower(s)
	}
	hay, needle := fold(text), fold(t.Text)
	runeAt := func(byteOff int) int { return utf8.RuneCountInString(hay[:byteOff]) }
	span := func(start int) []int {
		var p []int
		for i := range utf8.RuneCountInString(needle) {
			p = append(p, runeAt(start)+i)
		}
		return p
	}
	exactScore := func(start int) int {
		s, _, _ := Match(t.Text, text[start:start+len(needle)])
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if start == 0 {
			r = ' '
		}
		return s + bonus(classOf(r), classOf([]rune(t.Text)[0]))
	}
	switch {
	case t.Prefix && t.Suffix:
		if hay == needle {
			return exactScore(0), span(0), true
		}
	case t.Prefix:
		if strings.HasPrefix(hay, needle) {
			return exactScore(0), span(0), true
		}
	case t.Suffix:
		if strings.HasSuffix(hay, needle) {
			at := len(hay) - len(needle)
			return exactScore(at), span(at), true
		}
	case t.Exact:
		if at := strings.Index(hay, needle); at >= 0 {
			return exactScore(at), span(at), true
		}
	default:
		return Match(t.Text, text)
	}
	return 0, nil, false
}
