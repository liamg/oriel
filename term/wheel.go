package term

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// WheelModes is what Wheel reads of a terminal: its modes, which screen it shows, and how it encodes keys.
type WheelModes interface {
	Mode(m Mode) bool
	AltScreen() bool
	EncodeKey(ev KeyEvent) []byte
}

// Wheel is what n notches of the wheel over cell (col, row) of the screen send the program, up for n < 0, as
// xterm sends them: mouse reports, in the encoding the program asked for, when it asked for mouse events; arrow keys
// on the alternate screen with alternate scroll on; else nil, and the viewer scrolls its scrollback. 1016's pixel
// reports are sent as 1006's, in cells.
func Wheel(e WheelModes, col, row, n int) []byte {
	if n == 0 {
		return nil
	}
	count, button := n, 65
	if n < 0 {
		count, button = -n, 64
	}
	var report string
	switch x, y := col+1, row+1; {
	case !e.Mode(ModeMouseX10) && !e.Mode(ModeMouseNormal) && !e.Mode(ModeMouseButton) && !e.Mode(ModeMouseAny):
		if !e.AltScreen() || !e.Mode(ModeAltScroll) {
			return nil
		}
		key := KeyArrowDown
		if n < 0 {
			key = KeyArrowUp
		}
		return []byte(strings.Repeat(string(e.EncodeKey(KeyEvent{Key: key, Action: KeyPress})), count))
	case e.Mode(ModeMouseSGR) || e.Mode(ModeMouseSGRPixels):
		report = fmt.Sprintf("\x1b[<%d;%d;%dM", button, x, y)
	case e.Mode(ModeMouseURXVT):
		report = fmt.Sprintf("\x1b[%d;%d;%dM", 32+button, x, y)
	case e.Mode(ModeMouseUTF8):
		b := []byte("\x1b[M")
		b = append(b, byte(32+button))
		b = utf8.AppendRune(b, rune(32+min(x, 2015)))
		b = utf8.AppendRune(b, rune(32+min(y, 2015)))
		report = string(b)
	default:
		report = string([]byte{'\x1b', '[', 'M', byte(32 + button), byte(32 + min(x, 223)), byte(32 + min(y, 223))})
	}
	return []byte(strings.Repeat(report, count))
}
