// Package vimregex is a backtracking matcher for vim's regular expressions, as pattern.txt defines them,
// following nvim where its engines differ. Patterns compile to a tree that is matched in the spirit of
// vim's regexp_bt.c: leftmost match, first alternative wins, greedy or lazy multis.
//
// One Exec or MatchAt call may take at most 10 million steps and nest at most 50000 deep;
// past either it fails with ErrTooComplex instead of hanging or overflowing the stack.
package vimregex

//go:generate sh -c "nvim --headless --clean -l gen_tables.lua > tables.go"

import (
	"bytes"
	"errors"
	"strings"
)

// Pos is a position in the text: 0-based line and byte column.
type Pos struct{ Line, Col int }

// Input is the text being searched, line by line, without newlines.
type Input interface {
	Lines() int
	Line(n int) []byte
}

// State is editor state some atoms read; any field may be zero.
type State struct {
	Cursor Pos                      // \%#, \%.l, \%.c, \%.v
	Visual *Area                    // \%V (nil: no area)
	Mark   func(r rune) (Pos, bool) // \%'m, \%<'m, \%>'m
	VCol   func(line, col int) int  // screen cells before byte col, for \%v and blockwise \%V; nil: byte column

	// \k \i \f \p; nil uses nvim's defaults: iskeyword "@,48-57,_,192-255", isident "@,48-57,_,192-255",
	// isfname "@,48-57,/,.,-,_,+,,,#,$,%,~,=", isprint "@,161-255".
	IsKeyword, IsIdent, IsFname, IsPrint func(r rune) bool
}

// Area is a Visual area. End is inclusive. Mode is 'v', 'V' or Ctrl-V (0x16); for a block, the columns
// it covers are screen columns, from State.VCol.
type Area struct {
	Start, End Pos
	Mode       byte
}

// Options are the options that change how a pattern compiles.
type Options struct {
	IgnoreCase bool   // 'ignorecase'
	SmartCase  bool   // 'smartcase'
	Magic      bool   // 'magic' (true in vim by default)
	LastSub    string // the previous substitute string, matched by ~
}

// Match is one match. End is exclusive and may be on a later line; \zs and \ze are applied.
// A match that takes the end-of-line of the last line ends at Pos{Lines(), 0}; with "\n\zs" its Start can
// be there too, which :s and searches skip, as vim does.
type Match struct {
	Start, End Pos
	Groups     [10]Group // \0 is the whole match; \1..\9 the submatches
}

// Group is one submatch. Set is false for a group that took no part in the match.
type Group struct {
	Start, End Pos
	Set        bool
}

// ErrTooComplex is returned when matching runs past the step or depth budget.
var ErrTooComplex = errors.New("E363: pattern uses more memory than 'maxmempattern'")

// The budget for one Exec or MatchAt call; variables so tests can lower them.
var (
	maxSteps = 10_000_000
	maxDepth = 50_000
)

// Regex is a compiled pattern.
type Regex struct {
	prog     *node
	ic       bool
	icombine bool
	first    byte // an ASCII character every match starts with, or 0
}

// Compile compiles a vim pattern. Errors carry vim's message, e.g. "E54: Unmatched \(".
func Compile(pattern string, o Options) (*Regex, error) {
	if strings.HasPrefix(pattern, `\%#=`) && (len(pattern) < 5 || pattern[4] < '0' || pattern[4] > '2') {
		return nil, errors.New(`E864: \%#= can only be followed by 0, 1, or 2. The automatic engine will be used `)
	}
	pattern = stripEngine(pattern)
	magic := magicOff
	if o.Magic {
		magic = magicOn
	}
	p := newParser(pattern, magic, o.LastSub)
	prog := p.parse()
	if p.err != nil {
		return nil, p.err
	}
	ic := o.IgnoreCase
	if ic && o.SmartCase && hasUpper(pattern, magic) {
		ic = false
	}
	if p.icase {
		ic = true
	} else if p.noicase {
		ic = false
	}
	re := &Regex{prog: prog, ic: ic, icombine: p.icombine}
	if r := firstRune(prog); !ic && r > 0 && r < 0x80 {
		re.first = byte(r)
	}
	return re, nil
}

// stripEngine drops a leading \%#=0, \%#=1 or \%#=2: there is one engine here.
func stripEngine(s string) string {
	if len(s) >= 5 && s[:4] == `\%#=` && s[4] >= '0' && s[4] <= '2' {
		return s[5:]
	}
	return s
}

// firstRune is the character every match of n must start with, or -1 when there isn't one.
func firstRune(n *node) rune {
	switch n.op {
	case opChar:
		if len(n.comb) == 0 {
			return n.r
		}
	case opCapture, opAtomic:
		return firstRune(n.subs[0])
	case opRepeat:
		if n.min > 0 {
			return firstRune(n.subs[0])
		}
	case opConcat:
		for _, s := range n.subs {
			if !zeroWidth(s) {
				return firstRune(s)
			}
		}
	case opAlt:
		r := firstRune(n.subs[0])
		for _, s := range n.subs[1:] {
			if firstRune(s) != r {
				return -1
			}
		}
		return r
	}
	return -1
}

func zeroWidth(n *node) bool {
	switch n.op {
	case opEmpty, opZS, opZE, opAhead, opNotAhead, opBehind, opNotBehind:
		return true
	}
	return n.op >= opBOL && n.op <= opVcol
}

// Multiline reports whether the pattern can match an end-of-line with \n or a \_x class, which is
// what nvim's :s checks before it looks for another match after one that ends a line. A collection
// with \_[] or [\n] doesn't count, as in nvim.
func (r *Regex) Multiline() bool { return multiline(r.prog) }

func multiline(n *node) bool {
	if n.op == opNL || n.nl && n.op != opSet {
		return true
	}
	for _, s := range n.subs {
		if multiline(s) {
			return true
		}
	}
	return false
}

// Exec finds the first match that starts at or after col on line `line`, trying each start on that line
// in turn (vim's vim_regexec_multi). The match may extend over following lines.
func (r *Regex) Exec(in Input, line, col int, st *State) (Match, bool) {
	m, ok, _ := r.ExecErr(in, line, col, st)
	return m, ok
}

// ExecErr is Exec that also reports ErrTooComplex.
func (r *Regex) ExecErr(in Input, line, col int, st *State) (Match, bool, error) {
	m := r.newMatcher(in, st)
	if line < 0 || line >= m.n || col < 0 {
		return Match{}, false, nil
	}
	text := m.line(line)
	if col > len(text) {
		return Match{}, false, nil
	}
	for {
		if r.first != 0 {
			i := bytes.IndexByte(text[col:], r.first)
			if i < 0 {
				return Match{}, false, nil
			}
			col += i
		}
		if res, ok := m.try(pos{line, col}); ok {
			return res, true, nil
		}
		if m.err != nil {
			return Match{}, false, m.err
		}
		if col >= len(text) {
			return Match{}, false, nil
		}
		col = clusterEnd(text, col)
	}
}

// MatchAt reports whether a match starts exactly at (line, col).
func (r *Regex) MatchAt(in Input, line, col int, st *State) (Match, bool) {
	m, ok, _ := r.MatchAtErr(in, line, col, st)
	return m, ok
}

// MatchAtErr is MatchAt that also reports ErrTooComplex.
func (r *Regex) MatchAtErr(in Input, line, col int, st *State) (Match, bool, error) {
	m := r.newMatcher(in, st)
	if line < 0 || line >= m.n || col < 0 || col > len(m.line(line)) {
		return Match{}, false, nil
	}
	res, ok := m.try(pos{line, col})
	return res, ok, m.err
}
