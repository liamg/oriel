package render

type Event interface{ isEvent() }

type Mods uint8

const (
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
)

// KeyEvent is a key press. Key is a printable character, or a key name from vim's key notation without the <>:
// "Esc", "CR", "Tab", "BS", "Up", "F1".
type KeyEvent struct {
	Key  string
	Mods Mods
}

// ResizeEvent reports a new window size in logical pixels.
type ResizeEvent struct{ Size Point }

// RedrawEvent asks for a new frame: something the last one drew has changed, such as a font landing.
type RedrawEvent struct{}

func (KeyEvent) isEvent()    {}
func (ResizeEvent) isEvent() {}
func (RedrawEvent) isEvent() {}
