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

// ModsEvent is the modifier keys held now, sent when one goes down or comes up, and with none when the window loses
// focus: a modifier held on its own (hold ⌥ for the HUD).
type ModsEvent struct{ Mods Mods }

// ResizeEvent reports a new window size in logical pixels.
type ResizeEvent struct{ Size Point }

// RedrawEvent asks for a new frame: something the last one drew has changed, such as a font landing.
type RedrawEvent struct{}

// DropEvent is files dropped on the window from outside it, at Pos.
type DropEvent struct {
	Pos   Point
	Paths []string
}

// AppearanceEvent is the OS's light or dark preference, sent when a window opens and whenever it changes.
type AppearanceEvent struct{ Dark bool }

// CloseRequestEvent is a held window asked to close (its close button, Quit): it stays open until the app calls
// Close.
type CloseRequestEvent struct{}

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

func (KeyEvent) isEvent()          {}
func (ModsEvent) isEvent()         {}
func (PointerEvent) isEvent()      {}
func (ResizeEvent) isEvent()       {}
func (RedrawEvent) isEvent()       {}
func (AppearanceEvent) isEvent()   {}
func (DropEvent) isEvent()         {}
func (CloseRequestEvent) isEvent() {}
