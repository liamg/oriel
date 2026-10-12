package vim

import (
	"unicode"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry/keymap"
)

// startRecording is q{reg}: record typed keys into a register.
func (e *Engine) startRecording(reg rune) {
	if !(reg >= 'a' && reg <= 'z' || reg >= 'A' && reg <= 'Z' || reg >= '0' && reg <= '9' || reg == '"') {
		return
	}
	e.recReg = reg
	e.recKeys = nil
}

func (e *Engine) stopRecording() {
	keys := e.recKeys
	if n := len(keys); n > 0 && keys[n-1] == "q" {
		keys = keys[:n-1]
	}
	reg := e.recReg
	e.recReg, e.recKeys = 0, nil
	val := edit.Register{Text: keymap.Format(keys), Kind: edit.Charwise}
	if reg >= 'A' && reg <= 'Z' {
		if old, ok := e.ed.Register(unicode.ToLower(reg)); ok {
			val = appendReg(old, val)
		}
		reg = unicode.ToLower(reg)
	}
	e.ed.SetRegister(reg, val)
}

// playRegister is @{reg}: run a register's text as typed keys, count times.
func (e *Engine) playRegister(reg rune, count int) {
	if reg == '@' {
		reg = e.lastPlayed
	}
	val, ok := e.ed.Register(unicode.ToLower(reg))
	if !ok {
		return
	}
	e.lastPlayed = reg
	keys := keymap.Parse(val.Text)
	if val.Kind == edit.Linewise {
		keys = append(keys, "<CR>")
	}
	for range count {
		for _, k := range keys {
			e.Key(k)
		}
	}
}
