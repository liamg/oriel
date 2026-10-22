package gitui

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ledgerTimes are how long an agent-written line must stay on screen in the diff to count: skimmed after skim,
// read after read plus line for each unread agent line showing with it (a screenful takes longer), at most max.
// Less than skim, scrolling past, counts for nothing.
type ledgerTimes struct{ skim, read, line, max time.Duration }

var defaultLedgerTimes = ledgerTimes{skim: 400 * time.Millisecond, read: time.Second, line: 150 * time.Millisecond, max: 8 * time.Second}

var (
	tLedgerGate = theme.Declare("ledger.gate", "accent")
)

// ledgerGateAuto is ledger.gate's default: the session kind's gate.
const ledgerGateAuto = "auto"

// activateLedger registers the review ledger: the query, the ledger.gate option, the
// commit gate and the diff's header bar.
func (x *Diff) activateLedger() {
	h := x.h
	x.Add(h.Option(registry.OptionSpec{Name: "ledger.gate", Section: "git", Type: registry.Enum, Values: []string{ledgerGateAuto, core.GateHard, core.GateSoft, core.GateOff},
		Default: ledgerGateAuto, Scopes: registry.ScopeUser | registry.ScopeProject | registry.ScopeSession,
		Help: "The commit gate while agent-written lines are unread: auto, by the session's kind (hard for work and plan, soft for jobs, off for the rest); hard, blocked until commit anyway; soft, one confirmation; off."}))
	x.Add(h.Contribute(registry.ReviewLedger{Name: "diff", Order: 100, Get: x.ledger, Source: "builtin:diff"}))
	x.Add(h.Contribute(registry.CommitGate{Name: "ledger", Label: "Agent lines reviewed", Order: 10, Check: x.gateCheck, Source: "builtin:diff"}))
	x.Add(h.Contribute(registry.ShipGate{Name: "ledger", Title: "Agent lines reviewed", Order: 30, Check: x.shipGate, Source: "builtin:diff"}))
	x.Add(h.View(kit.DiffHeader+"10-ledger", uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
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
func (x *Diff) agentTextsFor(root string) map[string]map[string]bool {
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

// ledger is root's review ledger, from changed files' diffs (made off the loop, so it's Ready once they all are)
// and the session's reviews.
func (x *Diff) ledger(root string) registry.Ledger {
	l := registry.Ledger{Root: root}
	tree := kit.GitOf(x.h).Tree(root)
	gs := tree.State()
	if gs == nil || tree.Root() == "" {
		return l
	}
	root = tree.Root()
	x.mu.Lock()
	key := ledgerKey{gs: gs, gen: x.gen, seq: x.h.State().Seq}
	reviews := x.reviews
	c, ok := x.ledgers[root]
	paced := ok && !c.l.Ready && c.key.gs == key.gs && c.key.seq == key.seq && c.reviews == reviews &&
		time.Since(c.made) < ledgerGap
	if paced && !c.waking {
		c.waking = true
		x.ledgers[root] = c
		time.AfterFunc(ledgerGap-time.Since(c.made), x.h.Redraw)
	}
	x.mu.Unlock()
	if ok && c.key == key || paced {
		return c.l
	}
	// In a session, the files an agent can have written are diffed and blamed for its gates; outside one, the ledger
	// counts the diffs made already, of the files opened, and starts none.
	_, _, inSession := x.session(root)
	var touched map[string]bool
	if inSession {
		var known bool
		if touched, known = x.agentTouched(root, tree.Status().Repo(), gs); !known {
			return l // not Ready: it's made once the branch's agent commits are read
		}
	}
	l.Root, l.Ready = root, true
	texts := x.agentTextsFor(root)
	for _, f := range gs.Files(gitx.Changed) {
		var d *diffFile
		switch {
		case inSession && (touched[f.Path] || len(texts[f.Path]) > 0):
			var current bool
			if d, current = x.diffFor(tree, gs, f, false, true); !current || !d.blamed {
				l.Ready = false // a gate waits for the diff the file has now
			}
		case !inSession:
			d = x.made(root, f.Path)
		}
		if d == nil {
			continue
		}
		lf := x.ledgerFile(diffKey{root, f.Path}, d, key.seq, reviews, func() registry.LedgerFile {
			lf := registry.LedgerFile{Path: f.Path}
			eachAgentLine(d, texts[f.Path], x.review(root, f.Path), func(_, _ int, a addedLine, s core.LineState) {
				countLine(&lf.All, s)
				if !a.committed {
					countLine(&lf.Uncommitted, s)
				}
			})
			return lf
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
	x.ledgers[root] = cachedLedger{key: key, reviews: reviews, made: time.Now(), l: l}
	x.mu.Unlock()
	return l
}

// agentTouched is the files the branch's agent commits touched, the ones whose ledger needs blame; known is false
// while they're read, off the loop, with a frame drawn when they are.
func (x *Diff) agentTouched(root string, repo gitx.Repo, gs *gitx.State) (touched map[string]bool, known bool) {
	k := touchedKey{root: root, base: gs.BaseHash, head: gs.Head}
	if gs.BaseHash == "" || gs.Head == "" {
		return nil, true
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	t, ok := x.touched[root]
	if ok && t.key == k && t.read {
		return t.files, true
	}
	if !ok || t.key != k {
		if x.touched == nil {
			x.touched = map[string]touchedFiles{}
		}
		x.touched[root] = touchedFiles{key: k}
		x.busy.Add(1)
		go func() {
			defer x.busy.Done()
			files, err := repo.AgentTouched(context.Background(), gs.BaseHash)
			if err != nil {
				x.h.Log().Warn("reading the branch's agent commits", "root", root, "err", err)
				files = nil
			}
			x.mu.Lock()
			if x.touched[root].key == k {
				x.touched[root] = touchedFiles{key: k, files: files, read: true}
				x.gen++
			}
			x.mu.Unlock()
			x.h.Redraw()
		}()
	}
	return nil, false
}

type touchedKey struct{ root, base, head string }

type touchedFiles struct {
	key   touchedKey
	files map[string]bool
	read  bool
}

// ledgerGap is the least time between making a ledger again while its diffs are still being made: thousands
// landing one by one remake it at this pace, not once each.
const ledgerGap = 250 * time.Millisecond

// ledgerPart is a file's share of its ledger, and what it was counted from.
type ledgerPart struct {
	d       *diffFile
	seq     uint64
	reviews uint64
	lf      registry.LedgerFile
}

// ledgerFile is k's share of its ledger, counted again only when its diff, the sessions or the local reviews change.
func (x *Diff) ledgerFile(k diffKey, d *diffFile, seq, reviews uint64, count func() registry.LedgerFile) registry.LedgerFile {
	x.mu.Lock()
	p, ok := x.parts[k]
	x.mu.Unlock()
	if ok && p.d == d && p.seq == seq && p.reviews == reviews {
		return p.lf
	}
	p = ledgerPart{d: d, seq: seq, reviews: reviews, lf: count()}
	x.mu.Lock()
	if x.parts == nil {
		x.parts = map[diffKey]ledgerPart{}
	}
	x.parts[k] = p
	x.mu.Unlock()
	return p.lf
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
	key     ledgerKey
	reviews uint64
	made    time.Time
	waking  bool // a frame is set for when it can be made again
	l       registry.Ledger
}

// shipGate is the ledger as a "before this ships" gate: every agent-written line of the session's projects read.
func (x *Diff) shipGate(id string) registry.Gate {
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
func (x *Diff) gateMode(root string) string {
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
func (x *Diff) gateCheck(c registry.CommitCheck) registry.GateResult {
	l, _ := registry.LedgerOf(x.h, c.Root)
	n := l.Of(c.Files, true)
	res := registry.GateResult{Mode: x.gateMode(c.Root), Pass: n.NotRead() == 0, Pending: !l.Ready, Done: n.Read, Total: n.Total(), Unread: n.NotRead()}
	if res.Mode == core.GateOff {
		res.Pass = true
	}
	if n.NotRead() > 0 {
		res.Reason = fmt.Sprintf("%d agent-written %s unread", n.NotRead(), kit.Plural(n.NotRead(), "line", "lines"))
		if n.Skimmed > 0 {
			res.Reason += fmt.Sprintf(" (%d only skimmed)", n.Skimmed)
		}
		res.Trailer = fmt.Sprintf("Unreviewed-Agent-Lines: %d", n.NotRead())
		res.Review, res.ReviewArgs = "diff.open", registry.Args{"path": c.Root}
		res.ReviewLabel = fmt.Sprintf("Review %d %s", n.NotRead(), kit.Plural(n.NotRead(), "line", "lines"))
	}
	return res
}

// track counts agent-written lines on screen in the diff as skimmed, then read, as they stay there. A line is unread
// until it's been scrolled past in the diff or its hunk is marked reviewed.
func (x *Diff) track(c *ui.Ctx, st *diffState, body ui.ID) {
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
	lt := x.ledgerTimes
	need := min(lt.read+time.Duration(len(lines))*lt.line, lt.max)
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
		case age >= lt.skim:
			if l.state == core.LineUnread && st.sent[k] < core.LineSkimmed {
				skimmed[l.file] = append(skimmed[l.file], l.key)
				st.sent[k] = core.LineSkimmed
			}
			next(since.Add(need))
		default:
			next(since.Add(lt.skim))
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

// ledgerBar is the diff's REVIEW LEDGER: the agent-written lines read of all of them, a bar of read and
// skimmed, the commit gate and Commit.
func (x *Diff) ledgerBar(c *ui.Ctx, root string) {
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
		c.Text("k", "REVIEW LEDGER", kit.KickerFont(th.C(tLedgerGate)))
		c.Row("read", ui.Style{Gap: 5, Cross: ui.Center, Role: ui.RoleText,
			Label: fmt.Sprintf("You have read %d / %d agent-written lines", n.Read, n.Total())}, func(c *ui.Ctx) {
			c.Text("a", "You have read", kit.UIFont(13, th.C(theme.Fg)))
			c.Text("n", fmt.Sprint(n.Read), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(kit.TLedgerRead)})
			c.Text("of", fmt.Sprintf("/ %d", n.Total()), kit.UIFont(13, th.C(theme.FgFaint)))
			c.Text("b", "agent-written lines", kit.UIFont(13, th.C(theme.Fg)))
		})
		c.Box("bar", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(4, 0)}, func(c *ui.Ctx) {
			tot := float64(n.Total())
			widget.Meter(c, "m", float64(n.Read)/tot, widget.MeterOpts{Tone: th.C(kit.TLedgerRead), Height: 5, Label: "read",
				Then: []widget.MeterPart{{Frac: float64(n.Skimmed) / tot, Tone: th.C(kit.TLedgerSkimmed)}}})
		})
		if n.Skimmed > 0 {
			c.Text("sk", fmt.Sprintf("%d skimmed", n.Skimmed), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		}
		gate, tone := "", th.C(tLedgerGate)
		switch {
		case n.NotRead() == 0:
			gate, tone = "✓ ALL READ", th.C(kit.TLedgerRead)
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
		widget.Chip(c, "unread", fmt.Sprintf("unread · %d %s", n, kit.Plural(n, "line", "lines")), c.Theme().C(kit.TLedgerUnread), "files/diff#ledger")
	}
}
