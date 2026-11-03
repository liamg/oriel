package mine

import (
	"regexp"
	"strings"
)

// Func is one `func Test_*()` in a test file.
type Func struct {
	Name  string
	Line  int // of the func line, 1-based
	Stmts []Stmt
}

// Stmt is one statement: continuation lines joined, and a block (an `:insert` text, a heredoc, or an
// if/for/while/try) kept whole.
type Stmt struct {
	Line  int
	Text  string
	Block bool // control flow or embedded text: replayed as is, never converted
}

var (
	funcRe    = regexp.MustCompile(`^fu(?:n|nc|nct|ncti|nctio|nction)?!?\s+([\w:#]+)\(`)
	endfuncRe = regexp.MustCompile(`^endf(?:u|un|unc|unct|uncti|unctio|unction)?\b`)
	openerRe  = regexp.MustCompile(`^(?:if|for|while|wh|try)\b`)
	enderRe   = regexp.MustCompile(`^(?:en|endi|endif|endfo|endfor|endw|endwhile|endt|endtry)\b`)
	insertRe  = regexp.MustCompile(`^(?:\d*|\$|\.)(?:i|in|ins|inse|inser|insert|a|ap|app|appe|appen|append|c|ch|cha|chan|chang|change)!?$`)
	heredocRe = regexp.MustCompile(`=<<\s*(?:(?:trim|eval)\s+)*(\S+)\s*$`)
)

// Funcs splits a test file into its Test_ functions.
func Funcs(src string) []Func {
	lines := strings.Split(src, "\n")
	var out []Func
	for i := 0; i < len(lines); i++ {
		m := funcRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		f := Func{Name: m[1], Line: i + 1}
		start := i + 1
		for i++; i < len(lines) && !endfuncRe.MatchString(lines[i]); i++ {
		}
		if strings.HasPrefix(f.Name, "Test_") {
			f.Stmts = statements(lines[start:i], start+1)
			out = append(out, f)
		}
	}
	return out
}

// Helpers is the source of the file's functions other than tests, which tests call.
func Helpers(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		m := funcRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		start := i
		for i++; i < len(lines) && !endfuncRe.MatchString(lines[i]); i++ {
		}
		if !strings.HasPrefix(m[1], "Test_") && i < len(lines) {
			out = append(out, lines[start:i+1]...)
		}
	}
	return strings.Join(out, "\n")
}

func statements(lines []string, first int) []Stmt {
	type line struct {
		n    int
		text string
	}
	var joined []line
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, `"\`) {
			continue
		}
		if strings.HasPrefix(t, `\`) && len(joined) > 0 {
			joined[len(joined)-1].text += t[1:]
			continue
		}
		joined = append(joined, line{first + i, l})
	}
	var out []Stmt
	for i := 0; i < len(joined); i++ {
		t := strings.TrimSpace(joined[i].text)
		if t == "" || strings.HasPrefix(t, `"`) {
			continue
		}
		st := Stmt{Line: joined[i].n, Text: t}
		switch {
		case insertRe.MatchString(t):
			raw := []string{t}
			for i++; i < len(joined) && joined[i].text != "."; i++ {
				raw = append(raw, joined[i].text)
			}
			st.Text, st.Block = strings.Join(append(raw, "."), "\n"), true
		case heredocRe.MatchString(t) && strings.HasPrefix(t, "let"):
			marker := heredocRe.FindStringSubmatch(t)[1]
			raw := []string{t}
			for i++; i < len(joined) && strings.TrimSpace(joined[i].text) != marker; i++ {
				raw = append(raw, joined[i].text)
			}
			st.Text, st.Block = strings.Join(append(raw, marker), "\n"), true
		case openerRe.MatchString(t):
			raw, depth := []string{t}, 1
			for i++; i < len(joined) && depth > 0; i++ {
				u := strings.TrimSpace(joined[i].text)
				switch {
				case openerRe.MatchString(u):
					depth++
				case enderRe.MatchString(u):
					depth--
				}
				raw = append(raw, joined[i].text)
			}
			i--
			st.Text, st.Block = strings.Join(raw, "\n"), true
		}
		if st.Block {
			out = append(out, st)
			continue
		}
		for _, part := range splitBar(t) {
			out = append(out, Stmt{Line: st.Line, Text: part})
		}
	}
	return out
}

var normalRe = regexp.MustCompile(`^(?::\s*)?(?:sil(?:e|en|ent)?!?\s+)?norm(?:al|a)?!?(?:\s|$)`)

// splitBar splits a line at `|` command separators. `:normal` takes the rest of the line, and a `"`
// that can't start a string starts a comment.
func splitBar(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if normalRe.MatchString(strings.TrimSpace(s[start:])) {
			break
		}
		switch s[i] {
		case '@':
			i++
		case '\'':
			i = skipString(s, i) - 1
		case '"':
			prev := strings.TrimRight(s[start:i], " \t")
			if prev == "" || strings.HasSuffix(prev, ")") {
				return append(parts, strings.TrimSpace(s[start:i]))
			}
			i = skipString(s, i) - 1
		case '|':
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if p := strings.TrimSpace(s[start:]); p != "" {
		parts = append(parts, p)
	}
	return parts
}
