package widget

import "github.com/liamg/oriel/ui"

// SelectRow is a row of a list that a click selects, as List's rows are, for rows a view lays out itself: a list
// item tinted under the pointer rather than washed as a button is, and filled and edged in the selection's colours
// while selected; its own Fill (and its parts') show otherwise, the tint over them. It returns its interaction:
// Clicked to select it, DoubleClicked to open it.
func SelectRow(c *ui.Ctx, key any, st ui.Style, selected bool, body func(c *ui.Ctx)) ui.Interaction {
	hovered := c.Interaction(c.Next(key)).Hovered
	st.NoHover, st.Selected = true, selected
	if st.Role == ui.RoleNone {
		st.Role = ui.RoleListItem
	}
	if selected {
		st.Fill = col(c, tRowPicked)
	}
	return Clickable(c, key, st, func(c *ui.Ctx) {
		if hovered && !selected {
			c.Box("hover", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: col(c, tRowHover), Float: &ui.Float{Fill: true}, Z: ui.LayerUnder}, nil)
		}
		body(c)
		if selected {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: col(c, tRowEdge), Float: &ui.Float{Fill: true}}, nil)
		}
	})
}
