package ui

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Node is a view tree: what Ctx builds each frame, and what a plugin's view returns in any tier.
type Node interface{ isNode() }

// Box is a flex container: it lays its children out in a row or a column.
type Box struct {
	Key      any // identity among its siblings; their index when nil
	Style    Style
	Children []Node
}

// Text is one line of text.
type Text struct {
	Key   any
	Text  string
	Style TextStyle
}

// Paint is drawn by hand into its rect: the editor, a sparkline.
type Paint struct {
	Key  any
	W, H Size
	Fn   func(f render.Frame, r render.Rect)
}

// Func builds its part of the tree with the frame's Ctx, so a Go view can use widgets (ui/widget) and keep
// their state by id.
type Func struct {
	Key any
	Fn  func(c *Ctx)
}

func (*Box) isNode()   {}
func (*Text) isNode()  {}
func (*Paint) isNode() {}
func (*Func) isNode()  {}

// Dir is the axis a box lays its children along.
type Dir uint8

const (
	Row Dir = iota
	Column
)

type SizeKind uint8

const (
	Fit     SizeKind = iota // as big as the content
	Fixed                   // Value pixels
	Grow                    // a share of the space left over, by weight Value
	Percent                 // Value (0..1) of the parent's inner size
)

// Size is how big a node is along one axis, clamped to Min and Max (when Max > 0).
type Size struct {
	Kind     SizeKind
	Value    float32
	Min, Max float32
}

func Px(v float32) Size   { return Size{Kind: Fixed, Value: v} }
func Flex(w float32) Size { return Size{Kind: Grow, Value: w} }
func Pct(f float32) Size  { return Size{Kind: Percent, Value: f} }
func (s Size) Clamp(min, max float32) Size {
	s.Min, s.Max = min, max
	return s
}

type Align uint8

const (
	Start Align = iota
	Center
	End
	SpaceBetween // main axis only
)

type Insets struct{ L, T, R, B float32 }

func Pad(v float32) Insets      { return Insets{v, v, v, v} }
func PadXY(x, y float32) Insets { return Insets{x, y, x, y} }

type Style struct {
	Dir         Dir
	W, H        Size
	Pad         Insets
	Gap         float32
	Main, Cross Align // along Dir, and across it

	Fill   render.Color
	Stroke render.Color
	Border float32 // stroke width, inside the box
	Radius float32

	Clip    bool
	ScrollX bool // scroll offsets persist by id; scrolling clips
	ScrollY bool
	Wheel   bool // takes the wheel itself: movement over it comes as Interaction.Scroll, for content it scrolls itself

	Float *Float  // out of the flow, placed against another node or a point
	Z     int     // a float's layer (LayerPage…); one inside another float is at least that one's. Below 0 paints behind its parent's content
	Fade  float32 // how faded it's drawn, 0 (opaque) to 1

	// Motion (FW-13), on the motion spring: how the node comes in and goes (an Exit keeps it drawn, without input,
	// until it's out), whether a move glides there, and whether a fill change eases.
	Enter, Exit *Transition
	Glide       bool
	EaseFill    bool

	Interactive bool // takes hover and press
	Focusable   bool // takes focus; implies Interactive
	Disabled    bool // takes neither, and hides what's under it from the pointer
	Cursor      render.Cursor
	NoHover     bool      // draws its own hover and press, so a clickable box doesn't get the toolkit's wash
	Tooltip     string    // shown after the pointer rests on it
	Help        string    // its help page and anchor, "hud/usage#weekly-window" (help.md)
	KeyMode     string    // when focused, keymaps in this mode come first and unmapped keys come to it
	Trap        bool      // keeps keyboard focus inside while it's up, as a modal does
	Drag        *DragData // dragging it carries this, to a node that takes its kind (dnd.go)
	Drop        []string  // the kinds of drag it takes; one let go over it is its Interaction's Dropped

	// Accessibility (a11y.go): a node with a Role is in the accessibility tree; one without is transparent.
	Role     Role
	Label    string // its accessible name; by default, the text inside it
	Value    string // a text field's text, a slider's value
	Selected bool
	Expanded *bool // nil when it doesn't expand
}

// Float places a box against an anchor: the rect of the node with id To (from this frame's layout),
// or Point when To is zero, or the parent's rect when both are. Self's corner (0..1 on each axis)
// goes on the anchor's corner At, plus Offset. A box anchored to a buffer position uses a Point
// the editor publishes.
// Layers are where floats paint, lowest first; a float in another float is at least at its layer, and over it. Add a
// little to one (LayerOverlay + 1) to order floats within it.
const (
	LayerUnder   = -1  // behind its parent's content: a selection under the text
	LayerContent = 0   // in a view: highlights, a cursor
	LayerPane    = 100 // floating panes and the drop-down terminal
	LayerPage    = 200 // full-window pages: settings, the theme editor
	LayerPopup   = 300 // menus and popovers, over what opened them
	LayerToast   = 400 // notifications
	LayerDialog  = 500 // dialogs over a scrim: the omnibox, pickers, help, modals
	LayerOverlay = 600 // over everything: which-key, help markers, pick mode
	LayerDrag    = 700 // a drag's target, glow and ghost
	LayerTooltip = 800 // tooltips and debug readouts
)

type Float struct {
	To     ID
	Point  *render.Point
	At     render.Point // the anchor's corner: {1, 0} is its top right
	Self   render.Point // the box's corner placed there
	Offset render.Point
}

type TextStyle struct {
	Font  typeset.FontSpec // zero Size uses the UI's default style
	Color render.Color
}

// TextNode, ColumnNode and RowNode build trees without a Ctx, for views.
func TextNode(s string) Node { return &Text{Text: s} }

func ColumnNode(children ...Node) Node {
	return &Box{Style: Style{Dir: Column}, Children: children}
}

func RowNode(children ...Node) Node {
	return &Box{Style: Style{Dir: Row}, Children: children}
}
