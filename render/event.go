package render

type Event interface{ isEvent() }

type Mods uint8

const (
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
)

// KeyEvent is a key press. Key is the key's name: a printable character, or a name such as "Escape" or "Enter".
type KeyEvent struct {
	Key  string
	Mods Mods
}

// ResizeEvent reports a new window size in logical pixels.
type ResizeEvent struct{ Size Point }

func (KeyEvent) isEvent()    {}
func (ResizeEvent) isEvent() {}
