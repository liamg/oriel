package kit

import (
	"github.com/liamg/oriel/theme"
)

// The window's chrome that features draw into or beside: its bars, overlays, the omnibox and their help pages.

const OverlayPrefix = "app.overlay." // views named so float over the window: the omnibox, which-key

// HUDHelp is the help on the heads-up display.
const HUDHelp = "shell/hud"

// TabsHelp is the help on the session tabs, and what each says of its session.
const TabsHelp = "shell/tabs"

// NewSessionHelp is the title bar's + button's help, which its right-click menu (Quick session, New session…) is
// contributed to.
const NewSessionHelp = "shell/tabs#new"

// EndHelp is the help on ending a session.
const EndHelp = "shell/tabs#end"

// TitleRight prefixes the names of the views a feature puts at the title bar's right.
const TitleRight = "titlebar.right."

var TOmniBg = theme.Declare("omnibox.bg", "bg.surface")

var TOmniBorder = theme.Declare("omnibox.border", "line.strong")

var TScrimOmni = theme.Declare("omnibox.scrim", "alpha(#030508, 60%)")

// The window's bars: the title bar's height, the status line's, and the sidebar's narrow strip's width.
const (
	TitleH  = 50
	StatusH = 26
	RailW   = 44
)
