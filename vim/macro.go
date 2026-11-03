package vim

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/edit"
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
	e.lastRecorded = unicode.ToLower(reg)
	val := edit.Register{Text: keysToRaw(keys), Kind: edit.Charwise}
	if reg >= 'A' && reg <= 'Z' {
		reg = unicode.ToLower(reg)
		if old, ok := e.ed.Register(reg); ok {
			if old.Kind == edit.Linewise {
				val = edit.Register{Text: strings.TrimSuffix(old.Text, "\n") + val.Text + "\n", Kind: edit.Linewise}
			} else {
				val = appendReg(old, val)
			}
		}
	}
	e.ed.SetRegister(reg, val)
}

// playRegister is @{reg}: run a register's text as typed keys, count times. @: runs the last command line.
func (e *Engine) playRegister(reg rune, count int) {
	if reg == '@' {
		if e.lastPlayed == 0 {
			e.notify(errNoPrevRegister.with())
			e.failed = true
			return
		}
		reg = e.lastPlayed
	}
	if reg == ':' {
		if e.lastCmdline == "" {
			e.notify(errNoPrevCommand.with())
			return
		}
		e.lastPlayed = ':'
		e.feed(func() {
			for range count {
				e.ex(e.view(), e.lastCmdline)
				e.clamp(e.view())
			}
		})
		return
	}
	if reg == '"' && e.unnamed != 0 {
		reg = e.unnamed
	}
	val, ok := e.ed.Register(unicode.ToLower(reg))
	if !ok {
		e.failed = true
		return
	}
	e.lastPlayed = reg
	keys := rawToKeys(val.Text)
	if val.Kind == edit.Linewise {
		keys = append(keys, "<CR>")
	}
	e.feed(func() {
		if e.playing >= maxMapDepth {
			e.notify(errRecursiveRegister.with())
			e.failed = true
			return
		}
		e.playing++
		defer func() { e.playing-- }()
		for range count {
			for _, k := range keys {
				if e.failed {
					return
				}
				e.Key(k)
			}
		}
	})
}

// feed runs fn as keys that don't come from the user: they aren't recorded, their changes undo as one, and
// the first failure stops them.
func (e *Engine) feed(fn func()) {
	if e.feeding == 0 {
		e.failed = false
		e.outerGroup = e.ed.NewGroup()
	}
	e.feeding++
	defer func() {
		if e.feeding--; e.feeding == 0 {
			e.failed = false
			e.outerGroup = 0
		}
	}()
	fn()
}

// specialKeys are how vim stores keys that aren't characters in a register.
var specialKeys = map[string]string{
	"<Esc>": "\x1b", "<CR>": "\r", "<NL>": "\n", "<Tab>": "\t", " ": " ", "<Nul>": "\x00",
	"<BS>": "\x80kb", "<Del>": "\x80kD", "<Left>": "\x80kl", "<Right>": "\x80kr", "<Up>": "\x80ku",
	"<Down>": "\x80kd", "<Home>": "\x80kh", "<End>": "\x80@7", "<PageUp>": "\x80kP", "<PageDown>": "\x80kN",
	"<Insert>": "\x80kI",
}

var specialRaw = func() map[string]string {
	m := map[string]string{}
	for k, v := range specialKeys {
		m[v] = k
	}
	return m
}()

// keysToRaw turns keys into register text as vim stores it: characters as themselves, control keys as
// control characters.
func keysToRaw(keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		if r, ok := specialKeys[k]; ok {
			b.WriteString(r)
		} else if len(k) == 5 && strings.HasPrefix(k, "<C-") && k[4] == '>' && ctrlable(k[3]) {
			b.WriteByte(byte(unicode.ToUpper(rune(k[3]))) & 0x1f)
		} else {
			b.WriteString(k)
		}
	}
	return b.String()
}

func ctrlable(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || strings.IndexByte("@[\\]^_", c) >= 0
}

// rawToKeys turns register text back into keys.
func rawToKeys(s string) []string {
	var out []string
	for len(s) > 0 {
		if s[0] == 0x80 && len(s) >= 3 {
			if k, ok := specialRaw[s[:3]]; ok {
				out = append(out, k)
				s = s[3:]
				continue
			}
		}
		if k, ok := specialRaw[s[:1]]; ok {
			out = append(out, k)
			s = s[1:]
			continue
		}
		if s[0] < 0x20 {
			c := s[0] | 0x40
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			out = append(out, "<C-"+string(rune(c))+">")
			s = s[1:]
			continue
		}
		_, n := utf8.DecodeRuneInString(s)
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

// Recording is the register q is recording into, as typed: 'A' while appending to a.
func (e *Engine) Recording() (rune, bool) { return e.recReg, e.recReg != 0 }
