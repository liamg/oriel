// Package regex is PCRE2's pattern syntax over text, for everything that isn't the vim engine: project search and
// replace, the plain engine's find, plugins and Lua (editor.md, "Regex"). It's a second parser on the vim engine's
// backtracking matcher (vimregex): look-around, back references, atomic groups and possessive quantifiers, named
// groups, \K, Unicode properties. Recursion, conditionals, verbs and callouts aren't supported (IsUnsupported).
package regex

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/vim/vimregex"
)

// Flags are the options PCRE2 takes outside the pattern: i, m, s, x, xx and n.
type Flags = vimregex.PCREFlags

// Regex is a compiled pattern.
type Regex struct {
	re  *vimregex.Regex
	src string
}

// ErrTooComplex is a match that ran past the step or depth budget.
var ErrTooComplex = vimregex.ErrTooComplex

// Compile compiles a PCRE2 pattern.
func Compile(pattern string, f Flags) (*Regex, error) {
	re, err := vimregex.CompilePCRE(pattern, f)
	if err != nil {
		return nil, err
	}
	return &Regex{re: re, src: pattern}, nil
}

// MustCompile is Compile that panics on an error.
func MustCompile(pattern string, f Flags) *Regex {
	r, err := Compile(pattern, f)
	if err != nil {
		panic(err)
	}
	return r
}

// IsUnsupported reports whether err is a PCRE2 feature this package doesn't have.
func IsUnsupported(err error) bool {
	var u *vimregex.UnsupportedError
	return errors.As(err, &u)
}

func (r *Regex) String() string { return r.src }

// NumGroups is how many capture groups the pattern has.
func (r *Regex) NumGroups() int { return r.re.Groups() - 1 }

// Names are the named groups' numbers.
func (r *Regex) Names() map[string]int { return r.re.Names() }

// subject is text as the matcher reads it: lines, without their newlines.
type subject struct {
	lines []string
	at    []int // each line's offset
}

func newSubject(s string) *subject {
	sub := &subject{lines: strings.Split(s, "\n")}
	off := 0
	for _, l := range sub.lines {
		sub.at = append(sub.at, off)
		off += len(l) + 1
	}
	return sub
}

func (s *subject) Lines() int        { return len(s.lines) }
func (s *subject) Line(n int) []byte { return []byte(s.lines[n]) }

func (s *subject) offset(p vimregex.Pos) int {
	if p.Line >= len(s.at) {
		return s.at[len(s.at)-1] + len(s.lines[len(s.lines)-1])
	}
	return s.at[p.Line] + p.Col
}

func (s *subject) pos(off int) vimregex.Pos {
	line := 0
	for line+1 < len(s.at) && s.at[line+1] <= off {
		line++
	}
	return vimregex.Pos{Line: line, Col: off - s.at[line]}
}

// FindIndex is the first match at or after byte offset from: start and end of the match, then of each group (-1
// for a group that took no part), as Go's FindStringSubmatchIndex gives them.
func (r *Regex) FindIndex(s string, from int) ([]int, error) {
	sub := newSubject(s)
	return r.find(sub, from, false)
}

func (r *Regex) find(sub *subject, from int, notEmpty bool) ([]int, error) {
	m, ok, err := r.re.Find(sub, sub.pos(from), notEmpty)
	if err != nil || !ok {
		return nil, err
	}
	out := make([]int, 0, 2*r.re.Groups())
	for i := 0; i < r.re.Groups(); i++ {
		g := m.Groups[i]
		if !g.Set {
			out = append(out, -1, -1)
			continue
		}
		out = append(out, sub.offset(g.Start), sub.offset(g.End))
	}
	return out, nil
}

// MatchString reports whether s has a match.
func (r *Regex) MatchString(s string) bool {
	loc, _ := r.FindIndex(s, 0)
	return loc != nil
}

// FindAllIndex is every match in s, up to n (all when n < 0), as FindIndex gives each. After an empty match the
// next may not be empty at the same place, as PCRE2's global matching goes on.
func (r *Regex) FindAllIndex(s string, n int) ([][]int, error) {
	sub := newSubject(s)
	var out [][]int
	at, notEmpty := 0, false
	for n < 0 || len(out) < n {
		loc, err := r.find(sub, at, notEmpty)
		if err != nil {
			return out, err
		}
		if loc == nil {
			if !notEmpty || at >= len(s) {
				break
			}
			_, w := utf8.DecodeRuneInString(s[at:])
			at, notEmpty = at+w, false
			continue
		}
		out = append(out, loc)
		at, notEmpty = loc[1], loc[0] == loc[1]
	}
	return out, nil
}

// ReplaceAll replaces every match in s with repl, read as PCRE2's substitute reads it: $n, ${n}, $name, ${name}
// and $$. It reports how many it replaced.
func (r *Regex) ReplaceAll(s, repl string) (string, int, error) {
	locs, err := r.FindAllIndex(s, -1)
	if err != nil {
		return s, 0, err
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		out, err := vimregex.ExpandPCRE(repl, func(i int) (string, bool) {
			if i < 0 || 2*i >= len(loc) || loc[2*i] < 0 {
				return "", false
			}
			return s[loc[2*i]:loc[2*i+1]], true
		}, func(n string) (int, bool) { i, ok := r.Names()[n]; return i, ok })
		if err != nil {
			return s, 0, err
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(out)
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String(), len(locs), nil
}
