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

// Name is the token's name, as themes and decorations write it; "" for one never declared.
func (id TokenID) Name() string {
	tokensMu.RLock()
	defer tokensMu.RUnlock()
	if int(id) < 0 || int(id) >= len(tokens) {
		return ""
	}
	return tokens[id].Name
}

// Tokens lists every declared token.
func Tokens() []Token {
	tokensMu.RLock()
	defer tokensMu.RUnlock()
	return append([]Token(nil), tokens...)
}

// semantic are theme's own tokens' names: what a theme file sets under [semantic] (Section).
var semantic = map[string]bool{}

// semanticToken is Declare for one of theme's own tokens.
func semanticToken(name, def string) TokenID { return semanticOn(name, def, "") }

// semanticOn is DeclareOn for one of theme's own tokens.
func semanticOn(name, def, on string) TokenID {
	semantic[name] = true
	return DeclareOn(name, def, on)
}

// Semantic tokens: what every component reads.
var (
	BgBase    = semanticToken("bg.base", "bg0")
	BgSurface = semanticToken("bg.surface", "bg1")
	BgRaised  = semanticToken("bg.raised", "bg2")
	BgOverlay = semanticToken("bg.overlay", "bg3")
	BgStrong  = semanticToken("bg.strong", "bg4")

	Line       = semanticToken("line", "line")
	LineStrong = semanticToken("line.strong", "line2")

	Fg      = semanticToken("fg", "fg")
	FgMuted = semanticToken("fg.muted", "fg2")
	FgFaint = semanticToken("fg.faint", "fg3")
	FgGhost = semanticToken("fg.ghost", "fg4")

	Accent       = semanticToken("accent", "accent")
	AccentStrong = semanticToken("accent.strong", "accent2")
	AccentBg     = semanticOn("accent.bg", "alpha(accent, 8%)", "bg.surface")
	AccentInk    = semanticToken("accent.ink", "ink") // text on the accent
	Brand        = semanticToken("brand", "brand")    // the logo's cursor, debug mode

	Warn  = semanticToken("warn", "amber")
	Error = semanticToken("error", "red")
	OK    = semanticToken("ok", "green")

	Recording = semanticToken("recording", "amber") // a field being dictated into

	StatusNeedsYou   = semanticToken("status.needs_you", "red")
	StatusPermission = semanticToken("status.permission", "violet")
	StatusWorking    = semanticToken("status.working", "cyan")
	StatusCI         = semanticToken("status.ci", "blue")
	StatusReviewer   = semanticToken("status.reviewer", "pink")
	StatusQuiet      = semanticToken("status.quiet", "green")
	StatusOff        = semanticToken("status.off", "fg.ghost")

	DiffAdd = semanticOn("diff.add", "alpha(green, 7%)", "bg.base")
	DiffDel = semanticOn("diff.del", "alpha(red, 7%)", "bg.base")

	Selection = semanticOn("selection", "alpha(accent, 28%)", "bg.base")
	FocusRing = semanticToken("focus.ring", "accent")
	// Scrollbars, drawn over a scrolling box's content.
	ScrollThumb    = semanticToken("scrollbar.thumb", "alpha(fg, 22%)")
	ScrollThumbHot = semanticToken("scrollbar.thumb.hot", "alpha(fg, 42%)")
	Hover          = semanticToken("hover", "alpha(fg, 4%)")   // washed over what's clickable under the pointer
	Pressed        = semanticToken("pressed", "alpha(fg, 8%)") // and while it's pressed
)
