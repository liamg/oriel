package kit

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
)

var keyMods = map[byte]string{'C': "⌃", 'M': "⌥", 'D': "⌘", 'S': "⇧"}

var keyNames = map[string]string{"CR": "↵", "Esc": "esc", "Space": "␣", "BS": "⌫", "Tab": "⇥", "lt": "<", "Up": "↑", "Down": "↓",
	"Left": "←", "Right": "→", "Del": "⌦", "Home": "↖", "End": "↘", "PageUp": "⇞", "PageDown": "⇟", "Insert": "ins",
	"Leader": "leader", "LocalLeader": "localleader", "X1Mouse": "mouse back", "X2Mouse": "mouse forward"}

// KeyLabel writes keys in vim notation as a keyboard shows them: "<D-E>" is ⌘⇧E, "<C-w>c" is ⌃W c, "<Space>f" is ␣f.
func KeyLabel(keys string) string {
	var out []string
	for _, tok := range keymap.Parse(keys) {
		if !strings.HasPrefix(tok, "<") || len(tok) < 3 {
			if tok == " " {
				tok = "␣"
			}
			out = append(out, tok)
			continue
		}
		body, mods := tok[1:len(tok)-1], ""
		for len(body) > 2 && body[1] == '-' && keyMods[body[0]] != "" {
			mods += keyMods[body[0]]
			body = body[2:]
		}
		if n, ok := keyNames[body]; ok {
			body = n
		} else if len(body) == 1 && strings.ToUpper(body) == body && strings.ToLower(body) != body {
			mods += "⇧"
		}
		if mods != "" && len(body) == 1 {
			body = strings.ToUpper(body)
		}
		out = append(out, ordered(mods)+body)
	}
	return strings.Join(out, " ")
}

// ordered puts modifiers in the order macOS menus show them: ⌃⌥⇧⌘.
func ordered(mods string) string {
	var b strings.Builder
	for _, m := range []string{"⌃", "⌥", "⇧", "⌘"} {
		if strings.Contains(mods, m) {
			b.WriteString(m)
		}
	}
	return b.String()
}

// BoundKey is the key bound to command with args right now, as the keyboard shows it ("⌘⇧E"), or "".
func BoundKey(h plugin.Host, command string, args registry.Args) string {
	for _, k := range h.KeymapsFor(command) {
		if (k.Mode == "global" || k.Mode == "n") && SameArgs(k.Args, args) {
			return KeyLabel(k.Keys)
		}
	}
	return ""
}

func SameArgs(a, b registry.Args) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if fmt.Sprint(b[k]) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}

// Notation writes a key press as a canonical keymap token: "x", "X", "<C-x>", "<D-s>", "<CR>".
func Notation(ev render.KeyEvent) string {
	single := utf8.RuneCountInString(ev.Key) == 1
	mods := ev.Mods
	// A character's shift is in the character; Space's isn't, so ⌃⇧Space keeps it (⇧Space alone is still a space). A
	// control-letter's case is folded (⌃C is ⌃c), so ⌃⇧ and a letter keeps its shift too: <C-S-c>, Linux's copy.
	ctrlLetter := single && mods&render.ModShift != 0 && mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == render.ModCtrl &&
		unicode.IsLetter([]rune(ev.Key)[0])
	if ctrlLetter {
		ev.Key = strings.ToLower(ev.Key)
	}
	if single && !ctrlLetter && (ev.Key != " " || mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == 0) {
		mods &^= render.ModShift
	}
	if single && mods == 0 {
		return ev.Key
	}
	s := "<"
	for _, m := range []struct {
		mod    render.Mods
		prefix string
	}{{render.ModCtrl, "C-"}, {render.ModAlt, "M-"}, {render.ModSuper, "D-"}, {render.ModShift, "S-"}} {
		if mods&m.mod != 0 {
			s += m.prefix
		}
	}
	name := ev.Key
	if f := keymap.Format([]string{ev.Key}); single && f != ev.Key {
		name = f[1 : len(f)-1] // " " is <Space>, "<" is <lt>
	}
	if toks := keymap.Parse(s + name + ">"); len(toks) == 1 {
		return toks[0]
	}
	return s + name + ">"
}

// KeyEvent reads a keymap token back into a key press: "x", "<CR>", "<C-w>", "<D-S-s>".
func KeyEvent(tok string) render.KeyEvent {
	if !strings.HasPrefix(tok, "<") || len(tok) < 3 {
		return render.KeyEvent{Key: tok}
	}
	body := tok[1 : len(tok)-1]
	var mods render.Mods
	for len(body) > 2 && body[1] == '-' {
		switch body[0] {
		case 'C':
			mods |= render.ModCtrl
		case 'M':
			mods |= render.ModAlt
		case 'D':
			mods |= render.ModSuper
		case 'S':
			mods |= render.ModShift
		}
		body = body[2:]
	}
	if toks := keymap.Parse("<" + body + ">"); len(toks) == 1 && utf8.RuneCountInString(toks[0]) == 1 {
		body = toks[0] // <Space> is " ", <lt> is "<"
	}
	return render.KeyEvent{Key: body, Mods: mods}
}
