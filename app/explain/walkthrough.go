package explain

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit/draw"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Explain this change: a walkthrough of the branch's change, step by step across its files and projects.
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

// Walkthrough is explain.change.open: it asks the first explainer for a walkthrough of the branch's change, as JSON
// steps (WalkFormat), and shows it a step at a time: the step's file with the lines it's about lit and its notes
// beside them, and the step dock. It's thrown away once the code a step is about changes.
type Walkthrough struct {
	kit.Disposer
	ConfigDir string // the user's config
	h         plugin.Host
	busy      work.Busy
	metrics   draw.PaneMetrics // the code's, as the editor draws it

	w     *walkthrough // on the loop only
	focus bool         // give the view focus next frame
}

// walkthrough is one walkthrough: being asked for, failed, or its steps and the one shown.
type walkthrough struct {
	loading bool
	since   time.Time          // when it was asked for
	cancel  context.CancelFunc // stops the asking
	err     error
	subject string
	files   int
	repos   int
	steps   []walkStep
	step    int
	line    int  // the line of the step's file a comment is about
	top     int  // the first line shown
	follow  bool // scroll to the line: it was moved
	reveal  bool // the step just opened: scroll its first note into view
	by      string

	srcs    kit.FileTexts   // the step files' text now, by path
	checked map[string]int  // the text of each step file its steps were last checked against, by its gen
	cardH   map[int]float32 // each note card's height last frame
}

func (x *Walkthrough) Manifest() plugin.Manifest { return plugin.Manifest{ID: "walkthrough"} }

func (x *Walkthrough) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "walkthrough", Busy: x.busy.Busy}))
	x.h = h
	x.Add(kit.OnDirChanged(h, "a walkthrough is of its directory", func(kit.DirChanged) { x.w = nil }))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	x.Add(h.View(ExplainChangeView, walkView{x}))
	x.Add(h.View("statusline.left.05-explain", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		if f := h.Layout().Focus; f == nil || f.ID != ExplainChangeView || !x.ready() {
			return nil
		}
		return &ui.Func{Key: "explain.change", Fn: x.statusSegment}
	})))
	x.Add(h.View(kit.DiffHeader+"90-explain-change", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "explain.change", Fn: func(c *ui.Ctx) {
			c.Row("explain", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 4), Main: ui.End}, func(c *ui.Ctx) {
				if widget.Button(c, "go", "Explain this change", widget.ButtonOpts{Disabled: !h.Available(registry.ExplainCommand, nil),
					Help: "editor/pane#explain-change"}) {
					if err := h.Run(context.Background(), "explain.change.open", nil); err != nil {
						h.Notify(core.Notice{ID: "explain.change.open", Severity: core.SevWarning, Source: "explain", AutoClear: true, Text: err.Error()})
					}
				}
			})
		}}
	})))
	return nil
}

func (x *Walkthrough) commands() []registry.Command {
	ready := func(registry.Args) bool { return x.ready() }
	return []registry.Command{
		{ID: "explain.change.open", Title: "Explain this change: a walkthrough of the branch's change across every project and file, step by step, written by the session's agent; thrown away once the code it's about changes",
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.open(ctx) }},
		{ID: "explain.change.step", Title: "Go to step {step} of the walkthrough, or {by} steps on (-1 back)", Available: ready,
			Run: func(_ registry.Ctx, a registry.Args) error { x.goTo(x.stepArg(a)); return nil }},
		{ID: "explain.change.line.move", Title: "Move the walkthrough's line {by} lines, for a comment", Available: ready,
			Run: func(_ registry.Ctx, a registry.Args) error { x.moveLine(a.Int("by")); return nil }},
		{ID: "explain.change.ask", Title: "Ask about this step of the walkthrough: a new request with the step attached",
			Available: func(registry.Args) bool { return x.ready() && x.h.Available("request.new", nil) },
			Run:       func(ctx registry.Ctx, _ registry.Args) error { return x.ask(ctx) }},
		{ID: "explain.change.comment", Title: "Comment on the walkthrough step's {line} (the one picked by default): a thread on it",
			Available: func(registry.Args) bool {
				return x.ready() && x.current().path != "" && x.h.Available("thread.new", nil)
			},
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.comment(ctx, a.Int("line")) }},
		{ID: "explain.change.discard", Title: "Discard the walkthrough", Available: func(registry.Args) bool { return x.w != nil },
			Run: func(registry.Ctx, registry.Args) error { x.discard(); return nil }},
		{ID: "explain.change.close", Title: "Close the walkthrough, keeping it to come back to",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return x.h.Run(ctx, "view.close", registry.Args{"id": ExplainChangeView})
			}},
	}
}

func (x *Walkthrough) keymaps() []registry.Keymap {
	return []registry.Keymap{
		{Mode: "n", Keys: "<Leader>E", Command: "explain.change.open", Desc: "Walk through the change"},
		{Mode: ExplainChangeKeyMode, Keys: "<Left>", Command: "explain.change.step", Args: registry.Args{"by": -1}, Desc: "Previous step"},
		{Mode: ExplainChangeKeyMode, Keys: "h", Command: "explain.change.step", Args: registry.Args{"by": -1}, Desc: "Previous step"},
		{Mode: ExplainChangeKeyMode, Keys: "<Right>", Command: "explain.change.step", Args: registry.Args{"by": 1}, Desc: "Next step"},
		{Mode: ExplainChangeKeyMode, Keys: "l", Command: "explain.change.step", Args: registry.Args{"by": 1}, Desc: "Next step"},
		{Mode: ExplainChangeKeyMode, Keys: "j", Command: "explain.change.line.move", Args: registry.Args{"by": 1}, Desc: "Next line"},
		{Mode: ExplainChangeKeyMode, Keys: "<Down>", Command: "explain.change.line.move", Args: registry.Args{"by": 1}, Desc: "Next line"},
		{Mode: ExplainChangeKeyMode, Keys: "k", Command: "explain.change.line.move", Args: registry.Args{"by": -1}, Desc: "Previous line"},
		{Mode: ExplainChangeKeyMode, Keys: "<Up>", Command: "explain.change.line.move", Args: registry.Args{"by": -1}, Desc: "Previous line"},
		{Mode: ExplainChangeKeyMode, Keys: "c", Command: "explain.change.comment", Desc: "Comment on the line"},
		{Mode: ExplainChangeKeyMode, Keys: "a", Command: "explain.change.ask", Desc: "Ask about this step"},
		{Mode: ExplainChangeKeyMode, Keys: "q", Command: "explain.change.close", Desc: "Quit the walkthrough"},
		{Mode: ExplainChangeKeyMode, Keys: "<Esc>", Command: "explain.change.close", Desc: "Quit the walkthrough"},
		{Mode: ExplainChangeKeyMode, Keys: "<C-w>c", Command: "explain.change.close", Desc: "Close"},
		{Mode: ExplainChangeKeyMode, Keys: "<C-w>q", Command: "explain.change.close", Desc: "Close"},
	}
}

func (x *Walkthrough) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	return x.Disposer.Deactivate(ctx)
}

// Held says whether there's a walkthrough, shown or not: asked for, failed, or its steps.
func (x *Walkthrough) Held() bool { return x.w != nil }

func (x *Walkthrough) ready() bool {
	return x.w != nil && !x.w.loading && x.w.err == nil && len(x.w.steps) > 0
}

// current is the step shown.
func (x *Walkthrough) current() walkStep {
	if !x.ready() {
		return walkStep{}
	}
	return x.w.steps[x.w.step]
}

func (x *Walkthrough) notify(sev core.Severity, msg string) {
	x.h.Notify(core.Notice{ID: "explain.change.open", Severity: sev, Source: "explain", AutoClear: true, Text: msg})
}

// open shows the walkthrough, asking for one first unless there's one still good.
func (x *Walkthrough) open(ctx context.Context) error {
	if x.w != nil {
		x.check()
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
func (x *Walkthrough) projects() []walkProject {
	var out []walkProject
	for _, p := range kit.ProjectsHere(x.h).Projects {
		if p.Dir != "" {
			out = append(out, walkProject{name: p.Name, dir: p.Dir})
		}
	}
	if dir := kit.WindowDir(x.h); len(out) == 0 && dir != "" {
		out = append(out, walkProject{name: filepath.Base(dir), dir: dir})
	}
	return out
}

// start asks the first explainer for a walkthrough of the change, off the loop.
func (x *Walkthrough) start() error {
	if len(registry.Explainers(x.h)) == 0 {
		return errors.New("nothing can explain yet: it needs an agent")
	}
	ps := x.projects()
	if len(ps) == 0 {
		return errors.New("there's no project here to explain the change of")
	}
	open := map[string]openText{}
	if ed, ok := services.EditorsOf(x.h); ok {
		for _, b := range ed.Buffers() {
			open[filepath.Clean(b.Path)] = openText{src: b.Text.Snapshot().Bytes(), file: b.Bytes()}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	w := &walkthrough{loading: true, since: kit.WindowNow(x.h), cancel: cancel, srcs: kit.FileTexts{}, checked: map[string]int{}, cardH: map[int]float32{}}
	x.w = w
	x.busy.Go(func() {
		defer cancel()
		ch, err := gatherChange(ctx, ps, open)
		var steps []walkStep
		var by, subject string
		var files, repos int
		if err == nil {
			subject, files, repos = ch.subject(), len(ch.files), ch.projectCount()
			req := registry.ExplainRequest{Kind: "change", Subject: "The branch's change: " + subject, Context: ch.context(),
				Depth: registry.ExplainDetailed, Format: kit.RenderPrompt(x.ConfigDir, WalkFormat, nil)}
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
	})
	return nil
}

// discard throws the walkthrough away and closes it.
func (x *Walkthrough) discard() {
	x.w = nil
	if err := x.h.Run(context.Background(), "view.close", registry.Args{"id": ExplainChangeView}); err != nil {
		x.h.Log().Warn("explain.change", "err", err)
	}
}

func (x *Walkthrough) stepArg(a registry.Args) int {
	if n := a.Int("step"); n > 0 {
		return n - 1
	}
	return x.w.step + a.Int("by")
}

// goTo shows step i, its first note's line picked.
func (x *Walkthrough) goTo(i int) {
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
	w.top, w.follow, w.reveal = max(s.from-3, 1), false, true
	clear(w.cardH)
}

func (x *Walkthrough) moveLine(by int) {
	w := x.w
	s := x.current()
	if s.path == "" {
		return
	}
	n := len(x.srcOf(w, s.path).Lines())
	w.line, w.follow = min(max(w.line+by, 1), n), true
}

// srcOf is a step file's text now: an open buffer's, else the file's, read again when it changes on disk.
func (x *Walkthrough) srcOf(w *walkthrough, path string) *kit.FileText { return w.srcs.Of(x.h, path) }

// check discards the walkthrough once the lines a step is about read differently from when it was written.
func (x *Walkthrough) check() {
	w := x.w
	if w == nil || w.loading || w.err != nil {
		return
	}
	seen := map[string]int{}
	for _, s := range w.steps {
		if s.path == "" {
			continue
		}
		src := x.srcOf(w, s.path)
		if src.Loading() {
			seen[s.path] = -1
			continue
		}
		if seen[s.path] = src.Gen; w.checked[s.path] == src.Gen {
			continue
		}
		if linesHash(src.Src, s.from, s.to) != s.hash {
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
	w.checked = seen
}

// ask writes a new request about the step shown, with it attached.
func (x *Walkthrough) ask(ctx context.Context) error {
	w, s := x.w, x.current()
	var b strings.Builder
	fmt.Fprintf(&b, "Step %d of %d of the walkthrough of this change: %s\n\n%s", w.step+1, len(w.steps), s.Title, s.Prose)
	args := registry.Args{"text": ""}
	if s.path != "" {
		ls := x.srcOf(w, s.path).Lines()
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
func (x *Walkthrough) comment(ctx context.Context, line int) error {
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
func (x *Walkthrough) run(cmd string, a registry.Args) {
	x.focus = true
	if err := x.h.Run(context.Background(), cmd, a); err != nil {
		x.notify(core.SevWarning, err.Error())
	}
}
