// Package term is the terminal model panes draw and type into: an Emulator turns a program's output into a grid of
// cells, and keys and pastes into the bytes the program reads. term/ghostty implements it over libghostty-vt;
// term/pty starts programs on a pseudo-terminal.
package term

// Emulator is one terminal's state: screen, scrollback, modes and shell-integration marks. An Emulator is safe for use
// by one writer and one reader at a time (a PTY reader calling Write while a renderer calls Grid).
type Emulator interface {
	Write(p []byte) // bytes from the PTY
	Resize(cols, rows int)
	Grid() Grid // the viewport, with the rows changed since the last call marked dirty
	Cursor() (x, y int, shape Shape)
	Title() string

	Size() (cols, rows int)
	CursorState() CursorState
	Mode(m Mode) bool
	AltScreen() bool // the alternate screen is showing, as full-screen programs ask for

	// Held reports that the program asked to synchronize output (DEC 2026): Grid keeps returning the last
	// complete frame until it ends or times out.
	Held() bool

	Scroll(s Scroll) // moves the viewport over the scrollback
	Scrollbar() Scrollbar
	Text(from, to Point) string // plain text between two screen points, inclusive, soft wraps joined

	Commands() []Command // shell commands marked with OSC 133, oldest first
	Cwd() string         // the directory the shell reported with OSC 7, as a path

	EncodeKey(ev KeyEvent) []byte // the bytes a key sends in the terminal's current modes
	Paste(text string) []byte     // the bytes a paste sends, bracketed when the program asked

	Snapshot() ([]byte, error) // everything needed to show the terminal again, scrollback included
	Restore(snap []byte) error // replaces the state with a snapshot's
	Close() error
}

// Grid is the viewport. Lines share memory with the emulator and are valid until the next call to Grid.
type Grid struct {
	Cols, Rows int
	Lines      []Line
}

// Line is one row of the viewport.
type Line struct {
	Cells            []Cell
	Dirty            bool // changed since the last Grid
	Wrapped          bool // soft-wrapped onto the next line
	WrapContinuation bool // continues the line above
	Prompt           Prompt
}

// Prompt says whether a line holds a shell prompt (OSC 133).
type Prompt uint8

const (
	PromptNone Prompt = iota
	PromptPrimary
	PromptContinuation
)

// Text is the line's characters, a space for each empty cell and nothing for the second half of a wide character.
func (l Line) Text() string {
	b := make([]byte, 0, len(l.Cells))
	for _, c := range l.Cells {
		switch {
		case c.Width == SpacerTail || c.Width == SpacerHead:
		case c.Text == "":
			b = append(b, ' ')
		default:
			b = append(b, c.Text...)
		}
	}
	return string(b)
}

// Cell is one column of a line.
type Cell struct {
	Text           string // a grapheme cluster, or "" for an empty cell
	Width          Width
	FG, BG         Color
	UnderlineColor Color
	Attrs          Attrs
	Underline      Underline
}

// Width is how much of a wide character a cell holds.
type Width uint8

const (
	Narrow     Width = iota
	Wide             // the first cell of a two-cell character
	SpacerTail       // the second cell of a wide character; draw nothing
	SpacerHead       // padding at the end of a line before a wide character that wrapped
)

// Attrs are a cell's text attributes (SGR).
type Attrs uint8

const (
	Bold Attrs = 1 << iota
	Italic
	Faint
	Blink
	Inverse
	Invisible
	Strikethrough
	Overline
)

// Underline is a cell's underline style (SGR 4 and its 4:n subparameters).
type Underline uint8

const (
	UnderlineNone Underline = iota
	UnderlineSingle
	UnderlineDouble
	UnderlineCurly
	UnderlineDotted
	UnderlineDashed
)

// Color is the terminal's default, a palette index or an RGB value.
type Color uint32

// ColorKind says which a Color is.
type ColorKind uint8

const (
	ColorDefault ColorKind = iota
	ColorPalette
	ColorRGB
)

func PaletteColor(i uint8) Color { return Color(uint32(ColorPalette)<<24 | uint32(i)) }
func RGBColor(r, g, b uint8) Color {
	return Color(uint32(ColorRGB)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}
func (c Color) Kind() ColorKind      { return ColorKind(c >> 24) }
func (c Color) Index() uint8         { return uint8(c) }
func (c Color) RGB() (r, g, b uint8) { return uint8(c >> 16), uint8(c >> 8), uint8(c) }

// Shape is the cursor's shape (DECSCUSR).
type Shape uint8

const (
	ShapeBar Shape = iota
	ShapeBlock
	ShapeUnderline
	ShapeBlockHollow
)

// CursorState is the cursor as a renderer draws it.
type CursorState struct {
	X, Y       int  // in the active area, where the program writes
	InViewport bool // the viewport shows the cursor, at ViewX, ViewY
	ViewX      int
	ViewY      int
	Visible    bool // DECTCEM
	Blinking   bool
	Shape      Shape
	WideTail   bool // on the second half of a wide character
}

// Mode is an ANSI or DEC private mode, as SM/RM and DECSET/DECRST set it.
type Mode struct {
	N    uint16
	ANSI bool
}

var (
	ModeInsert          = Mode{N: 4, ANSI: true}
	ModeAppCursorKeys   = Mode{N: 1}    // DECCKM
	ModeOrigin          = Mode{N: 6}    // DECOM
	ModeAutoWrap        = Mode{N: 7}    // DECAWM
	ModeCursorVisible   = Mode{N: 25}   // DECTCEM
	ModeAppKeypad       = Mode{N: 66}   // DECNKM
	ModeMouseX10        = Mode{N: 9}    // button presses
	ModeMouseNormal     = Mode{N: 1000} // presses and releases
	ModeMouseButton     = Mode{N: 1002} // and motion with a button held
	ModeMouseAny        = Mode{N: 1003} // and all motion
	ModeFocusEvents     = Mode{N: 1004}
	ModeMouseUTF8       = Mode{N: 1005}
	ModeMouseSGR        = Mode{N: 1006}
	ModeAltScroll       = Mode{N: 1007}
	ModeMouseURXVT      = Mode{N: 1015}
	ModeMouseSGRPixels  = Mode{N: 1016}
	ModeAltScreen       = Mode{N: 1049} // alternate screen, saving the cursor
	ModeBracketedPaste  = Mode{N: 2004}
	ModeSyncOutput      = Mode{N: 2026}
	ModeGraphemeCluster = Mode{N: 2027}
)

// Point is a cell on the screen: X a column, Y a row counted from the top of the scrollback.
type Point struct{ X, Y int }

// Scroll moves the viewport: to the top or bottom of the scrollback, by Delta rows (up is negative), or to Row.
type Scroll struct {
	To    ScrollTo
	Delta int
	Row   int
}

type ScrollTo uint8

const (
	ScrollTop ScrollTo = iota
	ScrollBottom
	ScrollDelta
	ScrollRow
)

// Scrollbar is the scrollback as a scrollbar shows it, in rows.
type Scrollbar struct {
	Total, Offset, Len int
}

// Command is one shell command marked with OSC 133. Its rows are screen rows, as Point.Y, and follow the text as it
// scrolls; a mark that has scrolled out of the scrollback, or was never sent, is (-1, -1).
type Command struct {
	Prompt   Point // A: the prompt starts
	Input    Point // B: the prompt ends and the command line starts
	Output   Point // C: the command runs and its output starts
	End      Point // D: the command finished
	Finished bool
	ExitCode int // from D; 0 when the shell sent none
	HasExit  bool
}
