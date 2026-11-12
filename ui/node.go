package ui

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
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

func Px(v float32) Size { return Size{Kind: Fixed, Value: v} }

// times is s drawn k times bigger: its pixels scale; a share or a percentage doesn't.
func (s Size) times(k float32) Size {
	if s.Kind == Fixed {
		s.Value *= k
	}
	s.Min, s.Max = s.Min*k, s.Max*k
	return s
}
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

	// Scale draws the box and everything in it this many times bigger (its sizes, padding, gaps, radii and text), as a
	// part with its own text size does; 0 is 1. It multiplies a scaled parent's.
	Scale float32
	// Theme is the theme the box's subtree is built in (Ctx.Theme), a variation of the window's (theme.Resolved.With);
	// nil is its parent's.
	Theme *theme.Resolved

	Clip bool
	// Selectable makes it the box a selection stays in, with the text cursor over it. All text selects with the pointer
	// and copies with the platform's copy key by default, in the nearest Selectable, scrolling or floating box (else the
	// window): a drag across text nodes, a double click a word, a triple a node's text. Controls keep their clicks, and
	// their text, menus and popovers' never selects.
	Selectable bool
	// NoSelect keeps its text from being selected: a press on it starts no selection, and one around it skips it.
	NoSelect bool
	// Wrap puts a row's children that don't fit its width on the next line, Gap apart, each its own width (one wider
	// than the row is cut to it): the row grows as tall as its lines. Give it a width (Flex, Px); its own content is
	// only its widest child.
	Wrap    bool
	Keep    bool // keeps its width while its row is too narrow, as a control does; the text beside it is cut first
	ScrollX bool // scroll offsets persist by id; scrolling clips
	ScrollY bool
	// ScrollWith makes a scrolling box share its offset with every box naming the same id, as a diff's lines do: the
	// wheel over any moves them all, as far as the widest content of those laid out goes, and ScrollBy or ScrollTo on
	// the id (or any of them) moves them too. Its scrollbar is its own; hide it (BarsHidden) for many in a column.
	ScrollWith ID
	Follow     bool // a scrolling box at its end stays there as it grows, until scrolled away from it
	Wheel      bool // takes the wheel itself: movement over it comes as Interaction.Scroll, for content it scrolls itself
	// Sideways makes the wheel's up and down scroll it across, for a box that scrolls only that way: a strip of tabs.
	Sideways bool
	Bars     Bars // when a scrolling box shows its scrollbars

	Float *Float  // out of the flow, placed against another node or a point
	Z     int     // a float's layer (LayerPage…); one inside another float is at least that one's. Below 0 paints behind its parent's content
	Fade  float32 // how faded it's drawn, 0 (opaque) to 1

	// Motion, on the motion spring: how the node comes in and goes (an Exit keeps it drawn, without input,
	// until it's out), whether a move glides there, and whether a fill change eases.
	Enter, Exit *Transition
	Glide       bool
	EaseFill    bool

	Interactive bool // takes hover and press
	Focusable   bool // takes focus; implies Interactive
	NoFocus     bool // a drag source or target the pointer alone moves: what it's in has the keys, so it isn't focused
	Disabled    bool // takes neither, and hides what's under it from the pointer
	Cursor      render.Cursor
	NoHover     bool      // draws its own hover and press, so a clickable box doesn't get the toolkit's wash
	Sink        bool      // its content sinks a pixel while it's pressed, as a button's does
	Tooltip     string    // shown after the pointer rests on it
	Help        string    // its help page and anchor, "hud/usage#weekly-window"
	Subject     string    // what it stands for to a right-click menu: "session:fix-login"
	KeyMode     string    // when focused, keymaps in this mode come first and unmapped keys come to it
	Text        bool      // typed characters insert text here, so dictation may type into it (UI.TakesText)
	Trap        bool      // keeps keyboard focus inside while it's up, as a modal does
	Drag        *DragData // dragging it carries this, to a node that takes its kind (dnd.go)
	Drop        []string  // the kinds of drag it takes; one let go over it is its Interaction's Dropped

	// DefaultFocus marks where focus goes when what it's in opens or is activated (Ctx.FocusInto), or when an
	// overlay over it closes and what had focus before is gone.
	DefaultFocus bool
	// FocusOnOpen takes keyboard focus to it (its DefaultFocus target, else itself) the frame it first appears, unless
	// focus is on a higher layer; when it goes, focus goes back to what had it. It doesn't trap focus: a card that
	// wants an answer now, over whatever was being typed in.
	FocusOnOpen bool

	// Accessibility (a11y.go): a node with a Role is in the accessibility tree; one without is transparent.
	Role     Role
	Label    string // its accessible name; by default, the text inside it
	Value    string // a text field's text, a slider's value
	Selected bool
	Expanded *bool // nil when it doesn't expand
	Checked  *bool // a check box's state
	// Selection is a text field's selection, or its caret.
	Selection TextRange
	// Actions are what it takes from assistive technology besides a click, focus and scroll; they reach it as
	// Interaction.Access.
	Actions []Action
}

// Float places a box against an anchor: the rect of the node with id To (from this frame's layout),
// or Point when To is zero, or the parent's rect when both are. Self's corner (0..1 on each axis)
// goes on the anchor's corner At, plus Offset. A box anchored to a buffer position uses a Point
// the editor publishes.
// Layers are where floats paint, lowest first; a float in another float is at least at its layer, and over it. Add a
// little to one (LayerOverlay + 1) to order floats within it. A float under LayerPane is inlaid in its view: it's
// clipped by the scrolling and clipping boxes it's in, up to the float holding them.
const (
	LayerUnder   = -1  // behind its parent's content: a selection under the text
	LayerContent = 0   // in a view: highlights, a cursor
	LayerPane    = 100 // floating panes and the drop-down terminal
	LayerPage    = 200 // full-window pages: settings, the theme editor
	LayerPopup   = 300 // menus and popovers, over what opened them
	LayerToast   = 400 // notifications
	LayerWindow  = 450 // floating windows kept over the IDE and its notifications: the assistant's
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
	// Fill makes a float placed against its parent and sized Flex fill the parent that way: a card's edge.
	Fill bool
	// Flip places it the other way up when it would run past the window's bottom: At, Self and Offset mirrored on
	// the vertical, so a menu under a control at the foot of the window opens above it. It never starts above the
	// window's top.
	Flip bool
	// FlipX does the same across: a card beside a control at the window's right edge opens on its left.
	FlipX bool
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
