package vim

import (
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/registry/keymap"
)

// abbrev is an :abbreviate: a word typed as lhs becomes rhs when a non-keyword character follows it.
type abbrev struct {
	rhs     string
	noremap bool
}

// abbrWord finds the abbreviation that ends at col in line, as vim's check_abbr() does: the word before
// the cursor, no earlier than mincol, that ends in a keyword character and is all keyword or all non-keyword
// characters, or ends in a non-keyword character and runs back to white space. It returns the abbreviation,
// how many characters its lhs takes, and whether the word ended in a non-keyword character.
func (e *Engine) abbrWord(mode byte, line string, col, mincol int) (abbrev, int, bool, bool) {
	if col == 0 || col <= mincol || len(e.abbrs[mode]) == 0 {
		return abbrev{}, 0, false, false
	}
	p := col
	last, n := utf8.DecodeLastRuneInString(line[:p])
	p -= n
	nonID := !isKeyword(last)
	isID := false
	if !nonID && p > 0 {
		r, _ := utf8.DecodeLastRuneInString(line[:p])
		isID = isKeyword(r)
	}
	clen := 1
	for p > mincol {
		r, n := utf8.DecodeLastRuneInString(line[:p])
		if isWhite(r) || !nonID && isID != isKeyword(r) {
			break
		}
		p -= n
		clen++
	}
	p = max(p, mincol)
	if p >= col {
		return abbrev{}, 0, false, false
	}
	a, ok := e.abbrs[mode][line[p:col]]
	return a, utf8.RuneCountInString(line[p:col]), nonID, ok
}

// abbrTrigger reports whether a key typed after a word can expand it.
func abbrTrigger(k string) (special, ok bool) {
	switch k {
	case "<Esc>", "<CR>", "<NL>", "<C-]>", "<C-[>", "<C-c>":
		return true, true
	case "<Tab>":
		return false, true
	}
	r, ok := argRune(k)
	return false, ok && !isKeyword(r) && r >= ' '
}

// insertAbbrev expands an abbreviation before the cursor when k ends it, typing backspaces over the word,
// then rhs, then k. It reports whether it did.
func (e *Engine) insertAbbrev(k string) bool {
	in := e.ins
	if e.noAbbr > 0 || in.pendingCtrlR || in.pendingCtrlG || in.pendingCtrlV {
		return false
	}
	special, ok := abbrTrigger(k)
	if !ok {
		return false
	}
	mincol := 0
	if in.start.Line == e.cur.Line {
		mincol = in.start.Col
	}
	v := e.view()
	a, clen, nonID, ok := e.abbrWord('i', string(v.line(e.cur.Line)), e.cur.Col, mincol)
	if !ok || nonID && !special {
		return false
	}
	e.noAbbr++
	defer func() { e.noAbbr-- }()
	e.noremap++
	for range clen {
		e.input("<BS>")
	}
	e.noremap--
	e.runMapping(mapping{rhs: keymap.Parse(a.rhs), noremap: a.noremap})
	if k != "<C-]>" {
		e.noremap++
		e.input(k)
		e.noremap--
	}
	return true
}

// cmdlineAbbrev expands a command-line abbreviation before the end of the command line.
func (e *Engine) cmdlineAbbrev(cl *cmdline, k string) {
	special, ok := abbrTrigger(k)
	if !ok || e.noAbbr > 0 {
		return
	}
	a, clen, nonID, ok := e.abbrWord('c', cl.text, len(cl.text), 0)
	if !ok || nonID && !special {
		return
	}
	t := cl.text
	for range clen {
		_, n := utf8.DecodeLastRuneInString(t)
		t = t[:len(t)-n]
	}
	cl.text = t + a.rhs
}

// exAbbrev runs :abbreviate and its family.
func (e *Engine) exAbbrev(name, args string) error {
	modes := "ic"
	switch name[0] {
	case 'i':
		modes = "i"
	case 'c':
		modes = "c"
	}
	if strings.HasSuffix(name, "abclear") {
		for _, m := range []byte(modes) {
			delete(e.abbrs, m)
		}
		return nil
	}
	args = strings.TrimLeft(args, " \t")
	lhs, rhs, _ := strings.Cut(args, " ")
	rhs = strings.TrimLeft(rhs, " \t")
	if strings.Contains(name, "una") {
		found := false
		for _, m := range []byte(modes) {
			for l, a := range e.abbrs[m] {
				if l == lhs || a.rhs == lhs {
					delete(e.abbrs[m], l)
					found = true
				}
			}
		}
		if !found {
			return exError("E24: No such abbreviation")
		}
		return nil
	}
	if lhs == "" || rhs == "" {
		return nil
	}
	if e.abbrs == nil {
		e.abbrs = map[byte]map[string]abbrev{}
	}
	for _, m := range []byte(modes) {
		if e.abbrs[m] == nil {
			e.abbrs[m] = map[string]abbrev{}
		}
		e.abbrs[m][lhs] = abbrev{rhs: rhs, noremap: strings.Contains(name, "nore")}
	}
	return nil
}
