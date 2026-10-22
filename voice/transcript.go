package voice

import (
	"strings"
	"unicode/utf8"
)

// Transcript turns a dictation's passes (ListenOpts.OnText) into edits of the text it types into, before the cursor:
// each pass replaces the live phrase, which stays provisional until a final pass commits it. Phrases are joined by a
// space.
type Transcript struct {
	// Continue is the cursor following text that doesn't end in a space: the first phrase gets one too.
	Continue bool

	live    string // the live phrase as typed, with its leading space
	phrases []string
}

// Edit takes back Delete runes before the cursor and types Insert. Live is how many runes before the cursor are
// provisional afterwards: drawn muted, and replaced by the next pass.
type Edit struct {
	Delete int
	Insert string
	Live   int
}

// Pass takes in one pass at the phrase. Only what changed is retyped: a correction keeps the words before it.
func (t *Transcript) Pass(text string, final bool) Edit {
	typed := ""
	if text != "" {
		typed = text
		if len(t.phrases) > 0 || t.Continue {
			typed = " " + text
		}
	}
	keep := commonPrefix(t.live, typed)
	e := Edit{Delete: utf8.RuneCountInString(t.live[keep:]), Insert: typed[keep:]}
	if final {
		if text != "" {
			t.phrases = append(t.phrases, text)
		}
		t.live = ""
	} else {
		t.live = typed
		e.Live = utf8.RuneCountInString(typed)
	}
	return e
}

// Drop takes the live phrase back, as Esc does.
func (t *Transcript) Drop() Edit {
	e := Edit{Delete: utf8.RuneCountInString(t.live)}
	t.live = ""
	return e
}

// Committed is the phrases committed so far, joined.
func (t *Transcript) Committed() string { return strings.Join(t.phrases, " ") }

// LiveText is the live phrase, as typed.
func (t *Transcript) LiveText() string { return t.live }

// commonPrefix is the byte length of a and b's common prefix, cut at a rune boundary.
func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	for n > 0 && n < len(a) && !utf8.RuneStart(a[n]) {
		n--
	}
	return n
}
