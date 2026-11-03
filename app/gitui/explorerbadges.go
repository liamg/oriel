package gitui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// fileBadge is a file badge and its counts for the session in front.
type fileBadge struct {
	b      uireg.FileBadge
	counts map[string]int
}

// readBadges asks each file badge for its counts, once a frame.
func (e *Explorer) readBadges() {
	e.badges = e.badges[:0]
	for _, b := range uireg.FileBadges(e.h) {
		if b.Counts == nil {
			continue
		}
		var counts map[string]int
		_ = registry.Call(func() error { counts = b.Counts(e.session()); return nil })
		if len(counts) > 0 {
			e.badges = append(e.badges, fileBadge{b, counts})
		}
	}
}

// badgeMarks are the file badges' marks on a row: a file's counts, or a folder's files' summed.
func (e *Explorer) badgeMarks(c *ui.Ctx, abs string, dir bool) {
	for _, fb := range e.badges {
		n := fb.counts[abs]
		if dir {
			n = 0
			prefix := abs + string(filepath.Separator)
			for p, k := range fb.counts {
				if strings.HasPrefix(p, prefix) {
					n += k
				}
			}
		}
		if n == 0 {
			continue
		}
		tip := fmt.Sprint(n, " ", fb.b.Noun)
		if n != 1 {
			tip += "s"
		}
		c.Box("badge."+fb.b.Name, ui.Style{Tooltip: tip, Help: fb.b.Help, Role: ui.RoleText, Label: tip}, func(c *ui.Ctx) {
			c.Text("t", fmt.Sprintf("%s%d", fb.b.Glyph, n), kit.MonoFont(10.5, kit.TokenColour(c.Theme(), fb.b.Tone, c.Theme().C(theme.FgMuted))))
		})
	}
}
