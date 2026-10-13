package mine

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/vim/vimtest"
)

// runeKey is the key a raw character in a key string stands for.
func runeKey(r rune) vimtest.Key {
	switch r {
	case 0x1b:
		return vimtest.Key{Name: "Esc"}
	case '\r':
		return vimtest.Key{Name: "CR"}
	case '\n':
		return vimtest.Key{Name: "NL"}
	case '\t':
		return vimtest.Key{Name: "Tab"}
	case 0x7f:
		return vimtest.Key{Name: "Del"}
	case 0:
		return vimtest.Key{Name: "Nul"}
	}
	if r < 0x20 {
		c := r + 0x40
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		return vimtest.Key{Rune: c, Mods: vimtest.Ctrl}
	}
	return vimtest.Key{Rune: r}
}

// rawKeys reads text as typed characters, as `:normal` does.
func rawKeys(s string) []vimtest.Key {
	var keys []vimtest.Key
	for _, r := range s {
		keys = append(keys, runeKey(r))
	}
	return keys
}

// notationKey reads the body of a "\<...>" escape as one key.
func notationKey(body string) (vimtest.Key, error) {
	keys := vimtest.ParseKeys("<" + body + ">")
	if len(keys) != 1 {
		return vimtest.Key{}, fmt.Errorf("unknown key notation <%s>", body)
	}
	return keys[0], nil
}

// stringLit reads a Vimscript string literal at the start of s as keys, returning what follows it.
func stringLit(s string) ([]vimtest.Key, string, error) {
	switch {
	case strings.HasPrefix(s, "'"):
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != '\'' {
				b.WriteByte(s[i])
				continue
			}
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return rawKeys(b.String()), s[i+1:], nil
		}
		return nil, "", errors.New("unterminated string")
	case strings.HasPrefix(s, `"`):
		return doubleQuoted(s)
	}
	return nil, "", fmt.Errorf("not a string: %q", s)
}

func doubleQuoted(s string) ([]vimtest.Key, string, error) {
	var keys []vimtest.Key
	for i := 1; i < len(s); {
		c := s[i]
		if c == '"' {
			return keys, s[i+1:], nil
		}
		if c != '\\' {
			r, n := utf8.DecodeRuneInString(s[i:])
			keys = append(keys, runeKey(r))
			i += n
			continue
		}
		if i+1 >= len(s) {
			break
		}
		i++
		e := s[i]
		i++
		switch e {
		case 'n':
			keys = append(keys, runeKey('\n'))
		case 'r':
			keys = append(keys, runeKey('\r'))
		case 't':
			keys = append(keys, runeKey('\t'))
		case 'e':
			keys = append(keys, runeKey(0x1b))
		case 'b':
			keys = append(keys, runeKey(0x08))
		case 'f':
			keys = append(keys, runeKey(0x0c))
		case '<':
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return nil, "", errors.New("unterminated \\<")
			}
			k, err := notationKey(s[i : i+end])
			if err != nil {
				return nil, "", err
			}
			keys = append(keys, k)
			i += end + 1
		case 'x', 'X', 'u', 'U':
			max := map[byte]int{'x': 2, 'X': 2, 'u': 4, 'U': 8}[e]
			n := 0
			for n < max && i+n < len(s) && isHex(s[i+n]) {
				n++
			}
			if n == 0 {
				keys = append(keys, runeKey(rune(e)))
				continue
			}
			v, _ := strconv.ParseUint(s[i:i+n], 16, 32)
			keys = append(keys, runeKey(rune(v)))
			i += n
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i - 1
			for i < len(s) && i-j < 3 && s[i] >= '0' && s[i] <= '7' {
				i++
			}
			v, _ := strconv.ParseUint(s[j:i], 8, 32)
			keys = append(keys, runeKey(rune(v)))
		default:
			r, n := utf8.DecodeRuneInString(s[i-1:])
			keys = append(keys, runeKey(r))
			i += n - 1
		}
	}
	return nil, "", errors.New("unterminated string")
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

var numberLit = regexp.MustCompile(`^-?\d+`)

// concatKeys reads an expression made only of string and number literals joined by `..` or `.`.
func concatKeys(expr string) ([]vimtest.Key, error) {
	var keys []vimtest.Key
	s := strings.TrimSpace(expr)
	for {
		if m := numberLit.FindString(s); m != "" {
			keys = append(keys, rawKeys(m)...)
			s = s[len(m):]
		} else {
			k, rest, err := stringLit(s)
			if err != nil {
				return nil, err
			}
			keys = append(keys, k...)
			s = rest
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return keys, nil
		}
		switch {
		case strings.HasPrefix(s, ".."):
			s = strings.TrimSpace(s[2:])
		case strings.HasPrefix(s, "."):
			s = strings.TrimSpace(s[1:])
		default:
			return nil, fmt.Errorf("not a literal: %q", expr)
		}
	}
}

// skipString returns the index just past the string literal starting at s[i].
func skipString(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch {
		case q == '"' && s[j] == '\\':
			j++
		case s[j] == q && q == '\'' && j+1 < len(s) && s[j+1] == '\'':
			j++
		case s[j] == q:
			return j + 1
		}
	}
	return len(s)
}

// splitArgs splits a call's argument list at top-level commas.
func splitArgs(s string) []string {
	var args []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '@':
			i++
		case '\'', '"':
			i = skipString(s, i) - 1
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" || len(args) > 0 {
		args = append(args, rest)
	}
	return args
}

var callRe = regexp.MustCompile(`^(?::\s*)?call\s+([\w:#]+)\(`)

// parseCall reads `call name(args)`, allowing a trailing comment.
func parseCall(stmt string) (name string, args []string, ok bool) {
	m := callRe.FindStringSubmatch(stmt)
	if m == nil {
		return "", nil, false
	}
	open := len(m[0]) - 1
	depth := 0
	for i := open; i < len(stmt); i++ {
		switch stmt[i] {
		case '@':
			i++
		case '\'', '"':
			i = skipString(stmt, i) - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				rest := strings.TrimSpace(stmt[i+1:])
				if rest != "" && !strings.HasPrefix(rest, `"`) {
					return "", nil, false
				}
				return m[1], splitArgs(stmt[open+1 : i]), true
			}
		}
	}
	return "", nil, false
}

var identRe = regexp.MustCompile(`^[A-Za-z_][\w:]*`)

// isLiteral reports whether expr is built only from string and number literals, lists, concatenation,
// and variables in vars.
func isLiteral(expr string, vars map[string]bool) bool {
	s := strings.TrimSpace(expr)
	if s == "" {
		return false
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '[' || c == ']' || c == ',' || c == '.':
			i++
		case c == '\'' || c == '"':
			j := skipString(s, i)
			if j > len(s) || s[j-1] != c || j-i < 2 {
				return false
			}
			i = j
		case c == '-' || c >= '0' && c <= '9':
			m := numberLit.FindString(s[i:])
			if m == "" {
				return false
			}
			i += len(m)
		default:
			m := identRe.FindString(s[i:])
			if m == "" || !vars[m] {
				return false
			}
			i += len(m)
			if i < len(s) && s[i] == '(' {
				return false
			}
		}
	}
	return true
}
