package app

import (
	"strings"

	"github.com/liamg/oriel/registry/keymap"
)

var keyMods = map[byte]string{'C': "⌃", 'M': "⌥", 'D': "⌘", 'S': "⇧"}

var keyNames = map[string]string{"CR": "↵", "Esc": "esc", "Space": "␣", "BS": "⌫", "Tab": "⇥", "lt": "<", "Up": "↑", "Down": "↓",
	"Left": "←", "Right": "→", "X1Mouse": "mouse back", "X2Mouse": "mouse forward"}

// keyLabel writes keys in vim notation as a keyboard shows them: "<D-E>" is ⌘⇧E, "<C-w>c" is ⌃W c, "<Space>f" is ␣f.
func keyLabel(keys string) string {
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
