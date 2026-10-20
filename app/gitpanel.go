package app

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// gitPanel is a project's Git panel (01h): its branch and what to do with it, what's uncommitted with the commit
// box, and what's left to push. It reads the session's projects as the Ship page does.
func (x *shipPlugin) gitPanel(c *ui.Ctx, project string) {
	se, p, ok := x.here(project)
	c.Column("git", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 6, ScrollY: true}, func(c *ui.Ctx) {
		if !ok {
			shipEmpty(c, "Git is a session's project's: open a directory or a session.")
			return
		}
		widget.Async(c, "load", x.loadState(c, se.ID, p), widget.AsyncOpts{Label: "Reading " + p, Retry: func() { x.refresh(se.ID, true) },
			Help: "git/ship#git"}, func(c *ui.Ctx) {
			c.Column("body", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) { x.gitPanelBody(c, se, p) })
		})
	})
}

// gitPanelBody is the Git panel once the project's been read.
func (x *shipPlugin) gitPanelBody(c *ui.Ctx, se core.SessionState, p string) {
	th := c.Theme()
	s, _ := x.ship(se.ID, p)
	if s.GitError != "" {
		shipEmpty(c, s.GitError)
		return
	}
	x.gitBranch(c, se, s)
	if s.ReadOnly {
		shipEmpty(c, p+" is read-only here: nothing to commit.")
		return
	}
	c.Box("sp", ui.Style{H: ui.Px(4)}, nil)
	if len(s.Files) == 0 {
		shipCap(c, "CHANGES")
		c.Text("clean", "Nothing uncommitted.", uiFont(12, th.C(theme.FgFaint)))
	} else {
		shipCap(c, fmt.Sprintf("CHANGES · %d", len(s.Files)))
		f := x.commitForm(se.ID, s.Project)
		x.fileRows(c, se, s, f, true)
		x.commitBox(c, se, s, f)
	}
	if len(s.Unpushed) > 0 {
		c.Box("sp2", ui.Style{H: ui.Px(4)}, nil)
		shipCap(c, fmt.Sprintf("TO PUSH · %d", len(s.Unpushed)))
		for i, cm := range s.Unpushed {
			c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Clip: true}, func(c *ui.Ctx) {
				c.Text("h", cm.Hash, monoFont(11, th.C(tShipHash)))
				c.Text("s", cm.Subject, monoFont(11, th.C(theme.FgMuted)))
			})
		}
	}
}

// gitBranch is the branch, where it stands against its base, and the steps on it: rename, rebase, push.
func (x *shipPlugin) gitBranch(c *ui.Ctx, se core.SessionState, s shipRead) {
	th := c.Theme()
	key := formKey(se.ID, s.Project)
	if rf := x.renaming[key]; rf != nil {
		c.Column("rename", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			r := widget.Input(c, "name", &rf.name, widget.InputOpts{Label: "Branch name", Placeholder: "A new name for " + s.Branch, Help: "git/ship#branch"})
			c.Row("buttons", ui.Style{Gap: 8}, func(c *ui.Ctx) {
				named := strings.TrimSpace(rf.name) != ""
				if widget.Button(c, "rename", "Rename", x.stepOpts(se.ID, s.Project, "Renaming", widget.ButtonOpts{Primary: true, Help: "git/ship#branch",
					Disabled: !named})) || r.Submitted && named && x.doing(se.ID, s.Project) == "" {
					x.run("ship.rename", registry.Args{"project": s.Project, "name": rf.name})
				}
				if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Help: "git/ship#branch"}) {
					delete(x.renaming, key)
				}
			})
		})
	} else {
		c.Row("branch", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("b", cmpOr(s.Branch, "no branch (detached)"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
		})
	}
	parts := []string{"→ " + cmpOr(s.Base, "?"), counted(s.Commits, "commit")}
	if s.Behind > 0 {
		parts = append(parts, fmt.Sprintf("%d behind", s.Behind))
	}
	if s.Replaced > 0 {
		parts = append(parts, fmt.Sprintf("%d rewritten", s.Replaced))
	}
	if len(s.Conflicts) > 0 {
		parts = append(parts, "conflicts in "+strings.Join(s.Conflicts, ", "))
	}
	c.Row("sub", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		c.Text("s", strings.Join(parts, " · "), monoFont(10.5, th.C(theme.FgFaint)))
	})
	if s.ReadOnly {
		return
	}
	c.Row("sync", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		if widget.Button(c, "rebase", "Rebase on "+cmpOr(s.Base, "base"), x.stepOpts(se.ID, s.Project, "Rebasing", widget.ButtonOpts{Help: "git/ship#sync",
			Disabled: !s.Remote, Primary: s.Behind > 0})) {
			x.run("ship.sync", registry.Args{"project": s.Project})
		}
		c.Row("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.IconButton(c, "refresh", "↻", widget.IconButtonOpts{Label: "Read again, fetching origin", Help: "git/ship#git"}) {
			x.run("ship.refresh", nil)
		}
	})
	c.Row("steps", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		if widget.Button(c, "push", pushLabel(s), x.stepOpts(se.ID, s.Project, "Pushing", widget.ButtonOpts{Help: "git/ship#push",
			Disabled: !s.Remote || s.Branch == "" || s.Pushed && len(s.Unpushed) == 0, Primary: len(s.Unpushed) > 0 && s.Behind == 0})) {
			x.run("ship.push", registry.Args{"project": s.Project})
		}
		if x.renaming[key] == nil && widget.Button(c, "rename", "Rename…", x.stepOpts(se.ID, s.Project, "Renaming", widget.ButtonOpts{Help: "git/ship#branch",
			Disabled: s.Branch == ""})) {
			x.run("ship.rename", registry.Args{"project": s.Project})
		}
		c.Row("gap", ui.Style{W: ui.Flex(1)}, nil)
		x.gitMore(c, se, s)
	})
	if x.doing(se.ID, s.Project) == mergeStep {
		widget.Busy(c, "merging", widget.LoadState{Phase: widget.Pending}, widget.BusyOpts{Label: "Merging into " + s.Default, Help: "git/ship#merge-default"})
	}
	if f := x.failed(se.ID, s.Project); f != "" && x.doing(se.ID, s.Project) == "" {
		widget.Wrap(c, "failed", f, 280, monoFont(10.5, th.C(theme.Error)))
	}
}

// gitMore is the branch's less used steps, in a menu: merging into the default branch without a PR.
func (x *shipPlugin) gitMore(c *ui.Ctx, se core.SessionState, s shipRead) {
	into := cmpOr(s.Default, "the default branch")
	off := s.Default == "" || s.Branch == "" || s.Branch == s.Default || x.doing(se.ID, s.Project) != ""
	items := []widget.MenuItem{{Label: "Merge into " + into + "…", Detail: "no PR", Disabled: off, Help: "git/ship#merge-default"}}
	if widget.MenuButton(c, "more", items, widget.MenuButtonOpts{Label: "More git actions", Glyph: "⋯", Help: "git/ship#merge-default"}) == 0 {
		x.run("ship.merge-default", registry.Args{"project": s.Project})
	}
}
