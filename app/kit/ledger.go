package kit

import (
	"fmt"
	"path/filepath"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The review ledger's tones.
var (
	TLedgerRead    = theme.Declare("ledger.read", "green")
	TLedgerSkimmed = theme.Declare("ledger.skimmed", "amber")
	TLedgerUnread  = theme.Declare("ledger.unread", "pink")
)

// ReadBar is a file's read bar in the Explorer: read, then skimmed, of its agent-written lines.
func ReadBar(c *ui.Ctx, key any, n registry.LedgerCount) {
	if n.Total() == 0 {
		return
	}
	th := c.Theme()
	tot := float64(n.Total())
	c.Box(key, ui.Style{W: ui.Px(26), Tooltip: fmt.Sprintf("%d of %d agent-written lines read", n.Read, n.Total())}, func(c *ui.Ctx) {
		widget.Meter(c, "m", float64(n.Read)/tot, widget.MeterOpts{Tone: th.C(TLedgerRead), Label: "read",
			Then: []widget.MeterPart{{Frac: float64(n.Skimmed) / tot, Tone: th.C(TLedgerSkimmed)}}})
	})
}

// LedgerTone is a count's colour: read once every line is, else unread.
func LedgerTone(th *theme.Resolved, n registry.LedgerCount) render.Color {
	if n.NotRead() == 0 {
		return th.C(TLedgerRead)
	}
	return th.C(TLedgerUnread)
}

// TouchedLedger is the review ledger of a file a request changed: its agent-written lines read, of all of them in
// the branch's change. ledgers keeps each project's for the frame.
func TouchedLedger(h plugin.Host, se core.SessionState, f core.Touched, ledgers map[string]registry.Ledger) (registry.LedgerCount, bool) {
	abs := AnchorPath(se, &core.Anchor{Project: f.Project, File: f.Path})
	within := Inside
	if rd, ok := ReadsOf(h); ok {
		within = rd.Within
	}
	for _, p := range se.Projects {
		if p.Dir == "" || !within(abs, p.Dir) {
			continue
		}
		l, ok := ledgers[p.Dir]
		if !ok {
			l, _ = registry.LedgerOf(h, p.Dir)
			ledgers[p.Dir] = l
		}
		rel, err := filepath.Rel(p.Dir, abs)
		if err != nil {
			return registry.LedgerCount{}, false
		}
		lf, found := l.File(filepath.ToSlash(rel))
		return lf.All, found
	}
	return registry.LedgerCount{}, false
}

// AnchorPath is where an anchor's file is.
func AnchorPath(se core.SessionState, a *core.Anchor) string {
	if filepath.IsAbs(a.File) {
		return a.File
	}
	if a.Project == core.DocsProject {
		return filepath.Join(se.Dir, filepath.FromSlash(a.File))
	}
	p, _ := se.Primary()
	for _, q := range se.Projects {
		if q.Name == a.Project && a.Project != "" {
			p = q
		}
	}
	return filepath.Join(p.Dir, filepath.FromSlash(a.File))
}
