package explain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// fix is agent.fix: a request with the trouble attached, about its file and line.
func (x *Plugin) fix(ctx registry.Ctx, a registry.Args) error {
	t := registry.TroubleOf(a)
	args := registry.Args{"text": t.FixText(), "context": strings.TrimSpace(t.Subject + "\n\n" + t.Context)}
	if t.File != "" {
		args["file"], args["line"] = t.File, max(t.Line, 1)
	}
	return x.h.Run(ctx, "request.new", args)
}

// keep asks about req off the loop, its answer kept under key for Kept to show where it was asked.
func (x *Plugin) keep(key string, req registry.ExplainRequest) {
	a := &answer{loading: true, since: kit.WindowNow(x.h)}
	x.mu.Lock()
	x.kept[key] = a
	x.mu.Unlock()
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		md, by, err := registry.Explain(ctx, x.h, req)
		label := ""
		if by.Label != nil {
			registry.Call(func() error { label = by.Label(); return nil })
		}
		x.h.Do(func() {
			x.mu.Lock()
			a.text, a.err, a.loading, a.by = md, err, false, label
			x.mu.Unlock()
			if err != nil {
				x.notify(err)
			}
		})
	})
}

// keptAnswer is the answer asked for under key, if one was.
func (x *Plugin) keptAnswer(key string) (answer, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	a, ok := x.kept[key]
	if !ok {
		return answer{}, false
	}
	return *a, true
}

// Kept is the answer kit.AskTwins asked for under key, once it was: an explain card that changes nothing.
func (x *Plugin) Kept(c *ui.Ctx, key, help string) {
	a, ok := x.keptAnswer(key)
	if !ok {
		return
	}
	th := c.Theme()
	mono := func(size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
	width := float32(760)
	if r, ok := c.Rect(c.Next("explain")); ok && r.Dx() > 20 {
		width = r.Dx() - 20
	}
	kit.Popup(c, "explain", ui.Style{W: ui.Flex(1), Pad: ui.Pad(10), Gap: 6, Help: help}, func(c *ui.Ctx) {
		c.Text("label", "EXPLAIN · CHANGES NOTHING", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold,
			Tracking: .14}, Color: th.C(theme.FgFaint)})
		switch {
		case a.loading:
			widget.Busy(c, "wait", a.state(), widget.BusyOpts{Label: "Asking", Size: 11, Help: help})
		case a.err != nil:
			c.Text("err", a.err.Error(), mono(11, theme.Error))
		default:
			kit.DocText(c, x.h, "md", a.text, width)
			if a.by != "" {
				c.Text("by", a.by, mono(10, theme.FgFaint))
			}
		}
	})
}

// request asks the first explainer about a step of the card, off the loop; the answer goes in the cache.
func (x *Plugin) request(cd *explainCard, i int) {
	cr := cd.crumbs[i]
	depth := explainDepths[cd.depth]
	key := answerKey{cr.hash, depth}
	r, ok := cd.rangeOf(i)
	if !ok {
		return
	}
	snap := cd.buf.Snapshot()
	first, last := spanLines(snap, r)
	lines := fmt.Sprintf("%s:%d-%d", x.relName(cd.path), first+1, last+1)
	req := registry.ExplainRequest{Kind: "unit", Depth: depth, Files: []string{cd.path},
		Subject: fmt.Sprintf("%s (%s)", strings.Join(crumbLabels(cd.crumbs[:i+1]), " › "), lines)}
	if cr.selection {
		req.Kind = cr.kind
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The code, %s:\n```\n%s```\n", lines, codeOf(snap, r))
	if cr.context != "" {
		b.WriteString("\n" + cr.context + "\n")
	}
	if i > 0 {
		if or, ok := cd.rangeOf(0); ok {
			fmt.Fprintf(&b, "\nIt's inside %s, which reads:\n```\n%s```\n", cd.crumbs[0].label, codeOf(snap, or))
		}
	}
	threads := ""
	if depth == registry.ExplainWhy {
		threads = x.threadsOn(cd.path, first, last)
	}
	a := &answer{loading: true, since: kit.WindowNow(x.h)}
	x.mu.Lock()
	x.answers[key] = a
	x.mu.Unlock()
	path, buf, kind, start := cd.path, cd.buf, cr.kind, r.Start
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if depth == registry.ExplainWhy {
			b.WriteString(x.history(ctx, path, snap, r) + threads)
		}
		req.Context = b.String()
		md, by, err := registry.Explain(ctx, x.h, req)
		label := by.Name
		if by.Label != nil {
			registry.Call(func() error { label = by.Label(); return nil })
		}
		x.h.Do(func() {
			x.mu.Lock()
			a.text, a.err, a.loading, a.by = md, err, false, label
			if err == nil {
				x.marks = slices.DeleteFunc(x.marks, func(m *explainMark) bool {
					off, ok := m.buf.Resolve(m.start)
					if m.buf == buf && m.kind == kind && (!ok || off == start) {
						m.buf.Release(m.start)
						return true
					}
					return false
				})
				x.marks = append(x.marks, &explainMark{path: path, buf: buf, kind: kind, start: buf.Anchor(start, text.Right), hash: key.hash})
			}
			x.mu.Unlock()
			x.h.Redraw()
		})
	})
}

func crumbLabels(cs []explainCrumb) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.label)
	}
	return out
}

// history is why · history's context: the commits that last touched the lines (blame), and the session's PR.
func (x *Plugin) history(ctx context.Context, path string, snap text.Snapshot, r text.Range) string {
	var b strings.Builder
	if g := kit.GitOf(x.h).Tree(""); g.Status() != nil {
		if blame, err := g.Status().Repo().Blame(ctx, g.Rel(path), snap.Bytes()); err == nil {
			a, z := spanLines(snap, r)
			seen := map[string]bool{}
			for l := a; l <= z && l < len(blame); l++ {
				bl := blame[l]
				key := bl.Hash
				if key == "" {
					key = "uncommitted"
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				if b.Len() == 0 {
					b.WriteString("\nHistory (git blame of these lines):\n")
				}
				if bl.Hash == "" {
					b.WriteString("- not committed yet\n")
					continue
				}
				fmt.Fprintf(&b, "- %s %s, %s (%s): %s\n", bl.Hash[:min(8, len(bl.Hash))], bl.Author, bl.When.Format("2006-01-02"), bl.Kind, bl.Summary)
			}
		}
	}
	return b.String()
}

// threadsOn is the session's threads on lines [a, z] of path, and its PR, for why · history.
func (x *Plugin) threadsOn(path string, a, z int) string {
	st := x.h.State()
	s, ok := st.Session(st.Focus)
	if !ok {
		return ""
	}
	se := s.Snapshot()
	var b strings.Builder
	for _, ch := range registry.Chips(x.h, se) {
		if ch.Link != "" {
			fmt.Fprintf(&b, "\nThe session's %s: %s\n", ch.Text, ch.Link)
		}
	}
	for _, t := range se.Threads {
		if t.Anchor == nil || filepath.Clean(kit.AnchorPath(se, t.Anchor)) != filepath.Clean(path) {
			continue
		}
		l := t.Anchor.Line - 1
		if l < a || l > z {
			continue
		}
		fmt.Fprintf(&b, "\nA thread on line %d (%s): %s", t.Anchor.Line, t.Status, t.Heading())
		if last, ok := t.Last(); ok {
			fmt.Fprintf(&b, "\n  last, from %s: %s", last.Author, kit.FirstLine(last.Body))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ask is explain.ask: kept under {key} for where it was asked, a card on {file}'s lines, or {text} answered in a
// notice.
// explainAsking is the notice explain.ask shows while it waits for an answer.
const explainAsking = "explain.asking"

func (x *Plugin) ask(a registry.Args) error {
	depth := cmp.Or(a.String("depth"), registry.ExplainBrief)
	if !slices.Contains(explainDepths, depth) {
		return fmt.Errorf("no depth %q: brief, detailed or why", depth)
	}
	f, key := a.String("file"), a.String("key")
	if f != "" && key == "" {
		line := max(a.Int("line"), 1)
		ed, ok := services.EditorsOf(x.h)
		if !ok {
			return errors.New("no editor")
		}
		fs, ok := ed.Buffer(f)
		if !ok {
			if err := x.h.Run(context.Background(), "editor.open", registry.Args{"path": f, "line": line}); err != nil {
				return err
			}
			if fs, ok = ed.Buffer(f); !ok {
				return fmt.Errorf("couldn't open %s", f)
			}
		}
		snap := fs.Text.Snapshot()
		from := min(line-1, snap.Lines()-1)
		to := min(max(a.Int("end_line")-1, from), snap.Lines()-1)
		more := a.String("context")
		if k := a.String("kind"); k != "" {
			more = strings.TrimSpace("It's a " + k + ".\n" + more)
		}
		if err := x.explainRange(f, fs.Text, text.Range{Start: snap.LineStart(from), End: snap.LineEnd(to)}, a.String("kind"), a.String("subject"), more); err != nil {
			return err
		}
		x.card.depth = slices.Index(explainDepths, depth)
		x.ensure()
		return nil
	}
	req := registry.ExplainRequest{Kind: cmp.Or(a.String("kind"), "selection"), Subject: cmp.Or(a.String("subject"), "this text"), Depth: depth,
		Context: strings.TrimSpace(a.String("text") + "\n\n" + a.String("context"))}
	if req.Context == "" && f == "" {
		return errors.New("explain.ask needs a file and line, or text")
	}
	if f != "" {
		req.Files = []string{f}
	}
	if key != "" {
		x.keep(key, req)
		return nil
	}
	x.h.Notify(core.Notice{ID: explainAsking, Severity: core.SevInfo, Source: "explain", Title: "Explain: " + req.Subject,
		Text: "Asking the explainer; its answer comes here."})
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		md, _, err := registry.Explain(ctx, x.h, req)
		x.h.Do(func() {
			x.h.Dismiss(explainAsking)
			if err != nil {
				x.notify(err)
				return
			}
			x.h.Notify(core.Notice{ID: "explain.answer", Severity: core.SevInfo, Source: "explain", Title: "Explain: " + req.Subject, Text: md})
		})
	})
	return nil
}

// followUp opens a new request with the explanation and the code attached.
func (x *Plugin) followUp(ctx registry.Ctx) error {
	cd := x.card
	r, ok := cd.rangeOf(cd.sel)
	if !ok {
		return errors.New("the explained code is gone")
	}
	snap := cd.buf.Snapshot()
	ctxt := fmt.Sprintf("%s in %s\n```\n%s```", cd.breadcrumb(), x.relName(cd.path), codeOf(snap, r))
	if a := x.shown(cd); a != nil && a.text != "" {
		ctxt += "\n\nExplained as:\n" + a.text
	}
	return x.h.Run(ctx, "request.new", registry.Args{"text": "", "context": ctxt, "file": cd.path, "line": snap.PosAt(r.Start).Line + 1})
}

// pin makes the explanation a resolved thread on the lines it's about: a note that doesn't go to the agent.
func (x *Plugin) pin(ctx registry.Ctx) error {
	cd := x.card
	a := x.shown(cd)
	if a == nil || a.text == "" {
		return errors.New("there's no explanation to pin yet")
	}
	r, ok := cd.rangeOf(cd.sel)
	if !ok {
		return errors.New("the explained code is gone")
	}
	snap := cd.buf.Snapshot()
	from, to := spanLines(snap, r)
	body := a.text
	if a.by != "" {
		body += "\n\n— " + a.by
	}
	return x.h.Run(ctx, "request.comment", registry.Args{"file": cd.path, "line": from + 1, "end_line": to + 1,
		"text": strings.TrimRight(string(snap.Line(from)), "\r\n"), "title": "Explain: " + cd.breadcrumb(), "body": body,
		"status": string(core.ThreadResolved)})
}

// requestThis writes a request about the selection, the unit at the cursor, or the cursor's place.
func (x *Plugin) requestThis(ctx registry.Ctx, what string) error {
	ed, ok := services.EditorsOf(x.h)
	if !ok {
		return errors.New("no editor")
	}
	b, ok := ed.Current()
	if !ok || b.Path == "" {
		return errors.New("the editor has no file here")
	}
	path, snap := b.Path, b.Text.Snapshot()
	r, ok := ed.Selection()
	name, at := "the selection", kit.Place{}
	if ok {
		at = kit.PlaceOf(snap, r)
		x.leaveVisual()
	} else {
		units := x.unitsNow(path, b.Text)
		cur := b.Cursor
		i := text.Innermost(units, text.Range{Start: firstNonBlank(snap, cur.Line), End: firstNonBlank(snap, cur.Line)})
		if i >= 0 {
			r, name = units[i].Range, units[i].Label(snap.Bytes())
			start, end := spanLines(snap, r)
			at = kit.Place{Line: start + 1, EndLine: end + 1}
		} else {
			r, name, at = text.Range{Start: snap.LineStart(cur.Line), End: snap.LineEnd(cur.Line)}, "the code at the cursor", kit.PlaceAt(snap, cur)
		}
	}
	start, _ := spanLines(snap, r)
	where := x.relName(path) + ":" + at.String()
	prompt, ok := map[string]string{
		"review":   "Review %s (%s): what's wrong with it, or could be better?",
		"tests":    "Write tests for %s (%s).",
		"refactor": "Refactor %s (%s): ",
	}[what]
	if !ok {
		return fmt.Errorf("no request %q: review, tests or refactor", what)
	}
	attached := fmt.Sprintf("%s\n```\n%s```", where, codeOf(snap, r))
	if at.Col > 0 && at.EndCol > 0 {
		attached += fmt.Sprintf("\nthe selection:\n```\n%s\n```", snap.Slice(r))
	}
	return x.h.Run(ctx, "request.new", registry.Args{"text": fmt.Sprintf(prompt, name, where), "context": attached, "file": path, "line": start + 1})
}
