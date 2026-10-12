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

type PointerKind uint8

const (
	PointerMove PointerKind = iota
	PointerPress
	PointerRelease
	PointerScroll
	PointerLeave // the pointer left the window
)

type Buttons uint8

const (
	ButtonPrimary Buttons = 1 << iota
	ButtonSecondary
	ButtonTertiary
)

// PointerEvent is the mouse or trackpad, in logical pixels. Buttons is the button pressed or released,
// or those held during a move; Scroll is the distance a scroll moves the content's view.
type PointerEvent struct {
	Kind    PointerKind
	Pos     Point
	Buttons Buttons
	Scroll  Point
	Mods    Mods
}

// Cursor is the pointer's shape.
type Cursor uint8

const (
	CursorDefault Cursor = iota
	CursorPointer
	CursorText
	CursorNotAllowed
	CursorGrab
	CursorResizeCol
	CursorResizeRow
	CursorCrosshair
)

func (KeyEvent) isEvent()     {}
func (PointerEvent) isEvent() {}
func (ResizeEvent) isEvent()  {}
func (RedrawEvent) isEvent()  {}
