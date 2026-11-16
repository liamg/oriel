package widget

import (
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Rule is a one-pixel line across its row in the theme's line colour, between two parts of a view.
func Rule(c *ui.Ctx, key any) ui.ID { return RuleTone(c, key, theme.Line) }

// RuleTone is a Rule in tok.
func RuleTone(c *ui.Ctx, key any, tok theme.TokenID) ui.ID {
	return c.Box(key, ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: c.Theme().C(tok)}, nil)
}

// VRule is Rule standing up: a one-pixel line down its column.
func VRule(c *ui.Ctx, key any) ui.ID { return VRuleTone(c, key, theme.Line) }

// VRuleTone is a VRule in tok.
func VRuleTone(c *ui.Ctx, key any, tok theme.TokenID) ui.ID {
	return c.Box(key, ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: c.Theme().C(tok)}, nil)
}
