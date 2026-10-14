package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Explain this change (ui.md 07): a walkthrough of the branch's change, step by step across its files and projects.
const (
	ExplainChangeView    = "explain.change"
	ExplainChangeKeyMode = "explain.change"
)

var (
	tWalkMode     = theme.Declare("explain.change.mode", "status.reviewer")
	tWalkBarBg    = theme.Declare("explain.change.bar.bg", "bg.surface")
	tWalkDockBg   = theme.DeclareOn("explain.change.dock.bg", "alpha(accent, 3%)", "bg.surface")
	tWalkDockEdge = theme.Declare("explain.change.dock.edge", "alpha(accent, 35%)")
	tWalkStep     = theme.Declare("explain.change.step", "accent")
	tWalkProject  = theme.Declare("explain.change.project", "violet")
	tWalkNotes    = []theme.TokenID{theme.Declare("explain.note.1", "status.working"), theme.Declare("explain.note.2", "status.reviewer"),
		theme.Declare("explain.note.3", "accent")}
)

const walkHelp = "editor/pane#explain-change"

// walkthroughExt is RV-6: explain.change asks the first explainer for a walkthrough of the branch's change, as JSON
// steps (walkFormat), and shows it a step at a time: the step's file with the lines it's about lit and its notes
// beside them, and the step dock. It's thrown away once the code a step is about changes.
type walkthroughExt struct {
	disposer
	sh   *shell
	h    ext.Host
	busy sync.WaitGroup

	w     *walkthrough // on the loop only
	focus bool         // give the view focus next frame
}

// walkthrough is one walkthrough: being asked for, failed, or its steps and the one shown.
type walkthrough struct {
	loading bool
	err     error
	subject string
	files   int
	repos   int
	steps   []walkStep
	step    int
	line    int  // the line of the step's file a comment is about
	top     int  // the first line shown
	follow  bool // scroll to the line: it was moved
	by      string

	srcs   map[string]*walkSrc // the step files' text now, by path
	statAt time.Time           // when files on disk were last looked at
	cardH  map[int]float32     // each note card's height last frame
}

// walkSrc is a step file's text as it is now: an open buffer's, else the file's.
type walkSrc struct {
	src     []byte
	buf     *text.Buffer
	version uint64
	size    int64
	mod     time.Time
	hl      *sourceFile
}

func (x *walkthroughExt) Manifest() ext.Manifest { return ext.Manifest{ID: "explain.change"} }

func (x *walkthroughExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	ready := func(registry.Args) bool { return x.ready() }
	for _, c := range []registry.Command{
		{ID: "explain.change", Title: "Explain this change: a walkthrough of the branch's change across every project and file, step by step, written by the session's agent; thrown away once the code it's about changes",
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.open(ctx) }},
		{ID: "explain.change.step", Title: "Go to step {step} of the walkthrough, or {by} steps on (-1 back)", Available: ready,
			Run: func(_ registry.Ctx, a registry.Args) error { x.goTo(x.stepArg(a)); return nil }},
		{ID: "explain.change.line", Title: "Move the walkthrough's line {by} lines, for a comment", Available: ready,
			Run: func(_ registry.Ctx, a registry.Args) error { x.moveLine(a.Int("by")); return nil }},
		{ID: "explain.change.ask", Title: "Ask about this step of the walkthrough: a new request with the step attached",
			Available: func(registry.Args) bool { return x.ready() && h.Available("request.new", nil) },
			Run:       func(ctx registry.Ctx, _ registry.Args) error { return x.ask(ctx) }},
		{ID: "explain.change.comment", Title: "Comment on the walkthrough step's {line} (the one picked by default): a thread on it",
			Available: func(registry.Args) bool { return x.ready() && x.current().path != "" && h.Available("thread.new", nil) },
			Run:       func(ctx registry.Ctx, a registry.Args) error { return x.comment(ctx, a.Int("line")) }},
		{ID: "explain.change.discard", Title: "Discard the walkthrough", Available: func(registry.Args) bool { return x.w != nil },
			Run: func(registry.Ctx, registry.Args) error { x.discard(); return nil }},
		{ID: "explain.change.close", Title: "Close the walkthrough, keeping it to come back to",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "view.close", registry.Args{"id": ExplainChangeView})
			}},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "n", Keys: "<Leader>E", Command: "explain.change", Desc: "Walk through the change"},
		{Mode: ExplainChangeKeyMode, Keys: "<Left>", Command: "explain.change.step", Args: registry.Args{"by": -1}, Desc: "Previous step"},
		{Mode: ExplainChangeKeyMode, Keys: "h", Command: "explain.change.step", Args: registry.Args{"by": -1}, Desc: "Previous step"},
		{Mode: ExplainChangeKeyMode, Keys: "<Right>", Command: "explain.change.step", Args: registry.Args{"by": 1}, Desc: "Next step"},
		{Mode: ExplainChangeKeyMode, Keys: "l", Command: "explain.change.step", Args: registry.Args{"by": 1}, Desc: "Next step"},
		{Mode: ExplainChangeKeyMode, Keys: "j", Command: "explain.change.line", Args: registry.Args{"by": 1}, Desc: "Next line"},
		{Mode: ExplainChangeKeyMode, Keys: "<Down>", Command: "explain.change.line", Args: registry.Args{"by": 1}, Desc: "Next line"},
		{Mode: ExplainChangeKeyMode, Keys: "k", Command: "explain.change.line", Args: registry.Args{"by": -1}, Desc: "Previous line"},
		{Mode: ExplainChangeKeyMode, Keys: "<Up>", Command: "explain.change.line", Args: registry.Args{"by": -1}, Desc: "Previous line"},
		{Mode: ExplainChangeKeyMode, Keys: "c", Command: "explain.change.comment", Desc: "Comment on the line"},
		{Mode: ExplainChangeKeyMode, Keys: "a", Command: "explain.change.ask", Desc: "Ask about this step"},
		{Mode: ExplainChangeKeyMode, Keys: "q", Command: "explain.change.close", Desc: "Quit the walkthrough"},
		{Mode: ExplainChangeKeyMode, Keys: "<Esc>", Command: "explain.change.close", Desc: "Quit the walkthrough"},
		{Mode: ExplainChangeKeyMode, Keys: "<C-w>c", Command: "explain.change.close", Desc: "Close"},
		{Mode: ExplainChangeKeyMode, Keys: "<C-w>q", Command: "explain.change.close", Desc: "Close"},
	} {
		x.add(h.Keymap(k))
	}
	x.add(h.View(ExplainChangeView, walkView{x}))
	x.add(h.View("statusline.left.05-explain", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		if f := h.Layout().Focus; f == nil || f.ID != ExplainChangeView || !x.ready() {
			return nil
		}
		return &ui.Func{Key: "explain.change", Fn: x.statusSegment}
	})))
	x.add(h.View(DiffHeader+"90-explain-change", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "explain.change", Fn: func(c *ui.Ctx) {
			c.Row("explain", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 4), Main: ui.End}, func(c *ui.Ctx) {
				if widget.Button(c, "go", "Explain this change", widget.ButtonOpts{Disabled: !h.Available(registry.ExplainCommand, nil),
					Help: "editor/pane#explain-change"}) {
					if err := h.Run(context.Background(), "explain.change", nil); err != nil {
						h.Notify(core.Notice{ID: "explain.change", Severity: core.SevWarning, Source: "explain", Key: true, Text: err.Error()})
					}
				}
			})
		}}
	})))
	return nil
}

func (x *walkthroughExt) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

func (x *walkthroughExt) wait() { x.busy.Wait() }

func (x *walkthroughExt) ready() bool {
	return x.w != nil && !x.w.loading && x.w.err == nil && len(x.w.steps) > 0
}

// current is the step shown.
func (x *walkthroughExt) current() walkStep {
	if !x.ready() {
		return walkStep{}
	}
	return x.w.steps[x.w.step]
}

func (x *walkthroughExt) notify(sev core.Severity, msg string) {
	x.h.Notify(core.Notice{ID: "explain.change", Severity: sev, Source: "explain", Key: true, Text: msg})
}

// open shows the walkthrough, asking for one first unless there's one still good.
func (x *walkthroughExt) open(ctx context.Context) error {
	if x.w != nil {
		x.check(time.Now(), true)
	}
	if x.w == nil || x.w.err != nil {
		if err := x.start(); err != nil {
			return err
		}
	}
	x.focus = true
	return x.h.Run(ctx, "view.open", registry.Args{"id": ExplainChangeView, "place": "tab", "title": "Explain this change"})
}

// projects are the projects the change is in: the session's, else the work dir.
func (x *walkthroughExt) projects() []walkProject {
	var out []walkProject
	for _, p := range projectsHere(x.h).projects {
		if p.Dir != "" {
			out = append(out, walkProject{name: p.Name, dir: p.Dir})
		}
	}
	if len(out) == 0 && x.sh.cfg.WorkDir != "" {
		out = append(out, walkProject{name: filepath.Base(x.sh.cfg.WorkDir), dir: x.sh.cfg.WorkDir})
	}
	return out
}

// start asks the first explainer for a walkthrough of the change, off the loop.
func (x *walkthroughExt) start() error {
	if len(registry.Explainers(x.h)) == 0 {
		return errors.New("nothing can explain yet: it needs an agent")
	}
	ps := x.projects()
	if len(ps) == 0 {
		return errors.New("there's no project here to explain the change of")
	}
	open := map[string]openText{}
	if ed := x.sh.editor; ed != nil && ed.e != nil {
		for _, f := range ed.e.Files() {
			if f.File != nil {
				open[filepath.Clean(f.File.Path)] = openText{src: f.Text.Snapshot().Bytes(), file: f.Bytes()}
			}
		}
	}
	w := &walkthrough{loading: true, srcs: map[string]*walkSrc{}, cardH: map[int]float32{}}
	x.w = w
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ch, err := gatherChange(ctx, ps, open)
		var steps []walkStep
		var by, subject string
		var files, repos int
		if err == nil {
			subject, files, repos = ch.subject(), len(ch.files), ch.projectCount()
			req := registry.ExplainRequest{Kind: "change", Subject: "The branch's change: " + subject, Context: ch.context(),
				Depth: registry.ExplainDetailed, Format: walkFormat}
			for _, f := range ch.files {
				req.Files = append(req.Files, f.path)
			}
			var md string
			var e registry.Explainer
			md, e, err = registry.Explain(ctx, x.h, req)
			by = e.WrittenBy()
			if err == nil {
				if steps, err = parseWalkthrough(md); err == nil {
					steps = ch.resolve(steps)
				}
			}
		}
		x.h.Do(func() {
			if x.w != w {
				return
			}
			w.loading, w.err, w.steps, w.by, w.subject, w.files, w.repos = false, err, steps, by, subject, files, repos
			x.goTo(0)
			x.h.Redraw()
		})
	}()
	return nil
}

// discard throws the walkthrough away and closes it.
func (x *walkthroughExt) discard() {
	x.w = nil
	if err := x.h.Run(context.Background(), "view.close", registry.Args{"id": ExplainChangeView}); err != nil {
		x.h.Log().Warn("explain.change", "err", err)
	}
}

func (x *walkthroughExt) stepArg(a registry.Args) int {
	if n := a.Int("step"); n > 0 {
		return n - 1
	}
	return x.w.step + a.Int("by")
}

// goTo shows step i, its first note's line picked.
func (x *walkthroughExt) goTo(i int) {
	w := x.w
	if w == nil || len(w.steps) == 0 {
		return
	}
	w.step = min(max(i, 0), len(w.steps)-1)
	s := w.steps[w.step]
	w.line = s.Start
	if len(s.Notes) > 0 {
		w.line = s.Notes[0].Start
	}
	w.top, w.follow = max(s.from-3, 1), false
	clear(w.cardH)
}

func (x *walkthroughExt) moveLine(by int) {
	w := x.w
	s := x.current()
	if s.path == "" {
		return
	}
	n := len(fileLines(x.srcOf(w, s.path, time.Time{}).src))
	w.line, w.follow = min(max(w.line+by, 1), n), true
}

// srcOf is a step file's text now: an open buffer's, else the file's, read again when it's changed on disk (looked
// at once a second, or now when now is zero).
func (x *walkthroughExt) srcOf(w *walkthrough, path string, now time.Time) *walkSrc {
	s := w.srcs[path]
	if s == nil {
		s = &walkSrc{}
		w.srcs[path] = s
	}
	if f, ok := x.sh.editor.openBuffer(path); ok {
		if v := f.Text.Snapshot().Version(); s.buf != f.Text || s.version != v || s.src == nil {
			s.buf, s.version, s.src, s.hl = f.Text, v, f.Text.Snapshot().Bytes(), nil
		}
		return s
	}
	if s.buf != nil || s.src == nil || now.IsZero() || now.Sub(w.statAt) > time.Second {
		fi, err := os.Stat(path)
		switch {
		case err != nil:
			s.buf, s.src, s.hl, s.size = nil, []byte{}, nil, -1
		case s.buf != nil || s.src == nil || fi.Size() != s.size || !fi.ModTime().Equal(s.mod):
			b, _ := os.ReadFile(path)
			s.buf, s.src, s.hl, s.size, s.mod = nil, b, nil, fi.Size(), fi.ModTime()
		}
	}
	return s
}

// check discards the walkthrough once the lines a step is about read differently from when it was written.
func (x *walkthroughExt) check(now time.Time, at bool) {
	w := x.w
	if w == nil || w.loading || w.err != nil {
		return
	}
	when := now
	if at {
		when = time.Time{}
	}
	for _, s := range w.steps {
		if s.path == "" {
			continue
		}
		if linesHash(x.srcOf(w, s.path, when).src, s.from, s.to) != s.hash {
			x.w = nil
			x.notify(core.SevInfo, fmt.Sprintf("The walkthrough of this change was discarded: %s changed since it was written. Explain this change again for a new one.", s.rel))
			x.h.Do(func() {
				if x.w == nil {
					x.discard()
				}
			})
			return
		}
	}
	if !at && now.Sub(w.statAt) > time.Second {
		w.statAt = now
	}
}

// ask writes a new request about the step shown, with it attached.
func (x *walkthroughExt) ask(ctx context.Context) error {
	w, s := x.w, x.current()
	var b strings.Builder
	fmt.Fprintf(&b, "Step %d of %d of the walkthrough of this change: %s\n\n%s", w.step+1, len(w.steps), s.Title, s.Prose)
	args := registry.Args{"text": ""}
	if s.path != "" {
		src := x.srcOf(w, s.path, time.Time{}).src
		ls := fileLines(src)
		fmt.Fprintf(&b, "\n\n%s · %s:%d-%d\n```\n", s.proj, s.rel, s.Start, s.End)
		for l := s.Start; l <= s.End && l <= len(ls); l++ {
			fmt.Fprintf(&b, "%4d  %s\n", l, ls[l-1])
		}
		b.WriteString("```")
		args["file"], args["line"] = s.path, s.Start
	}
	for i, n := range s.Notes {
		fmt.Fprintf(&b, "\n\nNote %d, L%d-%d: %s. %s", i+1, n.Start, n.End, n.Title, n.Text)
	}
	args["context"] = b.String()
	return x.h.Run(ctx, "request.new", args)
}

// comment opens a thread on a line of the step's file, in the editor.
func (x *walkthroughExt) comment(ctx context.Context, line int) error {
	s := x.current()
	if s.path == "" {
		return errors.New("this step isn't about a file")
	}
	if line <= 0 {
		line = x.w.line
	}
	if err := x.h.Run(ctx, "editor.open", registry.Args{"path": s.path, "line": line}); err != nil {
		return err
	}
	return x.h.Run(ctx, "thread.new", registry.Args{"file": s.path, "line": line})
}

// run runs a command from a control in the view, and gives the view the keys back.
func (x *walkthroughExt) run(cmd string, a registry.Args) {
	x.focus = true
	if err := x.h.Run(context.Background(), cmd, a); err != nil {
		x.notify(core.SevWarning, err.Error())
	}
}

type walkView struct{ x *walkthroughExt }

func (v walkView) Title(string) string { return "Explain this change" }
func (v walkView) EdgeToEdge()         {}
func (v walkView) ModeLabel(string) (string, string) {
	return "EXPLAIN", tWalkMode.Name()
}
func (v walkView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "explain.change", Fn: func(c *ui.Ctx) { v.x.view(c, vc) }}
}

// view is 07-explain: the header, the step's file with its notes, and the step dock.
func (x *walkthroughExt) view(c *ui.Ctx, vc registry.ViewCtx) {
	th := c.Theme()
	x.check(c.Now(), false)
	root := c.Next("walk")
	if vc.Focused && !c.FocusWithin(root) || x.focus {
		c.Focus(root)
		x.focus = false
	}
	w := x.w
	c.Column("walk", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tEditorBg), Clip: true, Focusable: true, KeyMode: ExplainChangeKeyMode,
		Role: ui.RoleRegion, Label: "Explain this change", Help: walkHelp}, func(c *ui.Ctx) {
		x.header(c, w)
		switch {
		case w == nil:
			x.message(c, "No walkthrough: Explain this change (␣E) asks for one.", false)
		case w.loading:
			x.message(c, "Asking for a walkthrough of the change…", false)
		case w.err != nil:
			x.message(c, w.err.Error(), true)
		default:
			x.stage(c, w)
			x.dock(c, w)
		}
	})
}

// message stands in for the stage: what's happening, or what went wrong with Try again.
func (x *walkthroughExt) message(c *ui.Ctx, msg string, failed bool) {
	th := c.Theme()
	c.Column("msg", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Gap: 12}, func(c *ui.Ctx) {
		tok := theme.FgFaint
		if failed {
			tok = theme.Error
		}
		c.Text("t", msg, uiFont(13, th.C(tok)))
		if failed && widget.Button(c, "again", "Try again", widget.ButtonOpts{Help: walkHelp}) {
			x.w = nil
			x.run("explain.change", nil)
		}
	})
}

// header is EXPLAIN THIS CHANGE, the step's project and file and its lines, and the throwaway note with Discard.
func (x *walkthroughExt) header(c *ui.Ctx, w *walkthrough) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center, Fill: th.C(tWalkBarBg), Stroke: th.C(theme.Line),
		Clip: true}, func(c *ui.Ctx) {
		widget.Chip(c, "chip", "Explain this change", th.C(tWalkStep), walkHelp)
		if x.ready() {
			s := w.steps[w.step]
			switch {
			case s.path != "":
				c.Box("dot", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: projectsHere(x.h).colour(th, s.proj)}, nil)
				c.Text("file", s.proj+" / "+s.rel, monoFont(12, th.C(theme.Fg)))
				c.Text("lines", fmt.Sprintf("L%d–%d", s.Start, s.End), monoFont(11, th.C(theme.FgFaint)))
			case s.missing:
				c.Text("file", s.rel+" isn't in the change", monoFont(12, th.C(theme.Warn)))
			default:
				c.Text("file", "summary", monoFont(12, th.C(theme.FgMuted)))
			}
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if w == nil {
			return
		}
		c.Text("throwaway", "throwaway · discarded if the code changes", uiFont(11, th.C(theme.FgFaint)))
		if widget.Button(c, "discard", "Discard", widget.ButtonOpts{Ghost: true, Help: walkHelp}) {
			x.run("explain.change.discard", nil)
		}
	})
}

// lit is the note lighting line l of step s, and whether any does; with no notes, the step's own lines are lit.
func lit(s walkStep, l int) (note int, ok bool) {
	for i, n := range s.Notes {
		if l >= n.Start && l <= n.End {
			return i, true
		}
	}
	if len(s.Notes) == 0 && l >= s.Start && l <= s.End {
		return -1, true
	}
	return -1, false
}

func noteTone(th *theme.Resolved, i int) render.Color {
	if i < 0 {
		return th.C(tWalkStep)
	}
	return th.C(tWalkNotes[i%len(tWalkNotes)])
}

// stage is the step's file from a little above what it's about: the lines its notes are on lit in their colours, the
// rest dimmed, and the notes beside them with leader lines; a step with no file says so.
func (x *walkthroughExt) stage(c *ui.Ctx, w *walkthrough) {
	th := c.Theme()
	s := w.steps[w.step]
	stageID := c.Next("stage")
	in := c.Interaction(stageID)
	if s.path == "" {
		c.Column("stage", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			msg := "This step is about the change as a whole."
			if s.missing {
				msg = s.rel + " isn't in the change or its projects."
			}
			c.Text("t", msg, uiFont(13, th.C(theme.FgFaint)))
		})
		return
	}
	m := x.sh.editor.metrics(c)
	src := x.srcOf(w, s.path, c.Now())
	if src.hl == nil {
		src.hl = highlightAround(x.h, s.path, src.src, 1, 0, math.MaxInt32)
	}
	lines := fileLines(src.src)
	area := render.XYWH(0, 0, 1200, 600)
	if r, ok := c.Rect(stageID); ok {
		area = r
	}
	rows := max(int(area.Dy()/m.lineH)-1, 1)
	if in.Scroll.Y != 0 {
		w.top += int(math.Round(float64(in.Scroll.Y / m.lineH)))
	}
	if w.follow {
		w.top = min(w.top, w.line)
		w.top = max(w.top, w.line-rows+1)
		w.follow = false
	}
	w.top = min(max(w.top, 1), max(len(lines)-rows+1, 1))
	last := min(w.top+rows-1, len(lines))
	w.line = min(max(w.line, w.top), last)

	gutter := max(float32(gutterW), float32(len(fmt.Sprint(len(lines))))*m.charW+34)
	textX := gutter + 4
	notesW := float32(440)
	codeW := max(area.Dx()-notesW-110, area.Dx()*.5)
	notesW = min(notesW, area.Dx()-codeW-90)
	cardX := codeW + 70
	rowY := func(l int) float32 { return float32(l-w.top)*m.lineH + 10 }

	c.Box("stage", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true, Wheel: true, Help: walkHelp}, func(c *ui.Ctx) {
		codeID := c.Next("code")
		click := widget.Clickable(c, "code", ui.Style{Dir: ui.Column, W: ui.Px(codeW), Pad: ui.Insets{T: 10}, NoHover: true, Cursor: render.CursorText,
			Role: ui.RoleTextBox, Label: s.rel, Help: walkHelp}, func(c *ui.Ctx) {
			for l := w.top; l <= last; l++ {
				x.codeRow(c, w, s, src.hl, lines, l, gutter, m)
			}
		})
		if r, ok := c.Rect(codeID); ok && click.Clicked {
			l := w.top + int((click.Pointer.Y-r.Min.Y-10)/m.lineH)
			w.line = min(max(l, w.top), last)
		}
		// The notes, each level with its first line and below the one before; then up from the bottom so none runs out.
		type placed struct {
			i    int
			y, h float32
			from render.Point
		}
		var ps []placed
		for i, n := range s.Notes {
			if n.End < w.top || n.Start > last {
				continue
			}
			h := w.cardH[i]
			if h == 0 {
				h = 110
			}
			widest := 0
			for l := max(n.Start, w.top); l <= min(n.End, last); l++ {
				widest = max(widest, utf8.RuneCountInString(expandTabs(lines[l-1])))
			}
			from := render.Point{X: min(textX+float32(widest+3)*m.charW, codeW-12), Y: rowY(max(n.Start, w.top)) + m.lineH/2}
			y := from.Y - 30
			if len(ps) > 0 {
				p := ps[len(ps)-1]
				y = max(y, p.y+p.h+14)
			}
			ps = append(ps, placed{i: i, y: max(y, 8), h: h, from: from})
		}
		bottom := area.Dy() - 8
		for k := len(ps) - 1; k >= 0; k-- {
			ps[k].y = max(min(ps[k].y, bottom-ps[k].h), 8)
			bottom = ps[k].y - 14
		}
		if len(ps) > 0 {
			c.Box("leaders", ui.Style{Float: &ui.Float{}, Z: ui.LayerContent}, func(c *ui.Ctx) {
				c.Paint("lines", ui.Px(area.Dx()), ui.Px(area.Dy()), func(f render.Frame, r render.Rect) {
					f.PushClip(r, 0)
					defer f.Pop()
					for _, p := range ps {
						from := render.Point{X: r.Min.X + p.from.X, Y: r.Min.Y + p.from.Y}
						to := render.Point{X: r.Min.X + cardX, Y: r.Min.Y + p.y + 20}
						widget.Leader(f, from, to, noteTone(th, p.i))
					}
				})
			})
		}
		for _, p := range ps {
			n := s.Notes[p.i]
			c.Box(fmt.Sprint("note", p.i), ui.Style{Float: &ui.Float{Offset: render.Point{X: cardX, Y: p.y}}, Z: ui.LayerContent + 1}, func(c *ui.Ctx) {
				id := widget.Annotation(c, "card", widget.AnnotationOpts{N: fmt.Sprint(p.i + 1), Title: n.Title, Tone: noteTone(th, p.i),
					Width: ui.Px(notesW), Ground: th.C(tEditorBg), Help: walkHelp}, func(c *ui.Ctx) {
					if n.Text != "" {
						markdownText(c, "md", n.Text, notesW-28, widget.AnnotationBody(c))
					}
				})
				if r, ok := c.Rect(id); ok && r.Dy() != w.cardH[p.i] {
					w.cardH[p.i] = r.Dy()
					x.h.Redraw()
				}
			})
		}
	})
}

// codeRow is line l of the step's file: its number, coloured by the note it's lit for, and its text, dimmed when no
// note is on it, with a note's mark highlighted.
func (x *walkthroughExt) codeRow(c *ui.Ctx, w *walkthrough, s walkStep, hl *sourceFile, lines []string, l int, gutter float32, m paneMetrics) {
	th := c.Theme()
	note, on := lit(s, l)
	tone := noteTone(th, note)
	st := ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(m.lineH), Cross: ui.Center}
	if on {
		st.Fill = theme.WithAlpha(tone, .08)
	} else {
		st.Fade = .65
	}
	c.Row(l, st, func(c *ui.Ctx) {
		num := th.C(tGutterFg)
		switch {
		case l == w.line:
			num = th.C(theme.Fg)
		case on:
			num = tone
		}
		c.Row("gutter", ui.Style{W: ui.Px(gutter), Pad: ui.Insets{R: 16}, Main: ui.End}, func(c *ui.Ctx) {
			c.Text("n", fmt.Sprint(l), ui.TextStyle{Font: m.font, Color: num})
		})
		if on {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(m.lineH), Fill: tone, Float: &ui.Float{Offset: render.Point{X: gutter - 2}}}, nil)
		}
		c.Box("gap", ui.Style{W: ui.Px(4)}, nil)
		if on && note >= 0 {
			if mk := s.Notes[note].Mark; mk != "" {
				line := expandTabs(lines[l-1])
				if i := strings.Index(line, mk); i >= 0 {
					col := utf8.RuneCountInString(line[:i])
					c.Box("mark", ui.Style{W: ui.Px(float32(utf8.RuneCountInString(mk)) * m.charW), H: ui.Px(m.lineH - 2), Radius: 2,
						Fill: theme.WithAlpha(tone, .2), Z: ui.LayerUnder, Float: &ui.Float{Offset: render.Point{X: gutter + 4 + float32(col)*m.charW, Y: 1}}}, nil)
				}
			}
		}
		if hl != nil && l-hl.first >= 0 && l-hl.first < len(hl.spans) {
			for k, sp := range hl.spans[l-hl.first] {
				ts := ui.TextStyle{Font: m.font, Color: th.C(tCodeFg)}
				if sp.capture != "" {
					sy := th.Syntax("@" + sp.capture)
					ts.Color, ts.Font.Italic = sy.FG, sy.Italic
				}
				c.Text(k, strings.TrimRight(expandTabs(sp.text), "\r"), ts)
			}
			return
		}
		c.Text("t", expandTabs(lines[l-1]), ui.TextStyle{Font: m.font, Color: th.C(tCodeFg)})
	})
}

// dock is the step dock: a chip for each step across files and repos, then the step's number, title and prose, Ask
// about this step, Comment on its line, who wrote it, and ← →.
func (x *walkthroughExt) dock(c *ui.Ctx, w *walkthrough) {
	th := c.Theme()
	s := w.steps[w.step]
	n := len(w.steps)
	c.Column("dock", ui.Style{W: ui.Flex(1), Fill: th.C(tWalkDockBg), Role: ui.RoleRegion, Label: "Steps", Help: walkHelp}, func(c *ui.Ctx) {
		c.Row("edge", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tWalkDockEdge)}, func(c *ui.Ctx) {
			c.Box("done", ui.Style{W: ui.Pct(float32(w.step+1) / float32(n)), H: ui.Px(1), Fill: th.C(tWalkStep)}, nil)
		})
		c.Row("strip", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 18, R: 18, T: 12, B: 10}, Gap: 16, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
			c.Row("steps", ui.Style{W: ui.Flex(1), Cross: ui.Center, ScrollX: true}, func(c *ui.Ctx) {
				for i, st := range w.steps {
					if i > 0 {
						wire := th.C(theme.LineStrong)
						if i <= w.step {
							wire = theme.WithAlpha(th.C(tWalkStep), .5)
						}
						c.Box(fmt.Sprint("wire", i), ui.Style{W: ui.Px(26), H: ui.Px(1), Fill: wire}, nil)
					}
					if x.stepChip(c, i, st, i == w.step, i < w.step) {
						x.run("explain.change.step", registry.Args{"step": i + 1})
					}
				}
			})
			c.Text("n", fmt.Sprintf("%d %s · %d %s", w.repos, plural(w.repos, "repo", "repos"), w.files, plural(w.files, "file", "files")),
				monoFont(10.5, th.C(theme.FgFaint)))
		})
		c.Row("body", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 14), Gap: 26}, func(c *ui.Ctx) {
			c.Column("num", ui.Style{W: ui.Px(110), Gap: 6}, func(c *ui.Ctx) {
				c.Text("k", "STEP", kickerFont(th.C(theme.FgFaint)))
				c.Row("n", ui.Style{Cross: ui.End}, func(c *ui.Ctx) {
					c.Text("i", fmt.Sprintf("%02d", w.step+1), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 40, Weight: typeset.SemiBold},
						Color: th.C(tWalkStep)})
					c.Text("of", fmt.Sprintf("/%02d", n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 16}, Color: th.C(theme.FgFaint)})
				})
			})
			width := float32(760)
			if r, ok := c.Rect(c.Next("text")); ok {
				width = min(r.Dx(), 760)
			}
			c.Column("text", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
				c.Text("title", s.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 19, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
				if s.Prose != "" {
					markdownText(c, "prose", s.Prose, width, uiFont(13.5, th.C(theme.FgMuted)))
				}
				c.Row("acts", ui.Style{Gap: 8, Cross: ui.Center, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
					if widget.Button(c, "ask", "Ask about this step", widget.ButtonOpts{Ghost: true, Help: walkHelp,
						Disabled: !x.h.Available("explain.change.ask", nil)}) {
						x.run("explain.change.ask", nil)
					}
					if s.path != "" && widget.Button(c, "comment", fmt.Sprintf("Comment on L%d", w.line), widget.ButtonOpts{Ghost: true, Help: walkHelp,
						Disabled: !x.h.Available("explain.change.comment", nil)}) {
						x.run("explain.change.comment", nil)
					}
					c.Text("by", w.by, uiFont(11, th.C(theme.FgFaint)))
				})
			})
			c.Row("nav", ui.Style{Gap: 8}, func(c *ui.Ctx) {
				if widget.Button(c, "prev", "←", widget.ButtonOpts{Ghost: true, Help: walkHelp, Disabled: w.step == 0}) {
					x.run("explain.change.step", registry.Args{"by": -1})
				}
				if widget.Button(c, "next", "→", widget.ButtonOpts{Help: walkHelp, Disabled: w.step == n-1}) {
					x.run("explain.change.step", registry.Args{"by": 1})
				}
			})
		})
	})
}

// stepChip is a step's diamond and its project and file, lit while it's shown; it says whether it was clicked.
func (x *walkthroughExt) stepChip(c *ui.Ctx, i int, s walkStep, on, done bool) bool {
	th := c.Theme()
	fill, fg := th.C(theme.LineStrong), th.C(theme.FgFaint)
	switch {
	case on:
		fill, fg = th.C(tWalkStep), th.C(theme.BgBase)
	case done:
		fill, fg = theme.WithAlpha(th.C(tWalkStep), .18), th.C(tWalkStep)
	}
	proj, file := s.proj, filepath.Base(s.rel)
	if s.path != "" && strings.Contains(s.rel, "/") && on {
		file = s.rel
	}
	if s.path == "" && !s.missing {
		proj, file = "—", "summary"
	}
	border := th.C(theme.LineStrong)
	if on {
		border = theme.WithAlpha(th.C(tWalkStep), .45)
	}
	return widget.Clickable(c, fmt.Sprint("step", i), ui.Style{Dir: ui.Row, Gap: 6, Cross: ui.Center, Radius: 3, Label: fmt.Sprintf("Step %d: %s", i+1, s.Title),
		Selected: on, Tooltip: s.Title, Help: walkHelp}, func(c *ui.Ctx) {
		widget.Diamond(c, "n", fmt.Sprint(i+1), 22, fill, fg)
		c.Row("file", ui.Style{Pad: ui.PadXY(7, 2), Gap: 5, Stroke: border, Border: 1, Radius: 3, Fill: th.C(theme.BgSurface), Cross: ui.Center}, func(c *ui.Ctx) {
			if proj != "" {
				c.Text("p", proj, monoFont(10.5, th.C(tWalkProject)))
			}
			tok := theme.FgFaint
			if on {
				tok = theme.Fg
			}
			c.Text("f", file, monoFont(10.5, th.C(tok)))
		})
	}).Clicked
}

// statusSegment is the status line's step, file and keys while the walkthrough has focus: "step 3/7 · invoice.go ·
// ←/→ step · c comment · q quit".
func (x *walkthroughExt) statusSegment(c *ui.Ctx) {
	if !x.ready() {
		return
	}
	th := c.Theme()
	w, s := x.w, x.current()
	c.Row("explain", ui.Style{H: ui.Flex(1), Gap: 12, Cross: ui.Center, Role: ui.RoleStatus, Label: "Walkthrough"}, func(c *ui.Ctx) {
		c.Text("step", fmt.Sprintf("step %d/%d", w.step+1, len(w.steps)), statusText(c, tStatusStrong))
		if s.rel != "" {
			c.Text("file", filepath.Base(s.rel), statusText(c, tStatusFg))
		}
		c.Box("sep", ui.Style{W: ui.Px(1), H: ui.Px(12), Fill: th.C(theme.Line)}, nil)
		c.Text("keys", "←/→ step · c comment · q quit", statusText(c, tStatusFg))
	})
}
