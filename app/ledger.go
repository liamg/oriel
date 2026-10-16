package app

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// How long an agent-written line must stay on screen in the diff to count: skimmed after ledgerSkim, read after
// ledgerRead plus ledgerReadLine for each unread agent line showing with it (a screenful takes longer), at most
// ledgerReadMax. Less than ledgerSkim, scrolling past, counts for nothing.
var (
	ledgerSkim     = 400 * time.Millisecond
	ledgerRead     = time.Second
	ledgerReadLine = 150 * time.Millisecond
	ledgerReadMax  = 8 * time.Second
)

var (
	tLedgerRead    = theme.Declare("ledger.read", "green")
	tLedgerSkimmed = theme.Declare("ledger.skimmed", "amber")
	tLedgerUnread  = theme.Declare("ledger.unread", "pink")
	tLedgerGate    = theme.Declare("ledger.gate", "accent")
)

// ledgerGateAuto is ledger.gate's default: the session kind's gate.
const ledgerGateAuto = "auto"

// activateLedger registers the review ledger (agents.md, "Guardrails"): the query, the ledger.gate option, the
// commit gate and the diff's header bar.
func (x *diffPlugin) activateLedger() {
	h := x.h
	x.add(h.Option(registry.OptionSpec{Name: "ledger.gate", Section: "git", Type: registry.Enum, Values: []string{ledgerGateAuto, core.GateHard, core.GateSoft, core.GateOff},
		Default: ledgerGateAuto, Scopes: registry.ScopeUser | registry.ScopeProject | registry.ScopeSession,
		Help: "The commit gate while agent-written lines are unread: auto, by the session's kind (hard for work and plan, soft for jobs, off for the rest); hard, blocked until commit anyway; soft, one confirmation; off."}))
	x.add(h.Contribute(registry.ReviewLedger{Name: "diff", Order: 100, Get: x.ledger, Source: "builtin:diff"}))
	x.add(h.Contribute(registry.CommitGate{Name: "ledger", Label: "Agent lines reviewed", Order: 10, Check: x.gateCheck, Source: "builtin:diff"}))
	x.add(h.Contribute(registry.ShipGate{Name: "ledger", Title: "Agent lines reviewed", Order: 30, Check: x.shipGate, Source: "builtin:diff"}))
	x.add(h.View(DiffHeader+"10-ledger", registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "ledger", Fn: func(c *ui.Ctx) { x.ledgerBar(c, vc.Instance) }}
	})))
}

// agentTexts are the lines a session's agent added to a project's files by its edits, by file and trimmed text,
// from the transcript: what makes a line not committed yet the agent's.
type agentTexts struct {
	n, diffs int
	byFile   map[string]map[string]bool
}

// agentTextsFor is root's session's agent's added lines by file; nil outside a session.
func (x *diffPlugin) agentTextsFor(root string) map[string]map[string]bool {
	se, _, ok := x.session(root)
	if !ok {
		return nil
	}
	diffs := 0
	for _, it := range se.Transcript {
		if it.Diff != nil {
			diffs += 1 + len(it.Diff.Lines)
		}
	}
	k := string(se.ID) + "\x00" + root
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.texts == nil {
		x.texts = map[string]agentTexts{}
	}
	if t, ok := x.texts[k]; ok && t.n == len(se.Transcript) && t.diffs == diffs {
		return t.byFile
	}
	t := agentTexts{n: len(se.Transcript), diffs: diffs, byFile: map[string]map[string]bool{}}
	for _, it := range se.Transcript {
		if it.Diff == nil {
			continue
		}
		p := filepath.FromSlash(it.Diff.Path)
		if filepath.IsAbs(p) {
			r, err := filepath.Rel(root, p)
			if err != nil || strings.HasPrefix(r, "..") {
				continue
			}
			p = r
		}
		file := filepath.ToSlash(p)
		for _, l := range it.Diff.Lines {
			if l.Op != "+" || strings.TrimSpace(l.Text) == "" {
				continue
			}
			if t.byFile[file] == nil {
				t.byFile[file] = map[string]bool{}
			}
			t.byFile[file][strings.TrimSpace(l.Text)] = true
		}
	}
	x.texts[k] = t
	return t.byFile
}

// agentWritten says an added line is the agent's: blame says so, or the open buffer's authors; or, not committed
// yet and with no author known, the agent's edits added that text. Blank lines don't count.
func agentWritten(l gitx.DiffLine, a addedLine, texts map[string]bool) bool {
	t := strings.TrimSpace(l.Text)
	if l.Op != '+' || t == "" {
		return false
	}
	return a.who == "agent" || a.who == "uncommitted" && texts[t]
}

// eachAgentLine calls fn with every agent-written line of a file's diff and its state in r.
func eachAgentLine(d *diffFile, texts map[string]bool, r core.Review, fn func(hunk, k int, a addedLine, s core.LineState)) {
	for i, h := range d.hunks {
		for k, l := range h.Lines {
			if a := h.adds[k]; agentWritten(l, a, texts) {
				fn(i, k, a, r.State(a.change, a.key))
			}
		}
	}
}

func countLine(c *registry.LedgerCount, s core.LineState) {
	switch s {
	case core.LineRead:
		c.Read++
	case core.LineSkimmed:
		c.Skimmed++
	default:
		c.Unread++
	}
}

// ledger is root's review ledger, from each changed file's diff (made off the loop, so it's Ready once they all
// are) and the session's reviews.
func (x *diffPlugin) ledger(root string) registry.Ledger {
	l := registry.Ledger{Root: root}
	if x.sh.git == nil {
		return l
	}
	tree := x.sh.git.tree(root)
	gs := tree.state()
	if gs == nil || tree.root() == "" {
		return l
	}
	root = tree.root()
	x.mu.Lock()
	key := ledgerKey{gs: gs, gen: x.gen, seq: x.h.State().Seq}
	c, ok := x.ledgers[root]
	x.mu.Unlock()
	if ok && c.key == key {
		return c.l
	}
	l.Root, l.Ready = root, true
	texts := x.agentTextsFor(root)
	for _, f := range gs.Files(gitx.Changed) {
		d, current := x.diffFor(tree, gs, f, false)
		if !current {
			l.Ready = false // a gate waits for the diff the file has now
		}
		if d == nil {
			continue
		}
		lf := registry.LedgerFile{Path: f.Path}
		eachAgentLine(d, texts[f.Path], x.review(root, f.Path), func(_, _ int, a addedLine, s core.LineState) {
			countLine(&lf.All, s)
			if !a.committed {
				countLine(&lf.Uncommitted, s)
			}
		})
		if lf.All.Total() > 0 {
			l.Files = append(l.Files, lf)
			l.All, l.Uncommitted = l.All.Add(lf.All), l.Uncommitted.Add(lf.Uncommitted)
		}
	}
	x.mu.Lock()
	if x.ledgers == nil {
		x.ledgers = map[string]cachedLedger{}
	}
	x.ledgers[root] = cachedLedger{key: key, l: l}
	x.mu.Unlock()
	return l
}

// ledgerKey is everything a ledger is made from: the git reading, the diffs made and local reviews (gen), and the
// sessions' reviews and transcripts (the store's seq). The Explorer asks every frame; it's made again only when one
// of them changes.
type ledgerKey struct {
	gs  *gitx.State
	gen uint64
	seq uint64
}

type cachedLedger struct {
	key ledgerKey
	l   registry.Ledger
}

// shipGate is the ledger as a "before this ships" gate: every agent-written line of the session's projects read.
func (x *diffPlugin) shipGate(id string) registry.Gate {
	se, ok := x.h.Session(core.SessionID(id))
	if !ok {
		return registry.Gate{}
	}
	var n registry.LedgerCount
	ready := true
	for _, p := range se.Snapshot().Projects {
		if l, ok := registry.LedgerOf(x.h, p.Dir); ok {
			n, ready = n.Add(l.All), ready && l.Ready
		}
	}
	switch {
	case n.Total() == 0 && !ready:
		return registry.Gate{State: registry.GatePending, Note: "counting"}
	case n.Total() == 0:
		return registry.Gate{}
	case n.NotRead() == 0:
		return registry.Gate{State: registry.GatePass, Note: fmt.Sprintf("%d / %d", n.Read, n.Total())}
	}
	return registry.Gate{State: registry.GateWarn, Note: fmt.Sprintf("%d / %d", n.Read, n.Total())}
}

// gateMode is the commit gate for root: ledger.gate where it's set, else its session kind's; off outside a session.
func (x *diffPlugin) gateMode(root string) string {
	at := registry.At{Project: root}
	se, _, ok := x.session(root)
	if ok {
		at.Session = string(se.ID)
	}
	if v, _, _ := x.h.Options().Get("ledger.gate", at); v != nil && v != ledgerGateAuto {
		return fmt.Sprint(v)
	}
	if ok {
		return cmp.Or(se.CommitGate, core.DefaultGate(se.Kind))
	}
	return core.GateOff
}

// gateCheck is the ledger's commit gate: the agent-written lines the commit's files have that aren't committed yet
// must all be read.
func (x *diffPlugin) gateCheck(c registry.CommitCheck) registry.GateResult {
	l, _ := registry.LedgerOf(x.h, c.Root)
	n := l.Of(c.Files, true)
	res := registry.GateResult{Mode: x.gateMode(c.Root), Pass: n.NotRead() == 0, Pending: !l.Ready, Done: n.Read, Total: n.Total(), Unread: n.NotRead()}
	if res.Mode == core.GateOff {
		res.Pass = true
	}
	if n.NotRead() > 0 {
		res.Reason = fmt.Sprintf("%d agent-written %s unread", n.NotRead(), plural(n.NotRead(), "line", "lines"))
		if n.Skimmed > 0 {
			res.Reason += fmt.Sprintf(" (%d only skimmed)", n.Skimmed)
		}
		res.Trailer = fmt.Sprintf("Unreviewed-Agent-Lines: %d", n.NotRead())
		res.Review, res.ReviewArgs = "diff.open", registry.Args{"path": c.Root}
		res.ReviewLabel = fmt.Sprintf("Review %d %s", n.NotRead(), plural(n.NotRead(), "line", "lines"))
	}
	return res
}

// track counts agent-written lines on screen in the diff as skimmed, then read, as they stay there (agents.md: "until
// you've seen it in the diff (scrolled past it, or marked its hunk reviewed)").
func (x *diffPlugin) track(c *ui.Ctx, st *diffState, body ui.ID) {
	vp, ok := c.Rect(body)
	if !ok {
		return
	}
	now := c.Now()
	type shown struct {
		file, key string
		state     core.LineState
	}
	var lines []shown
	texts := x.agentTextsFor(st.root)
	for file, hs := range st.hunks {
		d := st.drawn[file]
		if d == nil || len(d.hunks) != len(hs) {
			continue
		}
		eachAgentLine(d, texts[file], x.review(st.root, file), func(i, k int, a addedLine, s core.LineState) {
			if s == core.LineRead {
				return
			}
			id, ok := st.ids[diffSel{file: file, hunk: i, line: k}]
			if !ok {
				return
			}
			if r, laid := c.Rect(id); laid && r.Dy() > 0 && r.Min.Y >= vp.Min.Y && r.Max.Y <= vp.Max.Y {
				lines = append(lines, shown{file: file, key: a.key, state: s})
			}
		})
	}
	if st.seen == nil {
		st.seen, st.sent = map[string]time.Time{}, map[string]core.LineState{}
	}
	need := min(ledgerRead+time.Duration(len(lines))*ledgerReadLine, ledgerReadMax)
	read, skimmed := map[string][]string{}, map[string][]string{}
	on := map[string]bool{}
	var wake time.Time
	next := func(t time.Time) {
		if wake.IsZero() || t.Before(wake) {
			wake = t
		}
	}
	for _, l := range lines {
		k := l.file + "\x00" + l.key
		on[k] = true
		since, ok := st.seen[k]
		if !ok {
			since = now
			st.seen[k] = now
		}
		age := now.Sub(since)
		switch {
		case age >= need:
			if st.sent[k] < core.LineRead {
				read[l.file] = append(read[l.file], l.key)
				st.sent[k] = core.LineRead
			}
		case age >= ledgerSkim:
			if l.state == core.LineUnread && st.sent[k] < core.LineSkimmed {
				skimmed[l.file] = append(skimmed[l.file], l.key)
				st.sent[k] = core.LineSkimmed
			}
			next(since.Add(need))
		default:
			next(since.Add(ledgerSkim))
		}
	}
	for k := range st.seen {
		if !on[k] {
			delete(st.seen, k)
		}
	}
	for file := range mergeKeys(read, skimmed) {
		if err := x.setSeen(st.root, file, read[file], skimmed[file]); err != nil {
			x.h.Log().Warn("diff", "err", err)
		}
	}
	if !wake.IsZero() {
		c.WakeAt(wake)
	}
}

func mergeKeys(a, b map[string][]string) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// ledgerBar is the diff's REVIEW LEDGER (06): the agent-written lines read of all of them, a bar of read and
// skimmed, the commit gate and Commit.
func (x *diffPlugin) ledgerBar(c *ui.Ctx, root string) {
	l, _ := registry.LedgerOf(x.h, root)
	n := l.All
	if n.Total() == 0 {
		return
	}
	th := c.Theme()
	mode := x.gateMode(root)
	const help = "files/diff#ledger"
	c.Row("ledger", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(16, 0), Gap: 14, Cross: ui.Center, Fill: th.C(tDiffFiles), Role: ui.RoleGroup,
		Label: "Review ledger", Help: help}, func(c *ui.Ctx) {
		c.Text("k", "REVIEW LEDGER", kickerFont(th.C(tLedgerGate)))
		c.Row("read", ui.Style{Gap: 5, Cross: ui.Center, Role: ui.RoleText,
			Label: fmt.Sprintf("You have read %d / %d agent-written lines", n.Read, n.Total())}, func(c *ui.Ctx) {
			c.Text("a", "You have read", uiFont(13, th.C(theme.Fg)))
			c.Text("n", fmt.Sprint(n.Read), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(tLedgerRead)})
			c.Text("of", fmt.Sprintf("/ %d", n.Total()), uiFont(13, th.C(theme.FgFaint)))
			c.Text("b", "agent-written lines", uiFont(13, th.C(theme.Fg)))
		})
		c.Box("bar", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(4, 0)}, func(c *ui.Ctx) {
			tot := float64(n.Total())
			widget.Meter(c, "m", float64(n.Read)/tot, widget.MeterOpts{Tone: th.C(tLedgerRead), Height: 5, Label: "read",
				Then: []widget.MeterPart{{Frac: float64(n.Skimmed) / tot, Tone: th.C(tLedgerSkimmed)}}})
		})
		if n.Skimmed > 0 {
			c.Text("sk", fmt.Sprintf("%d skimmed", n.Skimmed), monoFont(10.5, th.C(theme.FgFaint)))
		}
		gate, tone := "", th.C(tLedgerGate)
		switch {
		case n.NotRead() == 0:
			gate, tone = "✓ ALL READ", th.C(tLedgerRead)
		case mode == core.GateHard:
			gate = "⏻ COMMIT GATED UNTIL 100%"
		case mode == core.GateSoft:
			gate = "COMMIT ASKS FIRST"
		}
		if gate != "" {
			widget.Chip(c, "gate", gate, tone, help)
		}
		if widget.Button(c, "commit", "Commit", widget.ButtonOpts{Ghost: true, Help: help}) {
			if err := x.h.Run(context.Background(), "git.commit", registry.Args{"path": root}); err != nil {
				x.h.Notify(core.Notice{ID: "git.commit", Severity: core.SevError, Key: true, Text: err.Error()})
			}
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// unreadChip is a file's "UNREAD · 39 LINES" in the diff, while it's folded.
func unreadChip(c *ui.Ctx, n int) {
	if n > 0 {
		widget.Chip(c, "unread", fmt.Sprintf("unread · %d %s", n, plural(n, "line", "lines")), c.Theme().C(tLedgerUnread), "files/diff#ledger")
	}
}

// readBar is a file's read bar in the Explorer: read, then skimmed, of its agent-written lines.
func readBar(c *ui.Ctx, key any, n registry.LedgerCount) {
	if n.Total() == 0 {
		return
	}
	th := c.Theme()
	tot := float64(n.Total())
	c.Box(key, ui.Style{W: ui.Px(26), Tooltip: fmt.Sprintf("%d of %d agent-written lines read", n.Read, n.Total())}, func(c *ui.Ctx) {
		widget.Meter(c, "m", float64(n.Read)/tot, widget.MeterOpts{Tone: th.C(tLedgerRead), Label: "read",
			Then: []widget.MeterPart{{Frac: float64(n.Skimmed) / tot, Tone: th.C(tLedgerSkimmed)}}})
	})
}

// ledgerTone is a count's colour: read once every line is, else unread.
func ledgerTone(th *theme.Resolved, n registry.LedgerCount) render.Color {
	if n.NotRead() == 0 {
		return th.C(tLedgerRead)
	}
	return th.C(tLedgerUnread)
}

// touchedLedger is the review ledger of a file a request changed: its agent-written lines read, of all of them in
// the branch's change. ledgers keeps each project's for the frame.
func touchedLedger(h plugin.Host, se core.SessionState, f core.Touched, ledgers map[string]registry.Ledger) (registry.LedgerCount, bool) {
	abs := anchorPath(se, &core.Anchor{Project: f.Project, File: f.Path})
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
