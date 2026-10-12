package vimtest

import (
	"strings"
	"unicode/utf8"
)

// Mod is a set of key modifiers.
type Mod uint8

const (
	Shift Mod = 1 << iota
	Ctrl
	Alt
	Cmd
)

// Key is one keystroke. Name is set for special keys ("Esc", "CR", "Up"…), Rune otherwise.
type Key struct {
	Rune rune
	Name string
	Mods Mod
}

// specialKeys maps lower-cased key notation (see :help key-notation) to its canonical name.
var specialKeys = map[string]string{
	"esc": "Esc", "cr": "CR", "return": "CR", "enter": "CR", "nl": "NL", "tab": "Tab",
	"bs": "BS", "backspace": "BS", "del": "Del", "delete": "Del", "space": "Space",
	"lt": "lt", "bslash": "Bslash", "bar": "Bar", "nul": "Nul", "ff": "FF",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "pageup": "PageUp", "pagedown": "PageDown",
	"insert": "Insert", "help": "Help", "undo": "Undo",
	"f1": "F1", "f2": "F2", "f3": "F3", "f4": "F4", "f5": "F5", "f6": "F6",
	"f7": "F7", "f8": "F8", "f9": "F9", "f10": "F10", "f11": "F11", "f12": "F12",
}

// runeKeys are notations that stand for a plain character.
var runeKeys = map[string]rune{"lt": '<', "bslash": '\\', "bar": '|', "space": ' '}

var modPrefixes = map[byte]Mod{'s': Shift, 'c': Ctrl, 'a': Alt, 'm': Alt, 'd': Cmd}

// ParseKeys parses vim key notation: plain characters plus <Esc>, <C-x>, <S-Up> and the like.
// A '<' that doesn't start valid notation is a literal '<', as in vim.
func ParseKeys(s string) []Key {
	var keys []Key
	for len(s) > 0 {
		if s[0] == '<' {
			if k, n, ok := parseNotation(s); ok {
				keys = append(keys, k)
				s = s[n:]
				continue
			}
		}
		r, n := utf8.DecodeRuneInString(s)
		keys = append(keys, Key{Rune: r})
		s = s[n:]
	}
	return keys
}

func parseNotation(s string) (Key, int, bool) {
	end := strings.IndexByte(s[1:], '>')
	if end < 1 {
		return Key{}, 0, false
	}
	body := s[1 : end+1]
	var mods Mod
	for len(body) > 2 && body[1] == '-' {
		m, ok := modPrefixes[body[0]|0x20]
		if !ok {
			return Key{}, 0, false
		}
		mods |= m
		body = body[2:]
	}
	k := Key{Mods: mods}
	if name, ok := specialKeys[strings.ToLower(body)]; ok {
		if r, ok := runeKeys[strings.ToLower(body)]; ok {
			k.Rune = r
		} else {
			k.Name = name
		}
	} else if r, n := utf8.DecodeRuneInString(body); n == len(body) && mods != 0 {
		k.Rune = r
	} else {
		return Key{}, 0, false
	}
	return k, end + 2, true
}

// Notation renders keys back as vim key notation, which nvim_replace_termcodes accepts.
func Notation(keys []Key) string {
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k.String())
	}
	return sb.String()
}

func (k Key) String() string {
	name := k.Name
	if name == "" {
		switch k.Rune {
		case '<':
			name = "lt"
		case '\\':
			name = "Bslash"
		case '|':
			name = "Bar"
		default:
			if k.Mods == 0 {
				return string(k.Rune)
			}
			name = string(k.Rune)
		}
	}
	var sb strings.Builder
	sb.WriteByte('<')
	for _, m := range []struct {
		m Mod
		p string
	}{{Shift, "S-"}, {Ctrl, "C-"}, {Alt, "M-"}, {Cmd, "D-"}} {
		if k.Mods&m.m != 0 {
			sb.WriteString(m.p)
		}
	}
	sb.WriteString(name)
	sb.WriteByte('>')
	return sb.String()
}
