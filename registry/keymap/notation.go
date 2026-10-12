// Package keymap reads and writes keys in vim's key notation.
package keymap

import (
	"strings"
	"unicode/utf8"
)

// names maps a lowercased key name to its canonical form; aliases map to the key they stand for.
var names = map[string]string{
	"nul": "<Nul>", "bs": "<BS>", "tab": "<Tab>", "nl": "<NL>", "cr": "<CR>", "return": "<CR>", "enter": "<CR>",
	"esc": "<Esc>", "space": " ", "lt": "<", "bslash": `\`, "bar": "|", "del": "<Del>",
	"up": "<Up>", "down": "<Down>", "left": "<Left>", "right": "<Right>",
	"home": "<Home>", "end": "<End>", "pageup": "<PageUp>", "pagedown": "<PageDown>", "insert": "<Insert>",
	"leader": "<Leader>", "localleader": "<LocalLeader>",
	"f1": "<F1>", "f2": "<F2>", "f3": "<F3>", "f4": "<F4>", "f5": "<F5>", "f6": "<F6>",
	"f7": "<F7>", "f8": "<F8>", "f9": "<F9>", "f10": "<F10>", "f11": "<F11>", "f12": "<F12>",
}

var mods = map[byte]string{'c': "C-", 's': "S-", 'm': "M-", 'a': "M-", 'd': "D-"}

// Parse splits keys into one token per key press: a printable character as itself, anything else in
// canonical notation ("<C-w>", "<CR>"). Text that isn't valid notation is taken literally, as vim does.
func Parse(keys string) []string {
	var out []string
	for len(keys) > 0 {
		if keys[0] == '<' {
			if end := strings.IndexByte(keys, '>'); end > 1 {
				if tok, ok := token(keys[1:end]); ok {
					out = append(out, tok)
					keys = keys[end+1:]
					continue
				}
			}
		}
		_, n := utf8.DecodeRuneInString(keys)
		out = append(out, keys[:n])
		keys = keys[n:]
	}
	return out
}

// token reads what sits between < and >.
func token(s string) (string, bool) {
	prefix := ""
	for len(s) > 2 && s[1] == '-' {
		m, ok := mods[lower(s[0])]
		if !ok {
			return "", false
		}
		prefix += m
		s = s[2:]
	}
	if name, ok := names[strings.ToLower(s)]; ok {
		if prefix == "" {
			return name, true
		}
		return "<" + prefix + strings.Trim(nameOf(name), "<>") + ">", true
	}
	if prefix != "" && utf8.RuneCountInString(s) == 1 {
		if prefix == "C-" {
			s = string(lower(s[0])) + s[1:]
		}
		return "<" + prefix + s + ">", true
	}
	return "", false
}

// nameOf is how a key is written inside <>, so " " under a modifier becomes "Space".
func nameOf(tok string) string {
	switch tok {
	case " ":
		return "Space"
	case "<":
		return "lt"
	case `\`:
		return "Bslash"
	case "|":
		return "Bar"
	}
	return tok
}

func lower(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// Format writes tokens back as notation that Parse reads into the same tokens.
func Format(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		if n := nameOf(t); n != t {
			b.WriteString("<" + n + ">")
		} else {
			b.WriteString(t)
		}
	}
	return b.String()
}
