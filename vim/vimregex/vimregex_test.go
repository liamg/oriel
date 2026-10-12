package vimregex

import (
	"errors"
	"strings"
	"testing"
)

// The case tables run each pattern as ":s/{pat}/(&)/" over the given text. With ORIEL_ORACLE=1 the
// same commands also run in nvim, and every want must agree with it.

func TestPatternStructure(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */bar*
		// quote: If more than one branch matches, the first one is used.
		{pat: `foo\|beep`, in: "foo beep", g: true, want: "(foo) (beep)"},
		{pat: `foo\|foobar`, in: "foobar", want: "(foo)bar"},
		{pat: `foobar\|foo`, in: "foobar", want: "(foobar)"},
		{pat: `c\(a\|o\)t`, in: "cat cot cut", g: true, want: "(cat) (cot) cut"},

		// ref:   pattern.txt */\&*
		// quote: It matches the last concat, but only if all the preceding concats also match at the same position.
		{pat: `foobeep\&...`, in: "foobeep", want: "(foo)beep"},
		{pat: `.*Peter\&.*Bob`, in: "Peter and Bob\nBob and Peter\nPeter only", rep: "X", want: "X\nX and Peter\nPeter only"},
		{pat: `a\&b`, in: "ab", want: "ab"},

		// ref:   pattern.txt */concat*
		// quote: "\"f[0-9]b\", first matches \"f\", then a digit and then \"b\"."
		{pat: `f[0-9]b`, in: "f1b fb f12b", g: true, want: "(f1b) fb f12b"},
	})
}

func TestMagic(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */\v*
		// quote: Use of "\v" means that after it, all ASCII characters except '0'-'9', 'a'-'z', 'A'-'Z' and '_' have special meaning: "very magic"
		{pat: `\v(ab)+`, in: "ababx", want: "(abab)x"},
		{pat: `\v\d{2}`, in: "12345", g: true, want: "(12)(34)5"},
		{pat: `\vfoo|baz`, in: "foo bar baz", g: true, want: "(foo) bar (baz)"},
		{pat: `\vfoo=`, in: "fo foo", g: true, want: "(fo) (foo)"},
		{pat: `\v<is>`, in: "is this is", g: true, want: "(is) this (is)"},
		{pat: `\va\.c`, in: "abc a.c", g: true, want: "abc (a.c)"},
		{pat: `\va\{`, in: "a{ ab", g: true, want: "(a{) ab"},
		{pat: `\v(a)(b)`, in: "ab", rep: `\2\1`, want: "ba"},
		{pat: `\v\\`, in: `a\b`, want: `a(\)b`},
		{pat: `\va$`, in: "aa", want: "a(a)"},
		{pat: `\v^a`, in: "aa", g: true, want: "(a)a"},
		{pat: `\v%(a)b`, in: "ab", want: "(ab)"},
		{pat: `\v\_s+`, in: "a \n b", rep: "X", want: "aXbX"},
		{pat: `\v_s+`, in: "a_ss", want: "a(_ss)"},

		// ref:   pattern.txt */\V*
		// quote: Use of "\V" means that after it, only a backslash and the terminating character (usually / or ?) have special meaning: "very nomagic"
		{pat: `\Va.c*`, in: "a.c* abc", want: "(a.c*) abc"},
		{pat: `\V$`, in: "a$b", want: "a($)b"},
		{pat: `\Vab\$`, in: "abab", want: "ab(ab)"},
		{pat: `\Va.b\|c*d`, in: "a.b c*d", g: true, want: "(a.b) (c*d)"},
		{pat: `\V\^a`, in: "aa", g: true, want: "(a)a"},
		{pat: `\V^a`, in: "^aa", want: "(^a)a"},

		// ref:   pattern.txt */\M*
		// quote: Use of "\M" makes the pattern after it be interpreted as if 'nomagic' is used.
		{pat: `\Ma.c`, in: "abc a.c", g: true, want: "abc (a.c)"},
		{pat: `\Ma\.c`, in: "abc", want: "(abc)"},
		{pat: `\Ma*`, in: "aa*", g: true, want: "a(a*)"},
		{pat: `\Mab$`, in: "abab", want: "ab(ab)"},
		{pat: `\Ma[b]`, in: "a[b] ab", want: "(a[b]) ab"},
		{pat: `\Ma\[b]`, in: "a[b] ab", want: "a[b] (ab)"},
		{pat: `a.c`, in: "abc a.c", rep: `(\&)`, g: true, nomagic: true, want: "abc (a.c)"},
		{pat: `\ma.c`, in: "abc a.c", rep: `(\&)`, g: true, nomagic: true, want: "(abc) (a.c)"},

		// ref:   pattern.txt */\m*
		// quote: Use of "\m" makes the pattern after it be interpreted as if 'magic' is set, ignoring the actual value of the 'magic' option.
		{pat: `\va+\m+`, in: "aa+", want: "(aa+)"},
		{pat: `a{2}`, in: "a{2} aa", g: true, want: "(a{2}) aa"},
		{pat: `a+`, in: "a+ aa", g: true, want: "(a+) aa"},

		// ref:   pattern.txt */magic*
		// quote: If a character is taken literally or not depends on the 'magic' option and the items in the pattern mentioned next.
		{pat: `\~`, in: "a~b", want: "a(~)b"},
		{pat: `~`, in: "b X", last: "X", want: "b (X)"},
		{pat: `a~`, in: "aXY aXY", last: "XY", g: true, want: "(aXY) (aXY)"},
		{pat: `~*`, in: "ab", last: "a", want: "(a)b"},
		{pat: `\~`, in: "b X", rep: `(\&)`, last: "X", nomagic: true, want: "b (X)"},
		{pat: `\y`, in: "xyz", want: "x(y)z"},
	})
}

func TestMulti(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */star*
		// quote: Matches 0 or more of the preceding atom, as many as possible.
		{pat: `ba*`, in: "baaac", want: "(baaa)c"},
		{pat: `ba*c`, in: "bc", want: "(bc)"},
		{pat: `x*`, in: "abc", rep: "-", g: true, want: "-a-b-c"},
		{pat: `x*`, in: "xab", rep: "-", g: true, want: "-a-b"},
		{pat: `=.*`, in: "a=1\nb=2", want: "a(=1)\nb(=2)"},
		{pat: `=\_.*`, in: "a=1\nb=2", rep: "X", want: "aX"},
		{pat: `a\_.*END`, in: "a END\nb END c", rep: "X", want: "X c"},
		{pat: `\(ab\)*`, in: "ababc", want: "(abab)c"},

		// ref:   pattern.txt */star*
		// quote: Exception: When "*" is used at the start of the pattern or just after "^" it matches the star character.
		{pat: `*`, in: "a*b", want: "a(*)b"},
		{pat: `^*`, in: "*a*", g: true, want: "(*)a*"},
		{pat: `\(*a\)`, in: "x*a", want: "x(*a)"},
		{pat: `x\|*`, in: "a*", g: true, want: "a(*)"},

		// ref:   pattern.txt */\+*
		// quote: Matches 1 or more of the preceding atom, as many as possible.
		{pat: `a\+b`, in: "b ab aab", g: true, want: "b (ab) (aab)"},
		{pat: `^.\+$`, in: "a\n\nb", want: "(a)\n\n(b)"},
		{pat: `\s\+`, in: "a  b\tc", g: true, want: "a(  )b(\t)c"},

		// ref:   pattern.txt */\=*
		// quote: Matches 0 or 1 of the preceding atom, as many as possible.
		{pat: `foo\=`, in: "fo foo fooo", g: true, want: "(fo) (foo) (foo)o"},
		{pat: `foo\?`, in: "fo foo", g: true, want: "(fo) (foo)"},

		// ref:   pattern.txt */\{*
		// quote: \{n,m}	Matches n to m of the preceding atom, as many as possible
		{pat: `ab\{2,3}c`, in: "abc abbc abbbc abbbbc", g: true, want: "abc (abbc) (abbbc) abbbbc"},
		{pat: `a\{5}`, in: "aaaaaaa", want: "(aaaaa)aa"},
		{pat: `ab\{2,}c`, in: "abc abbbbc", g: true, want: "abc (abbbbc)"},
		{pat: `ab\{,3}c`, in: "ac abbbc abbbbc", g: true, want: "(ac) (abbbc) abbbbc"},
		{pat: `ba\{}`, in: "baaac", want: "(baaa)c"},
		{pat: `a[bc]\{3}d`, in: "abbbd acbcd abcd", g: true, want: "(abbbd) (acbcd) abcd"},
		{pat: `a\(bc\)\{1,2}d`, in: "abcd abcbcd abcbcbcd", g: true, want: "(abcd) (abcbcd) abcbcbcd"},
		{pat: `a\{2\}`, in: "aaa", want: "(aa)a"},
		{pat: `a\{3,1}`, in: "aaaa", g: true, want: "(aaa)(a)"},
		{pat: `a\{`, in: "a", want: "error: E554: Syntax error in \\{...}"},

		// ref:   pattern.txt *non-greedy*
		// quote: BUT: A match that starts earlier is preferred over a shorter match: "a\{-}b" matches "aaab" in "xaaab".
		{pat: `a\{-}b`, in: "xaaab", want: "x(aaab)"},
		{pat: `a[bc]\{-}[cd]`, in: "abcd", want: "(abc)d"},
		{pat: `a[bc]*[cd]`, in: "abcd", want: "(abcd)"},
		{pat: `<.\{-}>`, in: "<a><b>", g: true, want: "(<a>)(<b>)"},
		{pat: `<.*>`, in: "<a><b>", g: true, want: "(<a><b>)"},
		{pat: `a\{-1,}`, in: "aaa", g: true, want: "(a)(a)(a)"},
		{pat: `a\{-2,4}`, in: "aaaaa", g: true, want: "(aa)(aa)a"},
		{pat: `a\{-,2}b`, in: "bab", g: true, want: "(b)(ab)"},
		{pat: `\(ab\)\{-1,}`, in: "ababab", want: "(ab)abab"},

		// ref:   pattern.txt */\@>*
		// quote: will not match "aaab", because the "a*" matches the "aaa" (as many "a"s as possible), thus the "ab" can't match.
		{pat: `\(a*\)\@>ab`, in: "aaab", want: "aaab"},
		{pat: `a*ab`, in: "aaab", want: "(aaab)"},
		{pat: `\(a*\)\@>a\|b`, in: "aab", g: true, want: "aa(b)"},
		{pat: `\(a*\)\@>b`, in: "aab", want: "(aab)"},

		// ref:   pattern.txt */\@=*
		// quote: foo\(bar\)\@=foo	nothing
		{pat: `foo\(bar\)\@=`, in: "foobaz foobar", g: true, want: "foobaz (foo)bar"},
		{pat: `foo\(bar\)\@=foo`, in: "foobarfoo", rep: "X", want: "foobarfoo"},
		{pat: `foo\&..`, in: "foobar", want: "(fo)obar"},
		{pat: `\(foo\)\@=..`, in: "foobar", want: "(fo)obar"},

		// ref:   pattern.txt */\@!*
		// quote: a.\{-}p\@!		"a", "ap", "app", "appp", etc. not immediately followed by a "p"
		{pat: `foo\(bar\)\@!`, in: "foobar foobaz", g: true, want: "foobar (foo)baz"},
		{pat: `a.\{-}p\@!`, in: "appq", want: "(app)q"},
		{pat: `a.*p\@!`, in: "xapp", want: "x(app)"},
		{pat: `\(foo\)\@!bar`, in: "foobar", want: "foo(bar)"},
		{pat: `if \(\(then\)\@!.\)*$`, in: "if a then b\nif a else b", rep: "X", want: "if a then b\nX"},
		{pat: `^\%(.*bar\)\@!.*\zsfoo`, in: "x foo bar\ny foo", want: "x foo bar\ny (foo)"},

		// ref:   pattern.txt */\@<=*
		// quote: \(an\_s\+\)\@<=file	"file" after "an" and white space or an end-of-line
		{pat: `\(an\_s\+\)\@<=file`, in: "a file an file", g: true, want: "a file an (file)"},
		{pat: `\(an\_s\+\)\@<=file`, in: "an\nfile", rep: "X", want: "an\nX"},
		{pat: `an\_s\+\zsfile`, in: "a file an file", g: true, want: "a file an (file)"},
		{pat: `\(a\+\)\@<=b`, in: "aab b ab", g: true, want: "aa(b) b a(b)"},
		{pat: `\([a-z]\+\)\zs,\1`, in: "abc,abc", want: "abc(,abc)"},
		{pat: `\%#=1\1\@<=,\([a-z]\+\)`, in: "abc,abc", want: "abc(,abc)"},
		{pat: `\(a\n\)\@<=b`, in: "x\na\nb", rep: "X", want: "x\na\nX"},
		{pat: `\(x\n\n\)\@<=b`, in: "x\n\nb", rep: "X", want: "x\n\nb"},

		// ref:   pattern.txt */\@<=*
		// quote: Like "\@<=" but only look back 123 bytes.
		{pat: `<\@1<=span`, in: "<span x span", g: true, want: "<(span) x span"},
		{pat: `\(<.\)\@1<=span`, in: "<xspan", want: "<xspan"},
		{pat: `\(<.\)\@2<=span`, in: "<xspan", want: "<x(span)"},
		{pat: `\(an\_s\)\@2<=file`, in: "an\nfile", rep: "X", want: "an\nX"},
		{pat: `\(an\_s\)\@1<=file`, in: "an\nfile", rep: "X", want: "an\nfile"},

		// ref:   pattern.txt */\@<!*
		// quote: \(foo\)\@<!bar		any "bar" that's not in "foobar"
		{pat: `\(foo\)\@<!bar`, in: "foobar xbar", g: true, want: "foobar x(bar)"},
		{pat: `\(\/\/.*\)\@<!in`, in: "in // in", g: true, want: "(in) // in"},
		{pat: `\(a\n\)\@<!b`, in: "a\nb\nb", rep: "X", want: "a\nb\nX"},

		// ref:   pattern.txt */multi*
		// quote: |/star|	*	\*	0 or more	as many as possible
		{pat: `a**`, in: "a", want: "error: E871: (NFA regexp) Can't have a multi follow a multi"},
		{pat: `a*\+`, in: "a", want: "error: E871: (NFA regexp) Can't have a multi follow a multi"},
		{pat: `\v(a)@x`, in: "a", want: `error: E869: (NFA) Unknown operator '\@x'`},
		{pat: `\(\)\@<x`, in: "a", want: `error: E869: (NFA) Unknown operator '\@x'`},
		{pat: `a\@`, in: "a", want: `error: E869: (NFA) Unknown operator '\@`},
		{pat: `\+`, in: "a", want: "error: E866: (NFA regexp) Misplaced +"},
		{pat: `\v+`, in: "a", want: "error: E866: (NFA regexp) Misplaced +"},
		{pat: `\M\*`, in: "a", want: "error: E866: (NFA regexp) Misplaced *"},
		{pat: `a\{1`, in: "a", want: "error: E554: Syntax error in \\{...}"},
	})
}

func TestOrdinaryAtoms(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */^*
		// quote: At beginning of pattern or after "\|", "\(", "\%(" or "\n": matches start-of-line; at other positions, matches literal '^'.
		{pat: `^a`, in: "aa", g: true, want: "(a)a"},
		{pat: `a^b`, in: "a^b", want: "(a^b)"},
		{pat: `x\|^a`, in: "abxa", g: true, want: "(a)b(x)a"},
		{pat: `\(^a\)`, in: "aa", g: true, want: "(a)a"},
		{pat: `\%(^a\)`, in: "aa", g: true, want: "(a)a"},
		{pat: `a\n^b`, in: "a\nb", rep: "X", want: "X"},
		{pat: `^beep(`, in: "beep(x) beep(", g: true, want: "(beep()x) beep("},
		{pat: `\c^a`, in: "aa", g: true, want: "(a)a"},
		{pat: `\%1l^a`, in: "aa", g: true, want: "(a)a"},

		// ref:   pattern.txt */\^*
		// quote: Matches literal '^'.  Can be used at any position in the pattern, but not inside [].
		{pat: `\^`, in: "x^y", want: "x(^)y"},

		// ref:   pattern.txt */\_^*
		// quote: \_s*\_^foo	white space and blank lines and then "foo" at start-of-line
		{pat: `a\_s*\_^foo`, in: "a\n\nfoo", rep: "X", want: "X"},
		{pat: `a\_^b`, in: "ab", want: "ab"},

		// ref:   pattern.txt */$*
		// quote: At end of pattern or in front of "\|", "\)" or "\n" ('magic' on): matches end-of-line <EOL>; at other positions, matches literal '$'.
		{pat: `a$`, in: "aa", want: "a(a)"},
		{pat: `a$b`, in: "a$b", want: "(a$b)"},
		{pat: `a$\|b`, in: "aba", g: true, want: "a(b)(a)"},
		{pat: `\(a$\)`, in: "aa", want: "a(a)"},
		{pat: `a$\nb`, in: "a\nb", rep: "X", want: "X"},
		{pat: `a$\c`, in: "aA", want: "a(A)"},
		{pat: `\(\.$\|\. \)`, in: "a. b.", g: true, want: "a(. )b(.)"},

		// ref:   pattern.txt */\$*
		// quote: Matches literal '$'.  Can be used at any position in the pattern, but not inside [].
		{pat: `\$`, in: "x$y", want: "x($)y"},

		// ref:   pattern.txt */\_$*
		// quote: Note that "a\_$b" never matches, since "b" cannot match an end-of-line.
		{pat: `foo\_$\_s*`, in: "foo\n\n  bar", rep: "X", want: "Xbar"},
		{pat: `a\_$b`, in: "a\nb\nab", rep: "X", want: "a\nb\nab"},

		// ref:   pattern.txt */.*
		// quote: Matches any single character, but not an end-of-line.
		{pat: `a.c`, in: "abc a-c ac", g: true, want: "(abc) (a-c) ac"},
		{pat: `b.c`, in: "ab\nc", rep: "X", want: "ab\nc"},
		{pat: `a.c`, in: "aéc", want: "(aéc)"},
		{pat: `漢.`, in: "漢字x", want: "(漢字)x"},

		// ref:   pattern.txt */\_.*
		// quote: Matches any single character or end-of-line.
		{pat: `b\_.c`, in: "ab\nc", rep: "X", want: "aX"},
		{pat: `a\_.b`, in: "a\nb", want: "(a\nb)"},

		// ref:   pattern.txt */\<*
		// quote: Matches the beginning of a word: The next char is the first char of a word.
		{pat: `\<a`, in: "aa ba a", g: true, want: "(a)a ba (a)"},
		{pat: `\<b`, in: "a_b b-b", g: true, want: "a_b (b)-(b)"},
		{pat: `\<a`, in: "漢字abc", want: "漢字(a)bc"},
		{pat: `\<.`, in: "a漢字 b", g: true, want: "(a)(漢)字 (b)"},
		{pat: `\<é`, in: "aé é", g: true, want: "aé (é)"},

		// ref:   pattern.txt */\>*
		// quote: Matches the end of a word: The previous char is the last char of a word.
		{pat: `a\>`, in: "aa ab a", g: true, want: "a(a) ab (a)"},
		{pat: `.\>`, in: "ab漢字 c", g: true, want: "a(b)漢(字) (c)"},
		{pat: `\>`, in: "a b", rep: "|", g: true, want: "a| b|"},

		// ref:   pattern.txt */\zs*
		// quote: Can be used multiple times, the last one encountered in a matching branch is used.
		{pat: `^\s*\zsif`, in: "  if x if", g: true, want: "  (if) x if"},
		{pat: `\(.\{-}\zsFab\)\{3}`, in: "Fab Fab Fab Fab", want: "Fab Fab (Fab) Fab"},
		{pat: `foo\zsbar`, in: "foobar", want: "foo(bar)"},
		{pat: `a\zsb\|\zsc`, in: "abc", g: true, want: "a(b)(c)"},
		{pat: `a\(\zsb\)\@=`, in: "xab", want: "x(a)b"},
		{pat: `\(x\zsa\)\@<=b`, in: "xab", want: "xa(b)"},
		{pat: `\(x\zsa\)\@>b`, in: "xab", want: "(xab)"},
		{pat: `x\zs\&x`, in: "xab", want: "(x)ab"},
		{pat: `\(x\zea\)\@<=b`, in: "xab", want: "xa(b)"},
		{pat: `\(x\)\@>a`, in: "xab", rep: `[&|\1]`, want: "[xa|x]b"},
		{pat: `\(a\)\@<=b`, in: "xab", rep: `[&|\1]`, want: "xa[b|a]"},
		{pat: `\zs*`, in: "a", want: "error: E888: (NFA regexp) cannot repeat \\zs"},

		// ref:   pattern.txt */\ze*
		// quote: Example: "end\ze\(if\|for\)" matches the "end" in "endif" and "endfor".
		{pat: `end\ze\(if\|for\)`, in: "endif endfor endwhile", g: true, want: "(end)if (end)for endwhile"},
		{pat: `a\zeb\zec`, in: "abc", want: "(ab)c"},

		// ref:   pattern.txt */\%^*
		// quote: Matches start of the file.
		{pat: `\%^a`, in: "a\na", rep: "X", want: "X\na"},
		{pat: `\%^\_.\{-}\zsVIM`, in: "x VIM\ny VIM", want: "x (VIM)\ny VIM"},

		// ref:   pattern.txt */\%$*
		// quote: This one will find the last "VIM" in the file: >
		{pat: `a\%$`, in: "a\na", rep: "X", want: "a\nX"},
		{pat: `VIM\ze\(\(VIM\)\@!\_.\)*\%$`, in: "VIM\nVIM x\nVIM y", want: "VIM\nVIM x\n(VIM) y"},
		{pat: `VIM\_.\{-}\%$`, in: "VIM\nVIM x", rep: "X", want: "X"},

		// ref:   pattern.txt */\n*
		// quote: matches an end-of-line
		{pat: `a\nb`, in: "a\nb\nc", rep: "X", want: "X\nc"},
		{pat: `\n`, in: "a\nb", rep: "-", want: "a-b-"},
		{pat: `\_d\+`, in: "a1\n2b", want: "a(1\n2)b(\n)"},
		{pat: `a\n\n`, in: "a\n\nb", rep: "X", want: "Xb"},

		// ref:   pattern.txt */\e*
		// quote: \e	matches <Esc>
		{pat: `\e`, in: "a\x1bb", want: "a(\x1b)b"},
		// ref:   pattern.txt */\t*
		// quote: \t	matches <Tab>
		{pat: `\t`, in: "a\tb", rep: "T", want: "aTb"},
		// ref:   pattern.txt */\r*
		// quote: \r	matches <CR>
		{pat: `\r`, in: "a\rb", want: "a(\r)b"},
		// ref:   pattern.txt */\b*
		// quote: \b	matches <BS>
		{pat: `\b`, in: "a\bb", want: "a(\b)b"},

		// ref:   pattern.txt */~*
		// quote: ~	matches the last given substitute string
		{pat: `x~y`, in: "xaby", last: "ab", want: "(xaby)"},
		{pat: `~`, in: "a~b", rep: `(\&)`, nomagic: true, want: "a(~)b"},

		// ref:   pattern.txt */\(\)*
		// quote: E.g., "\(^a\)" matches 'a' at the start of a line.
		{pat: `\([abc]\)\([efg]\)`, in: "af fa bg", rep: `\2\1`, g: true, want: "fa fa gb"},
		{pat: `\(a\)\(b\)\(c\)\(d\)\(e\)\(f\)\(g\)\(h\)\(i\)`, in: "abcdefghi", rep: `\9\8\1`, want: "iha"},
		{pat: `\(a\)\(b\)\(c\)\(d\)\(e\)\(f\)\(g\)\(h\)\(i\)\(j\)`, in: "a", want: "error: E872: (NFA regexp) Too many '('"},
		{pat: `\(a`, in: "a", want: "error: E54: Unmatched \\("},
		{pat: `\v(a`, in: "a", want: "error: E54: Unmatched ("},
		{pat: `\%(a`, in: "a", want: "error: E53: Unmatched \\%("},
		{pat: `a\)`, in: "a", want: "error: E55: Unmatched \\)"},

		// ref:   pattern.txt */\1*
		// quote: Example: "\([a-z]\).\1" matches "ata", "ehe", "tot", etc.
		{pat: `\([a-z]\).\1`, in: "ata ehe tot abc", g: true, want: "(ata) (ehe) (tot) abc"},
		{pat: `\(a\)\(b\)-\2\1`, in: "ab-ba ab-ab", g: true, want: "(ab-ba) ab-ab"},
		{pat: `\(a\nb\)-\1`, in: "a\nb-a\nb", rep: "X", want: "X"},
		{pat: `\(x\)\=y\1`, in: "y", want: "(y)"},
		{pat: `\1\(a\)`, in: "a", want: "error: E65: Illegal back reference"},

		// ref:   pattern.txt */\9*
		// quote: Note: The numbering of groups is done based on which "\(" comes first in the pattern (going left to right), NOT based on what is matched first.
		{pat: `\(\(a\)b\)`, in: "ab", rep: `\2-\1`, want: "a-ab"},

		// ref:   pattern.txt */\%(\)*
		// quote: Just like \(\), but without counting it as a sub-expression.
		{pat: `\%(a\)\(b\)`, in: "ab", rep: `\1`, want: "b"},

		// ref:   pattern.txt */\\*
		// quote: A backslash followed by a single character, with no special meaning, is reserved for future expansions
		{pat: `a\#b`, in: "a#b", want: "(a#b)"},
		{pat: `\/`, in: "a/b", want: "a(/)b"},
		{pat: `\é`, in: "é éé x", g: true, want: "é (éé) x"},
	})
}

func TestPositionAtoms(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */\%V*
		// quote: Match inside the Visual area.
		{pat: `\%Va`, in: "aaaaa", vis: &Area{Pos{0, 1}, Pos{0, 2}, 'v'}, rep: "X", g: true, want: "aXXaa"},
		{pat: `\%Vfoo.*ba\%Vr`, in: "foo bar foo bar", vis: &Area{Pos{0, 0}, Pos{0, 6}, 'v'}, want: "(foo bar) foo bar"},
		{pat: `\%Vfoo.*bar\%V`, in: "foo bar foo bar", vis: &Area{Pos{0, 0}, Pos{0, 6}, 'v'}, want: "foo bar foo bar"},
		{pat: `\%Va`, in: "aaaa\naaaa", vis: &Area{Pos{0, 1}, Pos{1, 2}, 0x16}, rep: "X", g: true, want: "aXXa\naXXa"},
		{pat: `\%Va`, in: "aaaa\naaaa\naaaa", vis: &Area{Pos{1, 3}, Pos{0, 2}, 'v'}, rep: "X", g: true, want: "aaXX\nXXXX\naaaa"},
		{pat: `\%Va`, in: "aaaa\naaaa\naaaa", vis: &Area{Pos{1, 3}, Pos{0, 2}, 'V'}, rep: "X", g: true, want: "XXXX\nXXXX\naaaa"},
		{pat: `\%Va`, in: "\ta\na\ta", vis: &Area{Pos{0, 1}, Pos{1, 2}, 0x16}, rep: "X", g: true, want: "\tX\na\tX"},

		// ref:   pattern.txt */\%#*
		// quote: Matches with the cursor position.
		{pat: `.\%#.`, in: "x ab ab", cur: &Pos{0, 3}, line: 1, want: "x (ab) ab"},
		{pat: `\k*\%#\k*`, in: "foo bar baz", cur: &Pos{0, 5}, line: 1, want: "foo (bar) baz"},

		// ref:   pattern.txt */\%'m*
		// quote: Note that two dots are required to include mark 'e in the match.
		{pat: `.\%>'s.*\%<'e..`, in: "abcdefgh", marks: map[rune]Pos{'s': {0, 2}, 'e': {0, 5}}, want: "ab(cdef)gh"},
		{pat: `\%'a.`, in: "abcd", marks: map[rune]Pos{'a': {0, 2}}, want: "ab(c)d"},
		{pat: `\%>'a.`, in: "abcd", marks: map[rune]Pos{'a': {0, 2}}, g: true, want: "abc(d)"},
		{pat: `\%<'a.`, in: "abcd\nab", marks: map[rune]Pos{'a': {0, 2}}, g: true, want: "(a)(b)cd\nab"},
		{pat: `\%'b.`, in: "abcd", want: "abcd"},

		// ref:   pattern.txt */\%l*
		// quote: These six can be used to match specific lines in a buffer.
		{pat: `\%2la`, in: "a\na\na", rep: "X", want: "a\nX\na"},
		{pat: `\%<3la`, in: "a\na\na", rep: "X", want: "X\nX\na"},
		{pat: `\%>1la`, in: "a\na\na", rep: "X", want: "a\nX\nX"},
		{pat: `\%>1l\%<3llimit`, in: "limit\nlimit\nlimit", rep: "X", want: "limit\nX\nlimit"},
		{pat: `\%.la`, in: "a\nb a\na", cur: &Pos{1, 0}, rep: "X", want: "a\nb X\na"},
		{pat: `\%>.la`, in: "a\na\na", cur: &Pos{1, 0}, rep: "X", want: "a\na\nX"},
		{pat: `\%.2l`, in: "a", want: `error: E1204: No Number allowed after .: '\%2'`},

		// ref:   pattern.txt */\%c*
		// quote: Note that "\%<46c" matches in column 45 when the "." matches a byte in column 44.
		{pat: `\%3c.`, in: "abcd", want: "ab(c)d"},
		{pat: `\%3c.`, in: "éab", want: "é(a)b"},
		{pat: `\%>2c.\%<5c`, in: "abcdef", g: true, want: "ab(c)def"},
		{pat: `\%>2c.`, in: "abcd", g: true, want: "ab(c)(d)"},
		{pat: `\%.c.`, in: "abcd", cur: &Pos{0, 1}, line: 1, want: "a(b)cd"},

		// ref:   pattern.txt */\%v*
		// quote: Note that some virtual column positions will never match, because they are halfway through a tab or other character that occupies more than one screen character.
		{pat: `\%9v.`, in: "\tab", want: "\t(a)b"},
		{pat: `\%3v.`, in: "\tab", want: "\tab"},
		{pat: `^.*\%4v`, in: "abcdefg", want: "(abc)defg"},
		{pat: `^.*\%4v.`, in: "abcdefg", want: "(abcd)efg"},
		{pat: `^.*\%<5v.`, in: "ab", want: "(ab)"},
		{pat: `\%>3v.*`, in: "abcdef", want: "abc(def)"},
		{pat: `\%.v.`, in: "a\tbc", cur: &Pos{0, 2}, line: 1, want: "a\t(b)c"},
		{pat: `\%5v.`, in: "漢字ab", want: "漢字(a)b"},
	})
}

func TestCharacterClasses(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */character-classes*
		// quote: |/\_|	\_x	\_x	where x is any of the characters above: character class with end-of-line included
		{pat: `\s`, in: "a b\tc", rep: "_", g: true, want: "a_b_c"},
		{pat: `\S\+`, in: "ab cd", g: true, want: "(ab) (cd)"},
		{pat: `\S\+`, in: "漢字 かな", g: true, want: "(漢字) (かな)"},
		{pat: `\d\+`, in: "a12b3", g: true, want: "a(12)b(3)"},
		{pat: `\D`, in: "a12b3", g: true, want: "(a)12(b)3"},
		{pat: `\x\+`, in: "0xBEEFg", g: true, want: "(0)x(BEEF)g"},
		{pat: `\X`, in: "a9g", g: true, want: "a9(g)"},
		{pat: `\o\+`, in: "0789", g: true, want: "(07)89"},
		{pat: `\O`, in: "0789", g: true, want: "07(8)(9)"},
		{pat: `\w\+`, in: "a_1-b", g: true, want: "(a_1)-(b)"},
		{pat: `\w\+`, in: "caféx", g: true, want: "(caf)é(x)"},
		{pat: `\W`, in: "a-b c", g: true, want: "a(-)b( )c"},
		{pat: `\h`, in: "_a1", g: true, want: "(_)(a)1"},
		{pat: `\H`, in: "_a1-", g: true, want: "_a(1)(-)"},
		{pat: `\a`, in: "ab1_C", g: true, want: "(a)(b)1_(C)"},
		{pat: `\a`, in: "éa", g: true, want: "é(a)"},
		{pat: `\A`, in: "ab1_C", g: true, want: "ab(1)(_)C"},
		{pat: `\l`, in: "aBcDé", g: true, want: "(a)B(c)Dé"},
		{pat: `\L`, in: "aB1", g: true, want: "a(B)(1)"},
		{pat: `\u`, in: "aBcDÉ", g: true, want: "a(B)c(D)É"},
		{pat: `\U`, in: "aB1", g: true, want: "(a)B(1)"},
		{pat: `\<\h\w*`, in: "9ab_1 x", g: true, want: "9ab_1 (x)"},
		{pat: `\_d\+`, in: "a1\n2b", want: "a(1\n2)b(\n)"},
		{pat: `a\_s\+b`, in: "a\n  b", rep: "X", want: "X"},
		{pat: `a\_x`, in: "a\nb", rep: "X", want: "Xb"},
		{pat: `\_a\+`, in: "ab\ncd", want: "(ab\ncd\n)"},

		// ref:   pattern.txt */\k*
		// quote: \k	keyword character (see 'iskeyword' option)
		{pat: `\k\+`, in: "café-x", g: true, want: "(café)-(x)"},
		{pat: `\k\+`, in: "漢字、かな", g: true, want: "(漢字)、(かな)"},
		{pat: `\K`, in: "a1b", g: true, want: "(a)1(b)"},
		// ref:   pattern.txt */\i*
		// quote: \i	identifier character (see 'isident' option)
		{pat: `\i\+`, in: "ab_1.cé漢", g: true, want: "(ab_1).(cé)漢"},
		{pat: `\I`, in: "a1b", g: true, want: "(a)1(b)"},
		// ref:   pattern.txt */\f*
		// quote: \f	file name character (see 'isfname' option)
		{pat: `\f\+`, in: "see /tmp/a.txt, ok", g: true, want: "(see) (/tmp/a.txt,) (ok)"},
		{pat: `\F`, in: "a1b", g: true, want: "(a)1(b)"},
		// ref:   pattern.txt */\p*
		// quote: \p	printable character (see 'isprint' option)
		{pat: `\p\+`, in: "a b\x01c", g: true, want: "(a b)\x01(c)"},
		{pat: `\P`, in: "a1b", g: true, want: "(a)1(b)"},

		// ref:   pattern.txt */\s*
		// quote: NOTE: 'ignorecase', "\c" and "\C" are not used by character classes.
		{pat: `\c\u`, in: "aB", g: true, want: "a(B)"},
		{pat: `\u`, in: "aB", ic: true, g: true, want: "a(B)"},
	})
}

func TestCollections(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */[]*
		// quote: A collection.  This is a sequence of characters enclosed in square brackets.  It matches any single character in the collection.
		{pat: `[xyz]`, in: "xaybz", g: true, want: "(x)a(y)b(z)"},
		{pat: `[^xyz]`, in: "xaybz", g: true, want: "x(a)y(b)z"},
		{pat: `[a-z0-9]\+`, in: "a1B2c", g: true, want: "(a1)B(2c)"},
		{pat: `[a-zA-Z]$`, in: "ab1 cD", want: "ab1 c(D)"},
		{pat: `\c[a-z]$`, in: "ab1 cD", want: "ab1 c(D)"},
		{pat: `[А-яЁё]\+`, in: "Ёж z", want: "(Ёж) z"},
		{pat: `[à-ÿ]\+`, in: "aéèz", want: "a(éè)z"},
		{pat: `[^xy]`, in: "x漢y", g: true, want: "x(漢)y"},
		{pat: `[c-a]`, in: "a", want: "error: E944: Reverse range in character class"},

		// ref:   pattern.txt */[\n]*
		// quote: Thus "\_[^ab]" matches the end-of-line and any character but "a" and "b".
		{pat: `a[^x]b`, in: "a\nb", rep: "X", want: "a\nb"},
		{pat: `a[\n]b`, in: "a\nb", rep: "X", want: "X"},
		{pat: `a\_[xyz]b`, in: "a\nb", rep: "X", want: "X"},
		{pat: `x\_[^ab]y`, in: "x\ny x-y", rep: "Z", g: true, want: "Z Z"},
		{pat: `a[^\n]b`, in: "a\nb axb", rep: "X", want: "a\nb X"},

		// ref:   pattern.txt *E769*
		// quote: When the ']' is not there Vim will not give an error message but assume no collection is used.
		{pat: `a[b`, in: "x a[b", search: true, want: "1:3"},
		{pat: `a\_[b`, in: "x a[b", search: true, want: "1:3"},

		// ref:   pattern.txt *[:alpha:]*
		// quote: [:alpha:]   isalpha	ASCII letters
		{pat: `[[:alpha:]]\+`, in: "ab1cé", g: true, want: "(ab)1(c)é"},
		{pat: `[[:digit:]]\+`, in: "ab12c", g: true, want: "ab(12)c"},
		{pat: `[[:alnum:]]\+`, in: "a1-b2", g: true, want: "(a1)-(b2)"},
		{pat: `[[:space:]]`, in: "a b\tc", rep: "_", g: true, want: "a_b_c"},
		{pat: `[[:blank:]]`, in: "a b\tc", rep: "_", g: true, want: "a_b_c"},
		{pat: `[[:upper:]]`, in: "aBcD", g: true, want: "a(B)c(D)"},
		{pat: `[[:lower:]]`, in: "ÉéAa", g: true, want: "É(é)A(a)"},
		{pat: `[[:upper:]]`, in: "ÉéAa", g: true, want: "(É)é(A)a"},
		{pat: `\c[[:upper:]]`, in: "aB", g: true, want: "a(B)"},
		{pat: `[[:punct:]]`, in: "a,b!c", g: true, want: "a(,)b(!)c"},
		{pat: `[[:xdigit:]]`, in: "fg0", g: true, want: "(f)g(0)"},
		{pat: `[[:keyword:]]\+`, in: "café-x", g: true, want: "(café)-(x)"},
		{pat: `[[:cntrl:]]`, in: "a\x01b", g: true, want: "a(\x01)b"},
		{pat: `[[:graph:]]\+`, in: "a b", g: true, want: "(a) (b)"},
		{pat: `[[:print:]]\+`, in: "a b\x01", g: true, want: "(a b)\x01"},
		{pat: `[[:tab:]]`, in: "a\tb", rep: "T", want: "aTb"},
		{pat: `[[:ident:][:fname:]]\+`, in: "a/b c", g: true, want: "(a/b) (c)"},
		{pat: `[-./[:alnum:]_~]\+`, in: "see ./a-b_c~ ok", g: true, want: "(see) (./a-b_c~) (ok)"},

		// ref:   pattern.txt */[[=*
		// quote: An equivalence class.  This means that characters are matched that have almost the same meaning, e.g., when ignoring accents.
		{pat: `[[=e=]]`, in: "eéèxE", g: true, want: "(e)(é)(è)xE"},
		{pat: `[[=é=]]`, in: "eéèx", g: true, want: "(e)(é)(è)x"},
		{pat: `[[=x=]]`, in: "xy", g: true, want: "(x)y"},

		// ref:   pattern.txt */[[.*
		// quote: A collation element.  This currently simply accepts a single character in the form:
		{pat: `[[.a.]]`, in: "bab", g: true, want: "b(a)b"},

		// ref:   pattern.txt */\]*
		// quote: To include a literal ']', '^', '-' or '\' in the collection, put a backslash before it: "[xyz\]]", "[\^xyz]", "[xy\-z]" and "[xyz\\]".
		{pat: `[]b]`, in: "a]b", g: true, want: "a(])(b)"},
		{pat: `[^]b]`, in: "a]b", g: true, want: "(a)]b"},
		{pat: `[x\]]`, in: "a]b", g: true, want: "a(])b"},
		{pat: `[x-]`, in: "a-b", g: true, want: "a(-)b"},
		{pat: `[-x]`, in: "a-b", g: true, want: "a(-)b"},
		{pat: `[xy\-z]`, in: "a-y", g: true, want: "a(-)(y)"},
		{pat: `[\^b]`, in: "a^b", g: true, want: "a(^)(b)"},
		{pat: `[\\]`, in: `a\b`, g: true, want: `a(\)b`},
		{pat: `[\xyz]`, in: `a\xyz`, g: true, want: `a(\)(x)(y)(z)`},
		{pat: `[]`, in: "a]", search: true, want: "0:0"},
		{pat: `[]`, in: "a[]", search: true, want: "1:2"},

		// ref:   pattern.txt */\]*
		// quote: \d123	decimal number of character
		{pat: `[\t]`, in: "a\tb", rep: "T", g: true, want: "aTb"},
		{pat: `[\e]`, in: "a\x1bb", g: true, want: "a(\x1b)b"},
		{pat: `[\d97]`, in: "bab", g: true, want: "b(a)b"},
		{pat: `[\o141]`, in: "bab", g: true, want: "b(a)b"},
		{pat: `[\x61]`, in: "bab", g: true, want: "b(a)b"},
		{pat: `[€$]`, in: "5€ 6$", g: true, want: "5(€) 6($)"},
		{pat: `[\d97-\d99]\+`, in: "xabcd", want: "x(abc)d"},
	})
}

func TestOptionalSequence(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */\%[]*
		// quote: It matches as much of the list of atoms it contains as possible.
		{pat: `r\%[ead]`, in: "rex", want: "(re)x"},
		{pat: `r\%[ead]`, in: "read", want: "(read)"},
		{pat: `\<r\%[ead]\>`, in: "r read rx", g: true, want: "(r) (read) rx"},
		{pat: `\<fu\%[nction]\>`, in: "fu func function full", g: true, want: "(fu) (func) (function) full"},
		{pat: `\<r\%[[eo]ad]\>`, in: "road rod read", g: true, want: "(road) rod (read)"},
		{pat: `index\%[[[]0[]]]`, in: "index[0] index[1]", g: true, want: "(index[0]) (index[)1]"},
		{pat: `a\%[]`, in: "a", want: "error: E70: Empty \\%[]"},
		{pat: `a\%[bc`, in: "a", want: "error: E69: Missing ] after \\%["},
		{pat: `a\%[\(b\)c]`, in: "abc ab a", g: true, want: "(abc) (ab) (a)"},
		{pat: `a\%[b\|c]`, in: "a", want: "error: E866: (NFA regexp) Misplaced |"},
	})
}

func TestNumberedCharacters(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */\%d*
		// quote: \%d123	Matches the character specified with a decimal number.  Must be followed by a non-digit.
		{pat: `\%d97`, in: "bab", want: "b(a)b"},
		{pat: `\%x41`, in: "zAz", want: "z(A)z"},
		{pat: `\%x2a`, in: "a*b", want: "a(*)b"},
		{pat: `\%o101`, in: "zAz", want: "z(A)z"},
		{pat: `\%o40`, in: "a b", want: "a( )b"},
		{pat: `\%u00e9`, in: "cafe café", want: "cafe caf(é)"},
		{pat: `\%u20AC`, in: "5€", want: "5(€)"},
		{pat: `\%u6f22`, in: "a漢b", want: "a(漢)b"},
		{pat: `\%U1F600`, in: "a😀b", want: "a(😀)b"},
		{pat: `\%x`, in: "a", want: "error: E678: Invalid character after \\%[dxouU]"},
		{pat: `\%q`, in: "a", want: `error: E867: (NFA) Unknown operator '\%q'`},
		{pat: `\%2x`, in: "a", want: `error: E867: (NFA) Unknown operator '\%x'`},
	})
}

func TestIgnoreCase(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */\c*
		// quote: When "\c" appears anywhere in the pattern, the whole pattern is handled like 'ignorecase' is on.
		{pat: `foo`, in: "foo Foo FOO", g: true, want: "(foo) Foo FOO"},
		{pat: `foo`, in: "foo Foo FOO", ic: true, g: true, want: "(foo) (Foo) (FOO)"},
		{pat: `Foo`, in: "foo Foo FOO", ic: true, g: true, want: "(foo) (Foo) (FOO)"},
		{pat: `Foo`, in: "foo Foo FOO", ic: true, scs: true, g: true, want: "foo (Foo) FOO"},
		{pat: `foo`, in: "foo Foo FOO", ic: true, scs: true, g: true, want: "(foo) (Foo) (FOO)"},
		{pat: `\cfoo`, in: "foo Foo FOO", g: true, want: "(foo) (Foo) (FOO)"},
		{pat: `foo\C`, in: "foo Foo FOO", ic: true, g: true, want: "(foo) Foo FOO"},
		{pat: `fo\co`, in: "FOO", g: true, want: "(FOO)"},
		{pat: `\cé`, in: "éÉ", g: true, want: "(é)(É)"},
		{pat: `\c\(a\)\1`, in: "aA", want: "(aA)"},
		{pat: `\Sx`, in: "ax AX", ic: true, scs: true, g: true, want: "(ax) (AX)"},
		{pat: `É`, in: "é É", ic: true, scs: true, g: true, want: "é (É)"},
		{pat: `\c[^a]`, in: "Ab", g: true, want: "A(b)"},
		{pat: `k`, in: "K", ic: true, want: "(K)"},
	})
}

func TestComposing(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */\Z*
		// quote: When "\Z" appears anywhere in the pattern, all composing characters are ignored.
		{pat: `cat\Z`, in: "cat càt", g: true, want: "(cat) (càt)"},
		{pat: `cat`, in: "cat càt", g: true, want: "(cat) càt"},
		// ref:   pattern.txt */\%C*
		// quote: Use "\%C" to skip any composing characters.
		{pat: `a\%C`, in: "càt", want: "c(à)t"},
		{pat: `a\%C`, in: "cát", want: "cát"},
		{pat: `a\%Ct`, in: "cat", want: "c(at)"},
		{pat: `c.t`, in: "càt", want: "(càt)"},
		{pat: `à`, in: "à́ a", g: true, want: "(à́) a"},
	})
}

func TestExpand(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   change.txt *s/\\*
		// quote: :s/a\|b/xxx\0xxx/g		 modifies "a b"	     to "xxxaxxx xxxbxxx"
		{pat: `a\|b`, in: "a b", rep: `xxx\0xxx`, g: true, want: "xxxaxxx xxxbxxx"},
		{pat: `\([abc]\)\([efg]\)`, in: "af fa bg", rep: `\2\1`, g: true, want: "fa fa gb"},
		{pat: `abcde`, in: "abcde", rep: "abc\rde", want: "abc\nde"},
		{pat: `$`, in: "abcde", rep: "\\\r", want: "abcde\r"},
		{pat: `\w\+`, in: "bla bla", rep: `\u\0`, g: true, want: "Bla Bla"},
		{pat: `\w\+`, in: "BLA bla", rep: `\L\u\0`, g: true, want: "Bla Bla"},
		{pat: `aa`, in: "aa", rep: "a\ra", want: "a\na"},
		{pat: `aa`, in: "aa", rep: "a\\\ra", want: "a\ra"},
		{pat: `aa`, in: "aa", rep: "a\\\\\ra", want: "a\\\na"},
		{pat: `\(\(a[a-d] \)*\)`, in: "aa ab x", rep: `\2`, want: "ab x"},
		{pat: `\([ab]\)\|\([cd]\)`, in: "a b c d", rep: `\1x`, g: true, want: "ax bx x x"},

		// ref:   change.txt *s/\&*
		// quote: replaced with the whole matched pattern
		{pat: `b`, in: "abc", rep: `[&]`, want: "a[b]c"},
		{pat: `b`, in: "abc", rep: `[\&]`, want: "a[&]c"},
		{pat: `b`, in: "abc", rep: `[&]`, nomagic: true, want: "a[&]c"},
		{pat: `b`, in: "abc", rep: `[\&]`, nomagic: true, want: "a[b]c"},
		// ref:   change.txt *s/\~*
		// quote: \~	   ~	  replaced with ~
		{pat: `b`, in: "abc", rep: `[~]`, last: "XY", want: "a[XY]c"},
		{pat: `b`, in: "abc", rep: `[\~]`, last: "XY", want: "a[~]c"},
		{pat: `b`, in: "abc", rep: `[\~]`, last: "XY", nomagic: true, want: "a[XY]c"},
		{pat: `b`, in: "abc", rep: `[~]`, last: "", want: "a[]c"},
		// ref:   change.txt *s/\U*
		// quote: following characters made uppercase, until \E
		{pat: `\w\+`, in: "bla bla", rep: `\Ux\0\Ey\0`, want: "XBLAybla bla"},
		{pat: `\w\+`, in: "BLA", rep: `\l\0`, want: "bLA"},
		{pat: `\w\+`, in: "bla", rep: `\U\l\0`, want: "bLA"},
		{pat: `\w\+`, in: "BLA", rep: `\L\0\e\0`, want: "blaBLA"},
		{pat: `\S\+`, in: "éa", rep: `\U\0`, want: "ÉA"},
		// ref:   change.txt *s/\n*
		// quote: (does NOT break the line)
		{pat: `b`, in: "abc", rep: `\n`, want: "a\x00c"},
		{pat: `b`, in: "abc", rep: `\t\\\b`, want: "a\t\\\bc"},
		{pat: `b`, in: "abc", rep: `\r`, want: "a\nc"},
		{pat: `b`, in: "abc", rep: `\q`, want: "aqc"},
		{pat: `a\nb`, in: "a\nb", rep: `[&]`, want: "[a\nb]"},
		{pat: `a\nb`, in: "a\nb", rep: `\U&`, want: "A\nB"},
	})
}

func TestErrors(t *testing.T) {
	checkSubs(t, []sc{
		// ref:   pattern.txt */\_*
		// quote: \_x	Where "x" is any of the characters above: The character class with end-of-line added
		{pat: `\_q`, in: "a", want: "error: E877: (NFA regexp) Invalid character class: 113"},
		{pat: `\_`, in: "a", want: "error: E865: (NFA) Regexp end encountered prematurely"},
		// ref:   pattern.txt */\zs*
		// quote: \zs	Matches at any position, but not inside [], and sets the start of the match there: The next char is the first char of the whole match.
		{pat: `\zq`, in: "a", want: `error: E867: (NFA) Unknown operator '\zq'`},
		{pat: `\z`, in: "a", want: `error: E867: (NFA) Unknown operator '\z`},
		{pat: `\%`, in: "a", want: `error: E867: (NFA) Unknown operator '\%`},
		{pat: `\%<`, in: "a", want: `error: E867: (NFA) Unknown operator '\%`},
		{pat: `\z(a\)`, in: "a", want: `error: E66: \z( not allowed here`},
		{pat: `\z1`, in: "a", want: `error: E67: \z1 - \z9 not allowed here`},
		// ref:   pattern.txt *E1281*
		// quote: prepend one of the following to the pattern:
		{pat: `\%#=1a`, in: "a", want: "(a)"},
		{pat: `\%#=2a`, in: "a", want: "(a)"},
		{pat: `\%#=xa`, in: "a", want: `error: E864: \%#= can only be followed by 0, 1, or 2. The automatic engine will be used `},
		{pat: `a\%#=1`, in: "a", want: `error: E1281: Atom '\%#=1' must be at the start of the pattern`},
	})
}

func TestMatchAPI(t *testing.T) {
	// ref:   pattern.txt */\(\)*
	// quote: A pattern enclosed by escaped parentheses.
	re, err := Compile(`\(a\)\|\(b\)`, Options{Magic: true})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := re.Exec(buf{"xb"}, 0, 0, nil)
	if !ok || m.Start != (Pos{0, 1}) || m.End != (Pos{0, 2}) || m.Groups[1].Set || !m.Groups[2].Set {
		t.Errorf("Exec = %+v, %v", m, ok)
	}
	if _, ok := re.MatchAt(buf{"xb"}, 0, 0, nil); ok {
		t.Error("MatchAt at x matched")
	}
	if m, ok := re.MatchAt(buf{"xb"}, 0, 1, nil); !ok || m.End != (Pos{0, 2}) {
		t.Errorf("MatchAt = %+v, %v", m, ok)
	}

	// ref:   pattern.txt */\n*
	// quote: matches an end-of-line
	re, _ = Compile(`b\n`, Options{Magic: true})
	if m, ok := re.Exec(buf{"a", "b"}, 1, 0, nil); !ok || m.End != (Pos{2, 0}) {
		t.Errorf("b\\n at the last line = %+v, %v", m, ok)
	}
	re, _ = Compile(`\n\n`, Options{Magic: true})
	if _, ok := re.Exec(buf{"a", "b"}, 1, 0, nil); ok {
		t.Error(`\n\n matched past the end of the text`)
	}
}

func TestBudget(t *testing.T) {
	re, err := Compile(`\(a*\)*b`, Options{Magic: true})
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := re.ExecErr(buf{strings.Repeat("a", 60)}, 0, 0, nil)
	if ok || !errors.Is(err, ErrTooComplex) {
		t.Errorf("ExecErr = %v, %v; want ErrTooComplex", ok, err)
	}
	re, _ = Compile(`\(a\)*`, Options{Magic: true})
	_, ok, err = re.ExecErr(buf{strings.Repeat("a", 200000)}, 0, 0, nil)
	if ok || !errors.Is(err, ErrTooComplex) {
		t.Errorf("deep ExecErr = %v, %v; want ErrTooComplex", ok, err)
	}
	re, _ = Compile(`\_.*x`, Options{Magic: true})
	if _, ok, err := re.ExecErr(lines(strings.Repeat("abc\n", 20000)+"x"), 0, 0, nil); !ok || err != nil {
		t.Errorf(`\_.*x over many lines = %v, %v`, ok, err)
	}
}
