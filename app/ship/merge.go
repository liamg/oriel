package ship

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Merge… on a project's PR (the merge guard): its code host's blockers, the session's own and the
// ShipMerge hook's are shown first, Continue or Cancel; one the host would refuse turns Continue off. It merges at the
// head the dialog showed, with ship.merge_method's method when the repo allows it.

// MergeKeyMode is the merge dialog's keys: ⏎ merges, Esc cancels.
const MergeKeyMode = "ship.merge"

const mergeHelp = "git/ship#merge"

// ShipMerge is the ShipMerge hook's event: a pull request about to be merged from the Ship page. A veto doesn't stop it:
// its reason is one more blocker in the dialog, for the user to weigh.
type ShipMerge struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Method  string `json:"method"`
}

// mergeAsk is the merge dialog.
type mergeAsk struct {
	se       core.SessionState
	s        shipRead
	pr       registry.PR // as read when the dialog opened: its head is what's merged
	methods  []string
	method   int
	blockers []registry.MergeBlocker
	deleteBr bool
	loading  bool
	since    time.Time // when the read started
	err      error
	focus    bool
	host     registry.CodeHost
}

// state is the dialog's read as the loading widgets take it.
func (a *mergeAsk) state() widget.LoadState {
	switch {
	case a.loading:
		return widget.LoadState{Phase: widget.Pending, Since: a.since}
	case a.err != nil:
		return widget.LoadState{Phase: widget.Failed, Err: a.err}
	}
	return widget.LoadState{Phase: widget.Ready}
}

// hard are the blockers the host would refuse the merge over.
func (a *mergeAsk) hard() []registry.MergeBlocker {
	return slices.DeleteFunc(slices.Clone(a.blockers), func(b registry.MergeBlocker) bool { return !b.Hard })
}

func (x *Ship) activateMerge(h plugin.Host) {
	x.Add(h.Option(registry.OptionSpec{Name: "ship.merge_method", Section: "git", Type: registry.Enum,
		Values: []string{"repo", registry.MergeSquash, registry.MergeCommit, registry.MergeRebase}, Default: "repo",
		Scopes: registry.ScopeUser | registry.ScopeProject,
		Help:   "How Merge… merges a pull request: squash, merge or rebase; repo, the repo's own default, else squash, else what it allows."}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "ShipMerge", Fired: true, Payload: ShipMerge{},
		Doc: "A pull request about to be merged from the Ship page; a veto's reason is shown as one more reason to wait, which the user can still merge past. { session, project, number, url, title, method }"}))
	open := func(registry.Args) bool { return x.merging != nil }
	inFront := func(registry.Args) bool { _, ok := x.session(nil); return ok }
	for _, c := range []registry.Command{
		{ID: "ship.merge", Title: "Merge a project's open PR, after showing what stands in the way: {project}", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				se, p, err := x.project(a)
				if err != nil {
					return err
				}
				s, _ := x.ship(se.ID, p)
				if first, err := x.setupFirst(ctx, s); first {
					return err
				}
				return x.openMerge(se, p)
			}},
		{ID: "ship.merge.confirm", Title: "Merge the PR the merge dialog shows: past what it warns of, unless the host would refuse", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.confirmMerge() }},
		{ID: "ship.merge.cancel", Title: "Close the merge dialog without merging", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.merging = nil; return nil }},
	} {
		x.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<CR>", Command: "ship.merge.confirm", Desc: "Merge"},
		{Keys: "<Esc>", Command: "ship.merge.cancel", Desc: "Cancel"},
	} {
		k.Mode = MergeKeyMode
		x.Add(h.Keymap(k))
	}
	x.Add(h.View(kit.OverlayPrefix+"merge", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "merge", Fn: x.mergeView}
	})))
}

// openMerge opens the dialog and reads, off the loop, the PR as it is now and what stands in its way.
func (x *Ship) openMerge(se core.SessionState, p string) error {
	s, ok := x.ship(se.ID, p)
	if !ok {
		return fmt.Errorf("%s hasn't been read yet", p)
	}
	h, err := hosted(s, s.CodeHost != nil && s.CodeHost.Merge != nil && s.CodeHost.ForBranch != nil)
	if err != nil {
		return err
	}
	if !s.PR.Open() {
		return fmt.Errorf("%s has no open %s", p, h.Said().Long)
	}
	a := &mergeAsk{se: se, s: s, pr: *s.PR, focus: true, host: h}
	x.merging = a
	x.readMerge(a)
	return nil
}

// readMerge reads, off the loop, the PR as it is now and what stands in its way.
func (x *Ship) readMerge(a *mergeAsk) {
	se, s, h, p := a.se, a.s, a.host, a.s.Project
	a.loading, a.err, a.since = true, nil, time.Now()
	if u := x.h.UI(); u != nil {
		a.since = u.Now()
	}
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(x.ctx, time.Minute)
		defer cancel()
		pr, methods, blockers, err := x.mergeRead(ctx, se, s, h)
		x.h.Do(func() {
			if x.merging != a {
				return
			}
			a.loading = false
			if err != nil {
				a.err = err
				return
			}
			a.pr, a.methods, a.blockers = pr, methods.Allowed, blockers
			want, _ := registry.Option[string](x.h.Options(), "ship.merge_method", registry.At{Project: sessionProjectDir(se, p), Session: string(se.ID)})
			pick := methods.Pick(kit.Unless(want, "repo"))
			a.method = max(0, slices.Index(a.methods, pick))
			ev := &ShipMerge{Session: string(se.ID), Project: p, Number: pr.Number, URL: pr.URL, Title: pr.Title, Method: pick}
			if v, _ := x.h.Fire(context.Background(), "ShipMerge", pr.URL, ev); v.Veto {
				a.blockers = append(a.blockers, registry.MergeBlocker{Kind: "hook", Text: v.Reason})
			}
		})
		x.h.Redraw()
	})
}

// mergeRead is the PR now, the methods its repo allows, and every blocker: the host's, then the session's.
func (x *Ship) mergeRead(ctx context.Context, se core.SessionState, s shipRead, h registry.CodeHost) (registry.PR, registry.MergeMethods, []registry.MergeBlocker, error) {
	repo := s.codeRepo(se.ID)
	pr, err := x.forBranch(ctx, h, repo, s.Branch)
	if err != nil {
		return registry.PR{}, registry.MergeMethods{}, nil, err
	}
	if !pr.Open() {
		return registry.PR{}, registry.MergeMethods{}, nil, fmt.Errorf("%s's %s isn't open now", s.Project, h.Said().Short)
	}
	var methods registry.MergeMethods
	if h.MergeMethods != nil {
		if err := registry.Call(func() (err error) { methods, err = h.MergeMethods(ctx, repo, *pr); return err }); err != nil {
			return registry.PR{}, methods, nil, err
		}
	}
	if len(methods.Allowed) == 0 {
		methods.Allowed = []string{registry.MergeSquash, registry.MergeCommit, registry.MergeRebase}
	}
	var blockers []registry.MergeBlocker
	if h.Blockers != nil {
		if err := registry.Call(func() (err error) { blockers, err = h.Blockers(ctx, repo, *pr); return err }); err != nil {
			return registry.PR{}, methods, nil, err
		}
	}
	if !slices.ContainsFunc(blockers, func(b registry.MergeBlocker) bool { return b.Kind == registry.BlockThreads }) {
		blockers = append(blockers, mirroredBlockers(se, s.Project, pr.Number)...)
	}
	return *pr, methods, append(blockers, x.sessionBlockers(se, s)...), nil
}

// mirroredBlockers are a PR's reviewer threads the session mirrors from its code host and that aren't resolved there:
// what a host whose Blockers don't count them (a plugin's, say) still has unresolved.
func mirroredBlockers(se core.SessionState, project string, pr int) []registry.MergeBlocker {
	n := 0
	for _, t := range se.Threads {
		if h := t.Hosted; h != nil && !t.Outgoing && h.PR == pr && (h.Project == "" || h.Project == project) && !h.Resolved && t.Open() {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return []registry.MergeBlocker{{Kind: registry.BlockThreads, Count: n, Text: kit.Counted(n, "reviewer comment") + " unresolved"}}
}

// sessionBlockers are what Oriel knows stands in a PR's way: its checks, the PRs before it in the session's merge
// order, and the session's open threads.
func (x *Ship) sessionBlockers(se core.SessionState, s shipRead) []registry.MergeBlocker {
	var out []registry.MergeBlocker
	failing, running := 0, 0
	for _, c := range s.Checks {
		switch {
		case c.State == registry.CheckFailed:
			failing++
		case c.Pending():
			running++
		}
	}
	if failing > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockChecks, Count: failing, Text: kit.Counted(failing, "check") + " failing"})
	} else if running > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockChecks, Count: running, Text: kit.Counted(running, "check") + " still running"})
	}
	for _, o := range x.report(se.ID).ships {
		if o.Project == s.Project {
			break
		}
		if o.CodeHost != nil && !o.ReadOnly && (o.PR == nil || !o.PR.Merged) && (o.Commits > 0 || o.PR != nil) {
			out = append(out, registry.MergeBlocker{Kind: registry.BlockOrder, Text: prRef(o) + " merges first"})
		}
	}
	open := 0
	for _, t := range se.Threads {
		if t.Open() && t.Hosted == nil { // a mirrored one is its host's, counted there
			open++
		}
	}
	if open > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockOrielThreads, Count: open, Text: kit.Counted(open, "Oriel thread") + " open on the session"})
	}
	return out
}

// mergeLead is the dialog's first line: what's unresolved, by whose threads, else how many things to weigh.
func mergeLead(bs []registry.MergeBlocker) string {
	var parts []string
	for _, k := range []struct{ kind, what string }{{registry.BlockThreads, "reviewer comment"}, {registry.BlockOrielThreads, "Oriel thread"}} {
		for _, b := range bs {
			if b.Kind == k.kind && b.Count > 0 {
				parts = append(parts, kit.Counted(b.Count, k.what))
			}
		}
	}
	switch {
	case len(parts) > 0:
		return strings.Join(parts, ", ") + " unresolved"
	case len(bs) == 0:
		return "Nothing stands in the way."
	}
	return kit.Counted(len(bs), "thing") + " to weigh first"
}

// confirmMerge merges at the head the dialog read, unless the host would refuse.
func (x *Ship) confirmMerge() error {
	a := x.merging
	switch {
	case a == nil:
		return errors.New("no merge is waiting")
	case a.loading:
		return errors.New("still reading what stands in the way")
	case a.err != nil:
		return a.err
	case len(a.hard()) > 0:
		return fmt.Errorf("%s won't merge it: %s", a.s.CodeHost.Label(), a.hard()[0].Text)
	}
	x.merging = nil
	h, repo, pr := *a.s.CodeHost, a.s.codeRepo(a.se.ID), a.pr
	o := registry.MergeOpts{Head: pr.Head, DeleteBranch: a.deleteBr}
	if a.method < len(a.methods) {
		o.Method = a.methods[a.method]
	}
	return x.step(a.se.ID, a.s.Project, "Merging", func(ctx context.Context, _ host.ShipCalls) error {
		if err := registry.Call(func() error { return h.Merge(ctx, repo, pr, o) }); err != nil {
			return err
		}
		x.mergedHere(a.se.ID, a.s, pr)
		x.sawPR(repo, a.s.Branch, false)
		return nil
	}, func() {
		done := pr
		done.State, done.Merged = "closed", true
		x.h.Do(func() { x.afterMerge(a.se.ID, a.s.Project, &done) })
	})
}

// mergeView is the merge dialog while it's open.
func (x *Ship) mergeView(c *ui.Ctx) {
	a := x.merging
	if a == nil {
		return
	}
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	w := min(520, size.X-32)
	words := a.s.words()
	closed := widget.Scrim(c, "merge", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: mergeHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if a.focus {
			c.RequestFocus(box)
			a.focus = false
		}
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: MergeKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: "Merge " + words.Short, Help: mergeHelp}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", fmt.Sprintf("MERGE %s%d", words.Sigil, a.pr.Number), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14,
					Weight: typeset.Bold, Tracking: .24}, Color: th.C(theme.Fg)})
				c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", a.pr.Title, kit.MonoFont(10.5, th.C(theme.FgFaint)))
				})
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: mergeHelp}) {
					x.merging = nil
				}
			})
			widget.Rule(c, "rule")
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 16, B: 18}, Gap: 12}, func(c *ui.Ctx) {
				x.mergeBody(c, a, w-48)
			})
			widget.Rule(c, "rule2")
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 10, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: mergeHelp}) {
					x.merging = nil
					return
				}
				label := "Merge"
				if len(a.blockers) > 0 {
					label = "Continue"
				}
				if widget.Button(c, "merge", label, widget.ButtonOpts{Primary: true, Key: "↵", Help: mergeHelp,
					Disabled: a.loading || a.err != nil || len(a.hard()) > 0}) {
					x.run("ship.merge.confirm", nil)
				}
			})
		})
	})
	if closed {
		x.merging = nil
	}
}

func (x *Ship) mergeBody(c *ui.Ctx, a *mergeAsk, width float32) {
	widget.Async(c, "read", a.state(), widget.AsyncOpts{Policy: widget.Gate, Label: "Reading what stands in the way",
		Retry: func() { x.readMerge(a) }, Help: mergeHelp}, func(c *ui.Ctx) { x.mergeBlockers(c, a, width) })
}

// mergeBlockers are what stands in the way, the method and whether to delete the branch, once read.
func (x *Ship) mergeBlockers(c *ui.Ctx, a *mergeAsk, width float32) {
	th := c.Theme()
	lead := th.C(theme.Fg)
	if len(a.blockers) > 0 {
		lead = th.C(theme.Warn)
	}
	widget.Wrap(c, "lead", mergeLead(a.blockers), width, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13.5, Weight: typeset.SemiBold}, Color: lead})
	for i, b := range a.blockers {
		c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Role: ui.RoleText, Label: b.Text}, func(c *ui.Ctx) {
			glyph, tone := "!", th.C(theme.Warn)
			if b.Hard {
				glyph, tone = "✗", th.C(theme.Error)
			}
			c.Box("g", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", glyph, kit.UIFont(13, tone)) })
			c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", b.Text, kit.UIFont(12.5, th.C(theme.FgMuted))) })
			if b.Kind == registry.BlockOrielThreads {
				if widget.Button(c, "show", "Show", widget.ButtonOpts{Ghost: true, Help: mergeHelp}) {
					x.merging = nil
					x.run("picker.open", registry.Args{"source": "threads"})
				}
			} else if b.URL != "" && widget.Button(c, "show", "Show", widget.ButtonOpts{Ghost: true, Help: mergeHelp}) {
				if err := kit.OpenURL(x.h, b.URL); err != nil {
					a.err = err
				}
			}
		})
	}
	if hard := a.hard(); len(hard) > 0 {
		widget.Wrap(c, "refuse", a.s.CodeHost.Label()+" won't merge it until that's fixed.", width, kit.MonoFont(10.5, th.C(theme.Error)))
	}
	c.Row("method", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("l", "Method", kit.UIFont(12, th.C(theme.FgMuted)))
		widget.Segmented(c, "m", a.methods, &a.method, widget.SegmentedOpts{Label: "Merge method", Help: mergeHelp})
	})
	st := widget.CheckOff
	if a.deleteBr {
		st = widget.CheckOn
	}
	if widget.Check(c, "delete", st, widget.CheckOpts{Text: "Delete " + cmp.Or(a.pr.Branch, "the branch") + " after merging", Help: mergeHelp}) {
		a.deleteBr = !a.deleteBr
	}
}
