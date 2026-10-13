package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// SparkBar is one bar of a sparkline: its height from 0 to 1, and its colour.
type SparkBar struct {
	H    float32
	Tone render.Color
}

// Spark is a tiny bar chart, bottom-aligned: a test's recent runs, coloured by how each went.
func Spark(c *ui.Ctx, key any, bars []SparkBar, label string) {
	c.Row(key, ui.Style{H: ui.Px(12), Gap: 1, Cross: ui.End, Role: ui.RoleText, Label: label}, func(c *ui.Ctx) {
		for i, b := range bars {
			c.Box(i, ui.Style{W: ui.Px(3), H: ui.Px(max(2, 12*min(b.H, 1))), Fill: b.Tone, Radius: 1}, nil)
		}
	})
}
