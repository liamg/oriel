package theme

import "sync"

// TokenID indexes a resolved theme's colours, so a widget's lookup costs nothing per frame.
type TokenID int

// Token is a colour widgets read by name. Its default is an expression over other tokens and the palette;
// On, when set, is the token it sits on, so a translucent value resolves to an opaque one.
type Token struct {
	ID      TokenID
	Name    string
	Default string
	On      string
}

var (
	tokensMu sync.RWMutex
	tokens   []Token
	byName   = map[string]TokenID{}
)

// Declare adds a token, or returns the one already declared under that name. Built-in widgets and
// plugins declare the component tokens they paint with.
func Declare(name, def string) TokenID { return DeclareOn(name, def, "") }

// DeclareOn declares a token that sits on another, as accent.bg sits on bg.surface.
func DeclareOn(name, def, on string) TokenID {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	if id, ok := byName[name]; ok {
		return id
	}
	id := TokenID(len(tokens))
	tokens = append(tokens, Token{ID: id, Name: name, Default: def, On: on})
	byName[name] = id
	return id
}

// Tokens lists every declared token.
func Tokens() []Token {
	tokensMu.RLock()
	defer tokensMu.RUnlock()
	return append([]Token(nil), tokens...)
}

// Semantic tokens: what every component reads (theming.md).
var (
	BgBase    = Declare("bg.base", "bg0")
	BgSurface = Declare("bg.surface", "bg1")
	BgRaised  = Declare("bg.raised", "bg2")
	BgOverlay = Declare("bg.overlay", "bg3")
	BgStrong  = Declare("bg.strong", "bg4")

	Line       = Declare("line", "line")
	LineStrong = Declare("line.strong", "line2")

	Fg      = Declare("fg", "fg")
	FgMuted = Declare("fg.muted", "fg2")
	FgFaint = Declare("fg.faint", "fg3")
	FgGhost = Declare("fg.ghost", "fg4")

	Accent       = Declare("accent", "accent")
	AccentStrong = Declare("accent.strong", "accent2")
	AccentBg     = DeclareOn("accent.bg", "alpha(accent, 8%)", "bg.surface")
	AccentInk    = Declare("accent.ink", "ink") // text on the accent
	Brand        = Declare("brand", "brand")    // the logo's cursor, debug mode

	Warn  = Declare("warn", "amber")
	Error = Declare("error", "red")
	OK    = Declare("ok", "green")

	StatusNeedsYou   = Declare("status.needs_you", "red")
	StatusPermission = Declare("status.permission", "violet")
	StatusWorking    = Declare("status.working", "cyan")
	StatusCI         = Declare("status.ci", "blue")
	StatusReviewer   = Declare("status.reviewer", "pink")
	StatusQuiet      = Declare("status.quiet", "green")
	StatusOff        = Declare("status.off", "fg.ghost")

	DiffAdd = DeclareOn("diff.add", "alpha(green, 7%)", "bg.base")
	DiffDel = DeclareOn("diff.del", "alpha(red, 7%)", "bg.base")

	Selection = DeclareOn("selection", "alpha(accent, 20%)", "bg.base")
	FocusRing = Declare("focus.ring", "accent")
	Hover     = Declare("hover", "alpha(fg, 4%)")   // washed over what's clickable under the pointer
	Pressed   = Declare("pressed", "alpha(fg, 8%)") // and while it's pressed
)
