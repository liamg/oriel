package vimregex

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

var fuzzTokens = []string{
	"a", "b", "ab", "é", "漢", " ", ".", `\_.`, "*", `\+`, `\=`, `\?`, `\{-}`, `\{1,3}`, `\{-1,}`, `\{2}`,
	`\(`, `\)`, `\%(`, `\|`, `\&`, "^", "$", `\_^`, `\_$`, `\n`, "[ab]", "[^a]", `\_[b ]`, "[[:alpha:]]", "[a-é]",
	`\zs`, `\ze`, `\<`, `\>`, `\@=`, `\@!`, `\@<=`, `\@<!`, `\@1<=`, `\@>`, `\1`, `\2`, `\%[ab]`, `\v`, `\m`,
	`\M`, `\V`, `\c`, `\C`, "~", `\%V`, `\%#`, `\%2l`, `\%<2l`, `\%3c`, `\%>2v`, `\%'a`, `\k`, `\s`, `\S`, `\d`,
	`\w`, `\a`, `\u`, `\%^`, `\%$`, `\%d97`, `\%x62`, "(", ")", "|", "{", "}", "+", "=", "@", "%", "<", ">",
	`\`, "[", "]", `\%C`, `\Z`, "\t",
}

func randPattern(r *rand.Rand) string {
	var b strings.Builder
	for n := 1 + r.Intn(10); n > 0; n-- {
		b.WriteString(fuzzTokens[r.Intn(len(fuzzTokens))])
	}
	return b.String()
}

func randText(r *rand.Rand) string {
	const chars = "aab bé漢\t*~"
	rs := []rune(chars)
	var b strings.Builder
	for n := r.Intn(14); n > 0; n-- {
		if r.Intn(6) == 0 {
			b.WriteByte('\n')
			continue
		}
		b.WriteRune(rs[r.Intn(len(rs))])
	}
	return b.String()
}

// exercise compiles a pattern and runs it everywhere in the text, checking every result is in range.
func exercise(t *testing.T, pat, text string) {
	re, err := Compile(pat, Options{Magic: true, LastSub: "b"})
	if err != nil {
		return
	}
	b := lines(text)
	st := &State{Cursor: Pos{0, 1}, Visual: &Area{Pos{0, 1}, Pos{len(b) - 1, 0}, 'v'}, VCol: b.vcol,
		Mark: func(r rune) (Pos, bool) { return Pos{0, 2}, r == 'a' }}
	inRange := func(p Pos) bool {
		return p.Line >= 0 && p.Line < len(b) && p.Col >= 0 && p.Col <= len(b[p.Line]) || p == Pos{len(b), 0}
	}
	for l := range b {
		for c := 0; c <= len(b[l]); c++ {
			for _, run := range []func(Input, int, int, *State) (Match, bool, error){re.ExecErr, re.MatchAtErr} {
				m, ok, err := run(b, l, c, st)
				if err != nil && !errors.Is(err, ErrTooComplex) {
					t.Fatalf("%q on %q: %v", pat, text, err)
				}
				if !ok {
					continue
				}
				for _, g := range m.Groups {
					if g.Set && (!inRange(g.Start) || !inRange(g.End)) {
						t.Fatalf("%q on %q at %d:%d: group out of range: %+v", pat, text, l, c, m)
					}
				}
			}
		}
	}
}

func TestRandomPatterns(t *testing.T) {
	defer func(s, d int) { maxSteps, maxDepth = s, d }(maxSteps, maxDepth)
	maxSteps, maxDepth = 20_000, 2_000
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		exercise(t, randPattern(r), randText(r))
	}
}

func FuzzRegex(f *testing.F) {
	for _, s := range []string{`a*`, `\(a\|b\)\{-1,}\1`, `\v(a+)+b`, `\_.*\%$`, `\(a\n\)\@<!b`, `[[:alpha:]]\zs.`, `\%[ab]\>`} {
		f.Add(s, "aab\nba é\n\tb")
	}
	f.Fuzz(func(t *testing.T, pat, text string) {
		defer func(s, d int) { maxSteps, maxDepth = s, d }(maxSteps, maxDepth)
		maxSteps, maxDepth = 20_000, 2_000
		if len(text) > 200 {
			text = text[:200]
		}
		exercise(t, pat, text)
	})
}
