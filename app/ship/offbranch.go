package ship

import (
	"cmp"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// offBranch says a project's worktree is on a branch other than the session's, which Oriel won't push, and offers
// the two ways out: back to the session's branch, or making the one it's on the session's where that's safe.
func (x *Ship) offBranch(c *ui.Ctx, se core.SessionState, s shipRead, width float32) {
	off := s.Off
	if off == nil {
		return
	}
	th := c.Theme()
	on := cmp.Or(off.Current, "a detached HEAD")
	c.Column("off", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		c.Text("head", "Not on this session's branch", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.SemiBold},
			Color: th.C(theme.Warn)})
		widget.Wrap(c, "note", "On "+on+". Oriel pushes and raises PRs only from "+off.Session+".", width, kit.UIFont(11, th.C(theme.FgMuted)))
		c.Row("acts", ui.Style{W: ui.Flex(1), Gap: 8, Wrap: true}, func(c *ui.Ctx) {
			if widget.Button(c, "back", "Switch back to "+off.Session, x.stepOpts(se.ID, s.Project, "Switching back",
				widget.ButtonOpts{Primary: off.Refused != "", Help: "git/ship#branch"})) {
				x.run("ship.switch-back", registry.Args{"project": s.Project})
			}
			label := "Make " + cmp.Or(off.Current, "it") + " this session's branch"
			if widget.Button(c, "adopt", label, x.stepOpts(se.ID, s.Project, "Adopting", widget.ButtonOpts{Help: "git/ship#branch",
				Disabled: off.Refused != "", Why: off.Refused})) {
				x.run("ship.adopt-branch", registry.Args{"project": s.Project})
			}
		})
		if off.Refused != "" {
			widget.Wrap(c, "why", "It can't be made the session's: "+off.Refused+".", width, kit.UIFont(11, th.C(theme.FgFaint)))
		}
	})
}
