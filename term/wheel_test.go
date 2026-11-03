package term_test

import (
	"testing"

	"github.com/liamg/oriel/term"
)

type modes struct {
	on  map[term.Mode]bool
	alt bool
}

func (m modes) Mode(x term.Mode) bool { return m.on[x] }
func (m modes) AltScreen() bool       { return m.alt }
func (m modes) EncodeKey(ev term.KeyEvent) []byte {
	return map[term.Key][]byte{term.KeyArrowUp: []byte("\x1bOA"), term.KeyArrowDown: []byte("\x1bOB")}[ev.Key]
}

func on(ms ...term.Mode) map[term.Mode]bool {
	out := map[term.Mode]bool{}
	for _, m := range ms {
		out[m] = true
	}
	return out
}

// The wheel goes to a program that asked for mouse events as buttons 64 (up) and 65 (down), in its encoding; on the
// alternate screen with alternate scroll on, as arrow keys; otherwise to no one, for the scrollback.
// spec/xterm/ctlseqs.txt, "Wheel mice": "Wheel mice may return buttons 4 and 5.  Those buttons are represented by the
// same event codes as buttons 1 and 2 respectively, except that 64 is added to the event code." And: "if Alternate
// Scroll mode is set, then cursor up/down controls are sent when the terminal is displaying the Alternate Screen
// Buffer."
func TestWheel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		m        modes
		col, row int
		n        int
		want     string
	}{
		{"no mouse, main screen", modes{}, 3, 4, -1, ""},
		{"SGR up", modes{on: on(term.ModeMouseNormal, term.ModeMouseSGR)}, 3, 4, -1, "\x1b[<64;4;5M"},
		{"SGR down twice", modes{on: on(term.ModeMouseAny, term.ModeMouseSGR)}, 0, 0, 2, "\x1b[<65;1;1M\x1b[<65;1;1M"},
		{"URXVT", modes{on: on(term.ModeMouseButton, term.ModeMouseURXVT)}, 9, 1, -1, "\x1b[96;10;2M"},
		{"default", modes{on: on(term.ModeMouseNormal)}, 0, 0, 1, "\x1b[M\x61\x21\x21"},
		{"alternate screen", modes{on: on(term.ModeAltScroll), alt: true}, 0, 0, -2, "\x1bOA\x1bOA"},
		{"alternate screen, alternate scroll off", modes{alt: true}, 0, 0, -2, ""},
		{"mouse wins on the alternate screen", modes{on: on(term.ModeMouseNormal, term.ModeMouseSGR), alt: true}, 0, 0, 1, "\x1b[<65;1;1M"},
		{"no notches", modes{on: on(term.ModeMouseNormal)}, 0, 0, 0, ""},
	} {
		if got := string(term.Wheel(tc.m, tc.col, tc.row, tc.n)); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
