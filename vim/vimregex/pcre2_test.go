package vimregex

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// pcreCase is one pattern of a pcre2test output file, its subjects, and what pcre2test printed for each.
type pcreCase struct {
	file     string
	line     int
	pattern  string
	mods     []string
	compile  string // "Failed: …" when the pattern doesn't compile
	subjects []pcreSubject
	skip     string // why it isn't run: a modifier or directive this harness or matcher doesn't have
}

type pcreSubject struct {
	text string
	mods string // \= subject modifiers
	want []string
	line int
}

// fromLatin1 is the subject or pattern as pcre2test's 8-bit, non-UTF mode reads it: a byte per character.
func fromLatin1(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		s.WriteRune(rune(c))
	}
	return s.String()
}

// subjectEscapes undoes pcre2test's escapes in a subject line.
func subjectEscapes(s string) (string, bool) {
	// \[text]{n} is text n times.
	for {
		i := strings.Index(s, `\[`)
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "]{")
		k := strings.IndexByte(s[i+max(j, 0):], '}')
		if j < 0 || k < 0 {
			return "", false
		}
		n, err := strconv.Atoi(s[i+j+2 : i+j+k])
		if err != nil {
			return "", false
		}
		s = s[:i] + strings.Repeat(s[i+2:i+j], n) + s[i+j+k+1:]
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 == len(s) {
			break // a backslash ending the line stands for nothing
		}
		if c != '\\' {
			b = append(b, c)
			continue
		}
		i++
		switch c = s[i]; c {
		case 'a':
			b = append(b, 7)
		case 'b':
			b = append(b, 8)
		case 'e':
			b = append(b, 0x1b)
		case 'f':
			b = append(b, '\f')
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'v':
			b = append(b, '\v')
		case 'x':
			if i+1 < len(s) && s[i+1] == '{' {
				end := strings.IndexByte(s[i:], '}')
				v, err := strconv.ParseUint(s[i+2:i+end], 16, 32)
				if err != nil || v > 0xff {
					return "", false
				}
				b = append(b, byte(v))
				i += end
				continue
			}
			v, n := 0, 0
			for n < 2 && i+1 < len(s) && isHexByte(s[i+1]) {
				v = v*16 + hexVal(s[i+1])
				i++
				n++
			}
			b = append(b, byte(v))
		case 'o':
			end := strings.IndexByte(s[i:], '}')
			v, err := strconv.ParseUint(s[i+2:i+end], 8, 32)
			if err != nil || v > 0xff {
				return "", false
			}
			b = append(b, byte(v))
			i += end
		default:
			if c >= '0' && c <= '7' {
				v := int(c - '0')
				for n := 0; n < 2 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7'; n++ {
					i++
					v = v*8 + int(s[i]-'0')
				}
				b = append(b, byte(v))
				continue
			}
			b = append(b, c)
		}
	}
	return fromLatin1(b), true
}

// printable is text as pcre2test prints it in 8-bit mode: \xhh for what isn't printable ASCII.
func printable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\x%02x", r)
		}
	}
	return b.String()
}

// supportedMods are the pattern modifiers this harness runs.
var supportedMods = map[string]bool{"i": true, "m": true, "s": true, "x": true, "xx": true, "n": true, "g": true, "aftertext": true,
	// Output about the compiled pattern, which this harness skips, and optimisations that don't change a match.
	"I": true, "B": true, "info": true, "no_start_optimize": true, "no_auto_possess": true, "no_dotstar_anchor": true}

// replacement is a case's replace=… modifier, if it has one.
func (c *pcreCase) replacement() (string, bool) {
	for _, m := range c.mods {
		if r, ok := strings.CutPrefix(m, "replace="); ok {
			return r, true
		}
	}
	return "", false
}

// substitute is what pcre2test prints for a replace= case: how many replacements and the result, or the failure.
func (c *pcreCase) substitute(re *Regex, s pcreSubject, repl string) []string {
	in := linesOf(s.text)
	global := slices.Contains(c.mods, "g") || slices.Contains(c.mods, "global")
	var b strings.Builder
	done, last := 0, 0
	at, notEmpty := Pos{}, false
	for {
		m, ok := re.find(in, at, notEmpty)
		if !ok {
			if notEmpty {
				line := in[at.Line]
				if at.Col < len(line) {
					_, n := utf8.DecodeRuneInString(line[at.Col:])
					at.Col += n
				} else if at.Line+1 < len(in) {
					at = Pos{at.Line + 1, 0}
				} else {
					break
				}
				notEmpty = false
				continue
			}
			break
		}
		start, end := in.offset(m.Start), in.offset(m.End)
		out, err := ExpandPCRE(repl, func(i int) (string, bool) {
			if i < 0 || i >= len(m.Groups) || !m.Groups[i].Set {
				if i >= 0 && i < re.Groups() {
					return "", false
				}
				return "", i < 0 || i < re.Groups()
			}
			g := m.Groups[i]
			return s.text[in.offset(g.Start):in.offset(g.End)], true
		}, func(n string) (int, bool) { i, ok := re.Names()[n]; return i, ok })
		if err != nil {
			return []string{"Failed: " + err.Error()}
		}
		b.WriteString(s.text[last:start])
		b.WriteString(out)
		last = end
		done++
		if !global {
			break
		}
		notEmpty = m.Start == m.End
		at = m.End
	}
	b.WriteString(s.text[last:])
	return []string{fmt.Sprintf("%2d: %s", done, printable(b.String()))}
}

// infoMods print about the pattern before its subjects.
var infoMods = map[string]bool{"I": true, "B": true, "info": true}

func loadPCRE(t *testing.T, name string) []*pcreCase {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "spec", "pcre2", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	var out []*pcreCase
	var cur *pcreCase
	var sub *pcreSubject
	directive := ""
	inDump := false // between the rulers around B's compiled code
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case cur == nil && strings.HasPrefix(l, "#"):
			if f := strings.Fields(l); len(f) > 0 && (f[0] == "#pattern" || f[0] == "#subject") {
				directive = l
				if len(f) > 1 && strings.HasPrefix(f[1], "-") {
					directive = ""
				}
			}
		case cur == nil && strings.HasPrefix(l, "/"):
			start := i
			text := l[1:]
			for {
				if end, ok := closingSlash(text); ok {
					cur = &pcreCase{file: name, line: start + 1, pattern: text[:end], mods: splitMods(text[end+1:])}
					break
				}
				i++
				if i >= len(lines) {
					t.Fatalf("%s:%d: unterminated pattern", name, start+1)
				}
				text += "\n" + lines[i]
			}
			if directive != "" {
				cur.skip = "directive " + directive
			}
			for _, m := range cur.mods {
				if strings.HasPrefix(m, "replace=") && !strings.HasPrefix(m, "replace=[") || m == "global" {
					continue
				}
				if !supportedMods[m] && cur.skip == "" {
					cur.skip = "modifier " + m
				}
			}
			out = append(out, cur)
			sub = nil
		case cur == nil:
		case strings.HasPrefix(l, "------------------------------------------------------------------"):
			inDump = !inDump
		case inDump:
		case strings.TrimSpace(l) == "":
			cur, sub = nil, nil
		case strings.HasPrefix(l, "Failed:") && sub == nil:
			cur.compile = l
		case strings.HasPrefix(l, `\=`):
		case strings.HasPrefix(l, "    "):
			text := strings.TrimSpace(l)
			s := pcreSubject{line: i + 1}
			if j := strings.LastIndex(text, `\=`); j >= 0 {
				text, s.mods = text[:j], text[j+2:]
			}
			cur.subjects = append(cur.subjects, s)
			sub = &cur.subjects[len(cur.subjects)-1]
			if v, ok := subjectEscapes(text); ok {
				sub.text = v
			} else {
				sub.mods = "unescapable subject"
			}
		case sub != nil:
			sub.want = append(sub.want, l)
		default:
			// Output about the pattern itself (Capture group count, Memory allocation…): another harness's.
			if cur.skip == "" && !slices.ContainsFunc(cur.mods, func(m string) bool { return infoMods[m] }) {
				cur.skip = "pattern information output"
			}
		}
	}
	return out
}

// closingSlash finds the / that ends a pattern, skipping escaped ones.
func closingSlash(s string) (int, bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '/':
			return i, true
		}
	}
	return 0, false
}

func splitMods(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		letters := !strings.Contains(f, "=") && strings.Trim(f, "imsxnBIg") == ""
		switch {
		case f == "xx":
			out = append(out, f)
		case letters:
			for _, c := range f {
				out = append(out, string(c))
			}
		default:
			out = append(out, f)
		}
	}
	return out
}

// strInput is a subject as lines.
type strInput []string

func (s strInput) Lines() int        { return len(s) }
func (s strInput) Line(n int) []byte { return []byte(s[n]) }

func linesOf(s string) strInput { return strings.Split(s, "\n") }

// offset is p as a byte offset into the joined lines.
func (s strInput) offset(p Pos) int {
	o := 0
	for i := 0; i < p.Line && i < len(s); i++ {
		o += len(s[i]) + 1
	}
	return o + p.Col
}

// run matches c's pattern against each subject as pcre2test would, and returns what it would print.
func (c *pcreCase) run(re *Regex, s pcreSubject) []string {
	in := linesOf(s.text)
	global := slices.Contains(c.mods, "g")
	after := slices.Contains(c.mods, "aftertext")
	var out []string
	at := Pos{}
	notEmpty := false
	for {
		m, ok := re.find(in, at, notEmpty)
		if !ok {
			if notEmpty {
				// After an empty match, move on a character and look again.
				line := in[at.Line]
				if at.Col < len(line) {
					_, n := utf8.DecodeRuneInString(line[at.Col:])
					at.Col += n
				} else if at.Line+1 < len(in) {
					at = Pos{at.Line + 1, 0}
				} else {
					break
				}
				notEmpty = false
				continue
			}
			if len(out) == 0 {
				out = append(out, "No match")
			}
			break
		}
		last := 0
		for i, g := range m.Groups {
			if g.Set {
				last = i
			}
		}
		text := s.text
		for i := 0; i <= last; i++ {
			g := m.Groups[i]
			if !g.Set {
				out = append(out, fmt.Sprintf("%2d: <unset>", i))
				continue
			}
			out = append(out, fmt.Sprintf("%2d: %s", i, printable(text[in.offset(g.Start):in.offset(g.End)])))
			if i == 0 && after {
				out = append(out, fmt.Sprintf("%2d+ %s", i, printable(text[in.offset(g.End):])))
			}
		}
		if !global {
			break
		}
		notEmpty = m.Start == m.End
		at = m.End
	}
	return out
}

func (r *Regex) find(in strInput, at Pos, notEmpty bool) (Match, bool) {
	m, ok, _ := r.Find(in, at, notEmpty)
	return m, ok
}

func runPCRE(t *testing.T, file string) (passed int, skipped []string) {
	for _, c := range loadPCRE(t, file) {
		if c.skip != "" {
			skipped = append(skipped, fmt.Sprintf("%s:%d %s", c.file, c.line, c.skip))
			continue
		}
		f := PCREFlags{}
		for _, m := range c.mods {
			switch m {
			case "i":
				f.Caseless = true
			case "m":
				f.Multiline = true
			case "s":
				f.DotAll = true
			case "x":
				f.Extended = true
			case "xx":
				f.Extended, f.ExtendedX = true, true
			case "n":
				f.NoAuto = true
			}
		}
		pattern := fromLatin1([]byte(c.pattern))
		re, err := CompilePCRE(pattern, f)
		var unsup *UnsupportedError
		switch {
		case errorAs(err, &unsup):
			skipped = append(skipped, fmt.Sprintf("%s:%d %s", c.file, c.line, unsup.Feature))
			continue
		case c.compile != "" && err != nil:
			passed++
			continue
		case c.compile != "":
			t.Errorf("%s:%d /%s/: compiled; pcre2test says %s", c.file, c.line, c.pattern, c.compile)
			continue
		case err != nil:
			t.Errorf("%s:%d /%s/: %v", c.file, c.line, c.pattern, err)
			continue
		}
		ok := true
		for _, s := range c.subjects {
			if s.mods != "" || len(s.want) > 0 && (strings.HasPrefix(s.want[0], "Matched, but too many") || strings.HasPrefix(s.want[0], "Failed: error -")) {
				continue // a subject modifier, pcre2test's own limit or a match-time error from one
			}
			var got []string
			if repl, ok := c.replacement(); ok {
				got = c.substitute(re, s, repl)
			} else {
				got = c.run(re, s)
			}
			if !slices.Equal(got, s.want) {
				t.Errorf("%s:%d /%s/%s on %q:\n got %q\nwant %q", c.file, s.line, c.pattern, strings.Join(c.mods, ""), printable(s.text), got, s.want)
				ok = false
			}
		}
		if ok {
			passed++
		}
	}
	return passed, skipped
}

func errorAs(err error, target **UnsupportedError) bool {
	u, ok := err.(*UnsupportedError)
	if ok {
		*target = u
	}
	return ok
}

// PCRE2's own tests (spec/pcre2, pcre2-10.44): every case that uses supported features passes; the rest are listed
// in testdata/pcre2-unsupported.txt with why.
func TestPCRE2Suite(t *testing.T) {
	var all []string
	total := 0
	for _, f := range []string{"testoutput1", "testoutput2"} {
		n, skipped := runPCRE(t, f)
		t.Logf("%s: %d patterns pass, %d not supported", f, n, len(skipped))
		total += n
		all = append(all, skipped...)
	}
	golden := filepath.Join("testdata", "pcre2-unsupported.txt")
	got := strings.Join(all, "\n") + "\n"
	if os.Getenv("ORIEL_UPDATE") != "" {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(golden, []byte(got), 0o644)
	}
	want, _ := os.ReadFile(golden)
	if string(want) != got {
		t.Errorf("the unsupported list changed; ORIEL_UPDATE=1 rewrites %s", golden)
	}
	if total < 500 {
		t.Errorf("only %d patterns pass", total)
	}
}

// No pattern or subject makes the parser or matcher panic or hang past its budget.
func FuzzPCRE(f *testing.F) {
	for _, s := range []string{`(a|b)*?c`, `(?<=\d{1,3})x`, `[[:alpha:]-z]`, `\Qa.b\E+`, `(?i)(?<n>x)\k<n>`, `a{2,}+b`, `(?x) a # c`} {
		f.Add(s, "aaxbc123x")
	}
	f.Fuzz(func(t *testing.T, pat, subj string) {
		re, err := CompilePCRE(pat, PCREFlags{})
		if err != nil {
			return
		}
		re.Find(linesOf(subj), Pos{}, false)
	})
}
