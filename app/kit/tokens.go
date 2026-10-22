package kit

import (
	"image/color"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/theme"
)

// TokenColour is a decoration's Style as a colour: a theme token or palette colour, or a syntax group.
func TokenColour(th *theme.Resolved, name string, fallback color.NRGBA) color.NRGBA {
	if name == "" {
		return fallback
	}
	if name[0] == '@' {
		return th.Syntax(name).FG
	}
	if c, ok := th.Color(name); ok {
		return c
	}
	return fallback
}

// Tokens the features share.
var (
	TStatusFg   = theme.Declare("statusline.fg", "fg.muted")
	TStatsHead  = theme.Declare("stats.header.bg", "bg.surface")
	TStatsRowOn = theme.DeclareOn("stats.row.selected", "alpha(accent, 8%)", "bg.surface")
	// StatusTokens are each session status's colour.
	StatusTokens = map[core.Status]theme.TokenID{core.StatusNeedsYou: theme.StatusNeedsYou, core.StatusPermission: theme.StatusPermission,
		core.StatusWorking: theme.StatusWorking, core.StatusCI: theme.StatusCI, core.StatusReviewer: theme.StatusReviewer,
		core.StatusQuiet: theme.StatusQuiet, core.StatusOff: theme.StatusOff}
)
