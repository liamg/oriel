package scenes

import (
	"image/color"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token is a coloured run within a line.
type Token struct {
	Text   string
	Color  color.NRGBA
	Italic bool
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true,
	"defer": true, "else": true, "fallthrough": true, "for": true, "func": true, "go": true, "goto": true,
	"if": true, "import": true, "interface": true, "map": true, "package": true, "range": true,
	"return": true, "select": true, "struct": true, "switch": true, "type": true, "var": true,
}

var goTypes = map[string]bool{
	"bool": true, "byte": true, "error": true, "int": true, "int32": true, "int64": true,
	"string": true, "float64": true, "rune": true, "uint": true, "any": true,
}

const goOps = ":=!<>+-*/%&|^="

// LexGo splits one line of Go into coloured tokens. It is a highlighter for the spike, not a parser.
func LexGo(line string) []Token {
	var out []Token
	emit := func(s string, c color.NRGBA, it bool) {
		if s != "" {
			out = append(out, Token{s, c, it})
		}
	}
	i := 0
	for i < len(line) {
		r, n := utf8.DecodeRuneInString(line[i:])
		switch {
		case strings.HasPrefix(line[i:], "//"):
			emit(line[i:], synC, true)
			return out
		case r == '"' || r == '`':
			j := i + 1
			for j < len(line) && rune(line[j]) != r {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(line))
			emit(line[i:j], synS, false)
			i = j
		case unicode.IsDigit(r):
			j := i
			for j < len(line) && (line[j] == '.' || line[j] == 'x' || unicode.IsDigit(rune(line[j]))) {
				j++
			}
			emit(line[i:j], synN, false)
			i = j
		case unicode.IsLetter(r) || r == '_':
			j := i
			for j < len(line) {
				r2, n2 := utf8.DecodeRuneInString(line[j:])
				if !unicode.IsLetter(r2) && !unicode.IsDigit(r2) && r2 != '_' {
					break
				}
				j += n2
			}
			w := line[i:j]
			switch {
			case goKeywords[w]:
				emit(w, synK, false)
			case goTypes[w]:
				emit(w, synT, false)
			case j < len(line) && line[j] == '(':
				emit(w, synF, false)
			default:
				emit(w, fg, false)
			}
			i = j
		case strings.ContainsRune(goOps, r):
			j := i
			for j < len(line) && strings.ContainsRune(goOps, rune(line[j])) {
				j++
			}
			emit(line[i:j], synP, false)
			i = j
		default:
			j := i + n
			for j < len(line) {
				r2, _ := utf8.DecodeRuneInString(line[j:])
				if unicode.IsLetter(r2) || unicode.IsDigit(r2) || r2 == '_' || r2 == '"' || r2 == '`' || strings.ContainsRune(goOps, r2) || strings.HasPrefix(line[j:], "//") {
					break
				}
				j++
			}
			emit(line[i:j], fg, false)
			i = j
		}
	}
	return out
}
