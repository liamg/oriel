package diagram

import "github.com/liamg/oriel/theme"

// The tokens a diagram draws with, on the surface of the box it's drawn in.
var (
	tNodeBg      = theme.Declare("diagram.node.bg", "bg.raised")
	tNodeBorder  = theme.Declare("diagram.node.border", "line.strong")
	tNodeFg      = theme.Declare("diagram.node.fg", "fg")
	tEdge        = theme.DeclareOn("diagram.edge", "alpha(accent, 72%)", "bg.surface")
	tEdgeFg      = theme.Declare("diagram.edge.fg", "fg.muted")
	tLabelBg     = theme.Declare("diagram.label.bg", "bg.surface")
	tLabelBorder = theme.Declare("diagram.label.border", "line")
	tGroupBg     = theme.DeclareOn("diagram.group.bg", "alpha(accent, 3%)", "bg.surface")
	tGroupBorder = theme.DeclareOn("diagram.group.border", "alpha(accent, 18%)", "bg.surface")
	tGroupFg     = theme.Declare("diagram.group.fg", "fg.faint")
	tNoteBg      = theme.DeclareOn("diagram.note.bg", "alpha(amber, 9%)", "bg.surface")
	tNoteBorder  = theme.DeclareOn("diagram.note.border", "alpha(amber, 32%)", "bg.surface")
	tNoteFg      = theme.Declare("diagram.note.fg", "fg.muted")
	tLifeline    = theme.Declare("diagram.lifeline", "line.strong")
	tBlockBorder = theme.Declare("diagram.block.border", "line.strong")
	tBlockFg     = theme.Declare("diagram.block.fg", "fg.faint")
	tBlockTab    = theme.Declare("diagram.block.tab", "bg.raised")
	tActivation  = theme.DeclareOn("diagram.activation", "alpha(accent, 14%)", "bg.surface")
)

func init() {
	theme.Readable("diagram.node.fg", "diagram.node.bg", 4.5)
	theme.Readable("diagram.edge.fg", "diagram.label.bg", 4.5)
	theme.Readable("diagram.note.fg", "diagram.note.bg", 4.5)
}

// ThemePalette is the palette th's diagram tokens resolve to.
func ThemePalette(th *theme.Resolved) Palette {
	return Palette{
		NodeFill: th.C(tNodeBg), NodeStroke: th.C(tNodeBorder), NodeText: th.C(tNodeFg),
		Edge: th.C(tEdge), EdgeText: th.C(tEdgeFg),
		LabelFill: th.C(tLabelBg), LabelStroke: th.C(tLabelBorder),
		GroupFill: th.C(tGroupBg), GroupStroke: th.C(tGroupBorder), GroupText: th.C(tGroupFg),
		NoteFill: th.C(tNoteBg), NoteStroke: th.C(tNoteBorder), NoteText: th.C(tNoteFg),
		Lifeline: th.C(tLifeline), BlockStroke: th.C(tBlockBorder), BlockText: th.C(tBlockFg), BlockTabFill: th.C(tBlockTab),
		Activation: th.C(tActivation),
	}
}
