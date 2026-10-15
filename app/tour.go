package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/worker"
	"github.com/liamg/oriel/worker/tour"
)

// The tour reader (ui.md 14): a tour session's steps, read a step at a time, with their quotes, notes, diagrams,
// runs and screenshots; asking about a step; and the export.
const (
	TourView    = "tour"
	TourKeyMode = "tour"
	tourHelp    = "agent/tour"
)

// tourPlugin is RV-8: crit's tour reader and export, on the tour package's reading of the steps.
type tourPlugin struct {
	disposer
	sh     *shell
	h      plugin.Host
	loader *tour.Loader
	by     map[core.SessionID]*tourState
}

// tourState is one tour session's reader.
type tourState struct {
	t    tour.Tour
	err  error
	read time.Time // when the steps were last read
	seen int       // the transcript's length then

	step     int
	visited  map[string]bool // the steps read, by file
	scroll   float32         // to scroll the step by, next frame
	top      bool            // scroll the step to its top next frame
	focus    bool            // give the reader focus next frame
	focusAsk bool
	ask      string
	replies  map[core.ThreadID]*string
	expanded map[*tour.QuoteText]bool // collapsed quotes shown whole, by the quote as last read
	open     map[string]bool          // the same, by step file and quote number, kept across reads
	hl       map[string]*sourceFile
	images   map[string]tourImage
}

type tourImage struct {
	img  image.Image
	mod  time.Time
	size int64
}

func (x *tourPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "tour"} }

func (x *tourPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.loader, x.by = h, tour.NewLoader(), map[core.SessionID]*tourState{}
	isTour := func(registry.Args) bool { _, ok := x.front(); return ok }
	hasSteps := func(registry.Args) bool { _, st, ok := x.current(); return ok && len(st.t.Steps) > 0 }
	for _, c := range []registry.Command{
		{ID: "tour.open", Title: "Open the tour reader for the tour session in front", Available: isTour,
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.open(ctx) }},
		{ID: "tour.step", Title: "Go to step {step} of the tour, or {by} steps on (-1 back)", Available: hasSteps,
			Run: func(_ registry.Ctx, a registry.Args) error { x.goTo(a); return nil }},
		{ID: "tour.scroll", Title: "Scroll the tour's step {by} lines", Available: hasSteps,
			Run: func(_ registry.Ctx, a registry.Args) error { x.scroll(a.Int("by")); return nil }},
		{ID: "tour.open-quote", Title: "Open the step's quote {n} (the first by default) in the editor, at its lines", Available: hasSteps,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.openQuote(ctx, a.Int("n")) }},
		{ID: "tour.ask", Title: "Ask about the tour's step: {text}, or the ask field focused; asked again in the step's open thread", Available: isTour,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.askCmd(ctx, a.String("text")) }},
		{ID: "tour.export", Title: "Export the tour as one self-contained file at its root: {format} html (tour.html) or md (tour.md); {ask} asks the agent to publish it",
			Available: isTour, Run: func(ctx registry.Ctx, a registry.Args) error { return x.exportCmd(ctx, a) }},
		{ID: "tour.close", Title: "Close the tour reader",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "view.close", registry.Args{"id": TourView})
			}},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: TourKeyMode, Keys: "h", Command: "tour.step", Args: registry.Args{"by": -1}, Desc: "Previous step"},
		{Mode: TourKeyMode, Keys: "<Left>", Command: "tour.step", Args: registry.Args{"by": -1}, Desc: "Previous step"},
		{Mode: TourKeyMode, Keys: "l", Command: "tour.step", Args: registry.Args{"by": 1}, Desc: "Next step"},
		{Mode: TourKeyMode, Keys: "<Right>", Command: "tour.step", Args: registry.Args{"by": 1}, Desc: "Next step"},
		{Mode: TourKeyMode, Keys: "j", Command: "tour.scroll", Args: registry.Args{"by": 3}, Desc: "Scroll down"},
		{Mode: TourKeyMode, Keys: "<Down>", Command: "tour.scroll", Args: registry.Args{"by": 3}, Desc: "Scroll down"},
		{Mode: TourKeyMode, Keys: "k", Command: "tour.scroll", Args: registry.Args{"by": -3}, Desc: "Scroll up"},
		{Mode: TourKeyMode, Keys: "<Up>", Command: "tour.scroll", Args: registry.Args{"by": -3}, Desc: "Scroll up"},
		{Mode: TourKeyMode, Keys: "gd", Command: "tour.open-quote", Desc: "Open the quote in the editor"},
		{Mode: TourKeyMode, Keys: "a", Command: "tour.ask", Desc: "Ask about this step"},
		{Mode: TourKeyMode, Keys: "q", Command: "tour.close", Desc: "Close the tour"},
		{Mode: TourKeyMode, Keys: "<Esc>", Command: "tour.close", Desc: "Close the tour"},
		{Mode: TourKeyMode, Keys: "<C-w>c", Command: "tour.close", Desc: "Close"},
		{Mode: TourKeyMode, Keys: "<C-w>q", Command: "tour.close", Desc: "Close"},
	} {
		x.add(h.Keymap(k))
	}
	x.add(h.View(TourView, tourView{x}))
	x.add(h.View("statusline.left.05-tour", tourSegment(x, x.statusLeft)))
	x.add(h.View("statusline.right.05-tour", tourSegment(x, x.statusRight)))
	x.add(h.Contribute(registry.AgentTool{Name: "tour_export", Source: "builtin:tour", Params: map[string]string{"format": "string?"}, Run: x.toolExport,
		Desc: "Write the tour as one self-contained file at its root, from its steps as they are now: format html (tour.html, the default: " +
			"no fetches, nothing external, ready to publish as an artifact) or md (tour.md, quotes resolved to their code)."}))
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if _, ok := ev.(core.SessionFocused); ok {
			h.Do(x.openIfTour)
		}
	})))
	h.Do(x.openIfTour)
	return nil
}

// front is the session in front, when it's a tour.
func (x *tourPlugin) front() (core.SessionState, bool) {
	st := x.h.State()
	se, ok := st.Session(st.Focus)
	if !ok {
		return core.SessionState{}, false
	}
	s := se.Snapshot()
	return s, s.Kind == core.KindTour && s.Dir != ""
}

func (x *tourPlugin) state(id core.SessionID) *tourState {
	st := x.by[id]
	if st == nil {
		st = &tourState{visited: map[string]bool{}, replies: map[core.ThreadID]*string{}, expanded: map[*tour.QuoteText]bool{},
			open: map[string]bool{}, hl: map[string]*sourceFile{}, images: map[string]tourImage{}}
		x.by[id] = st
	}
	return st
}

// current is the tour in front and its reader, its steps read again each second or when the agent did something.
func (x *tourPlugin) current() (core.SessionState, *tourState, bool) {
	se, ok := x.front()
	if !ok {
		return se, nil, false
	}
	st := x.state(se.ID)
	x.reload(se, st, time.Now())
	return se, st, true
}

func (x *tourPlugin) reload(se core.SessionState, st *tourState, now time.Time) {
	if !st.read.IsZero() && now.Before(st.read.Add(time.Second)) && st.seen == len(se.Transcript) {
		return
	}
	st.read, st.seen = now, len(se.Transcript)
	info, ok := worker.TourOf(se.Dir)
	if !ok {
		info = tour.Info{Name: se.Name, Title: se.Prompt, Kind: tour.DefaultKind}
		for _, p := range se.Projects {
			info.Repos = append(info.Repos, tour.Repo{Name: p.Name, Repo: p.Repo, Dir: p.Dir, Base: p.Base, Commit: p.Commit})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st.t, st.err = x.loader.Load(ctx, se.Dir, info)
	st.step = min(st.step, max(len(st.t.Steps)-1, 0))
	clear(st.expanded)
	for i, s := range st.t.Steps {
		for k, q := range quotesOf(s) {
			if st.open[fmt.Sprint(s.File, "#", k)] {
				st.expanded[q] = true
			}
		}
		if i == st.step {
			st.visited[s.File] = true
		}
	}
}

func quotesOf(s tour.Step) []*tour.QuoteText {
	var out []*tour.QuoteText
	for _, it := range s.Items {
		if it.Quote != nil {
			out = append(out, it.Quote)
		}
	}
	return out
}

// openIfTour shows the reader when the window comes to a tour session.
func (x *tourPlugin) openIfTour() {
	se, ok := x.front()
	if !ok || filepath.Clean(x.sh.cfg.WorkDir) != filepath.Clean(sessionDir(se)) {
		return
	}
	for _, v := range x.h.Layout().Visible() {
		if v.ID == TourView {
			return
		}
	}
	if err := x.h.Run(context.Background(), "tour.open", nil); err != nil {
		x.h.Log().Warn("tour", "err", err)
	}
}

func (x *tourPlugin) open(ctx context.Context) error {
	se, ok := x.front()
	if !ok {
		return errors.New("the session in front isn't a tour")
	}
	x.state(se.ID).focus = true
	return x.h.Run(ctx, "view.open", registry.Args{"id": TourView, "place": "tab", "title": "tour: " + se.Name})
}

func (x *tourPlugin) goTo(a registry.Args) {
	_, st, ok := x.current()
	if !ok || len(st.t.Steps) == 0 {
		return
	}
	i := st.step + a.Int("by")
	if n := a.Int("step"); n > 0 {
		i = n - 1
	}
	i = min(max(i, 0), len(st.t.Steps)-1)
	if i != st.step {
		st.step, st.top = i, true
	}
	st.visited[st.t.Steps[i].File] = true
	st.focus = true
}

func (x *tourPlugin) scroll(by int) {
	if _, st, ok := x.current(); ok {
		st.scroll += float32(by) * 20
	}
}

// step is the step shown.
func (st *tourState) current() (tour.Step, bool) {
	if st.step < len(st.t.Steps) {
		return st.t.Steps[st.step], true
	}
	return tour.Step{}, false
}

// openQuote opens the step's quote n (1-based) in the editor at its first line; one at another revision can't be.
func (x *tourPlugin) openQuote(ctx context.Context, n int) error {
	_, st, ok := x.current()
	if !ok {
		return errors.New("no tour is in front")
	}
	s, _ := st.current()
	qs := quotesOf(s)
	n = max(n, 1)
	if n > len(qs) {
		return errors.New("this step quotes no code")
	}
	q := qs[n-1]
	switch {
	case q.Err != "":
		return errors.New(q.Err)
	case q.Rev != "":
		return fmt.Errorf("that quote is of %s at %s, not the worktree", q.Path, q.Rev)
	}
	line := q.Start
	for l := q.Start; l <= q.End; l++ {
		if q.Marked[l] {
			line = l
			break
		}
	}
	return x.h.Run(ctx, "editor.open", registry.Args{"path": filepath.Join(q.Dir, q.Path), "line": line})
}

// stepThreads are the threads asking about a step: anchored to its file.
func stepThreads(se core.SessionState, file string) []core.Thread {
	var out []core.Thread
	for _, t := range se.Threads {
		if a := t.Anchor; a != nil && a.Project == core.DocsProject && a.File == file && !t.Btw {
			out = append(out, t)
		}
	}
	return out
}

func (x *tourPlugin) askCmd(ctx context.Context, text string) error {
	se, st, ok := x.current()
	if !ok {
		return errors.New("no tour is in front")
	}
	if strings.TrimSpace(text) == "" {
		st.focusAsk = true
		return nil
	}
	return x.ask(ctx, se, st, text)
}

// ask asks about the step shown: in its latest open thread, else a new one on its file, so the agent reads where it is.
// Before there are steps, it's the tour's own thread.
func (x *tourPlugin) ask(ctx context.Context, se core.SessionState, st *tourState, text string) error {
	s, ok := st.current()
	if !ok {
		return x.h.Run(ctx, "request.comment", registry.Args{"thread": string(se.PlanThread()), "body": text})
	}
	ts := stepThreads(se, s.File)
	for _, t := range slices.Backward(ts) {
		if t.Open() {
			return x.h.Run(ctx, "request.comment", registry.Args{"thread": string(t.ID), "body": text})
		}
	}
	return x.h.Run(ctx, "request.comment", registry.Args{"file": filepath.Join(se.Dir, s.File), "line": 1, "text": "# " + s.Title,
		"title": fmt.Sprintf("Step %d: %s", st.step+1, s.Title), "body": text})
}

// exportCmd writes the export, and with ask (the reader's button) asks the agent in the tour's thread to publish it.
func (x *tourPlugin) exportCmd(ctx context.Context, a registry.Args) error {
	se, ok := x.front()
	if !ok {
		return errors.New("the session in front isn't a tour")
	}
	name, size, err := x.export(ctx, se.Dir, a.String("format"))
	if err != nil {
		return err
	}
	x.h.Notify(core.Notice{ID: "tour.export", Severity: core.SevInfo, Source: "tour", Text: fmt.Sprintf("Wrote %s (%s).", name, humanBytes(int64(size)))})
	if !a.Bool("ask") {
		return nil
	}
	return x.h.Run(ctx, "request.comment", registry.Args{"thread": string(se.PlanThread()), "body": "**Export**: `" + name +
		"` has just been written from the current steps. Publish it as an artifact, record the link with `mcp__oriel__tour_share`, and reply here with it."})
}

// export writes the tour at root as html or md, and says what it wrote.
func (x *tourPlugin) export(ctx context.Context, root, format string) (string, int, error) {
	info, ok := worker.TourOf(root)
	if !ok {
		return "", 0, errors.New("this session isn't a tour")
	}
	t, err := x.loader.Load(ctx, root, info)
	if err != nil {
		return "", 0, err
	}
	name, b := tour.HTMLFile, []byte(nil)
	switch strings.ToLower(cmp.Or(format, "html")) {
	case "md", "markdown":
		name, b = tour.MarkdownFile, tour.Markdown(t)
	case "html":
		b = tour.HTML(t, x.htmlOptions())
	default:
		return "", 0, fmt.Errorf("%q isn't html or md", format)
	}
	if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
		return "", 0, err
	}
	return name, len(b), nil
}

func (x *tourPlugin) toolExport(ctx registry.Ctx, p map[string]any) (any, error) {
	id := core.SessionID(registry.SessionOf(ctx))
	se, ok := x.h.Session(id)
	if !ok {
		return nil, errors.New("no session asked")
	}
	root := se.Snapshot().Dir
	format, _ := p["format"].(string)
	name, size, err := x.export(ctx, root, format)
	if err != nil {
		return nil, err
	}
	out := fmt.Sprintf("%s (%s)", filepath.Join(root, name), humanBytes(int64(size)))
	if name == tour.HTMLFile {
		out += "\nSelf-contained: no fetches, nothing external. Publish it as an artifact as it is."
	}
	return out, nil
}

// htmlOptions colour the export's code with the window's languages and its theme, dark and light.
func (x *tourPlugin) htmlOptions() tour.HTMLOptions {
	name := "oriel"
	if v, _, err := x.h.Options().Get("ui.theme", registry.At{}); err == nil {
		name, _ = v.(string)
	}
	srcs, _ := x.h.Themes()
	resolve := func(v theme.Variant) *theme.Resolved {
		r, err := resolveTheme(srcs, name, v)
		if err != nil {
			r, _ = resolveTheme(theme.Builtins(), "oriel", v)
		}
		return r
	}
	dark, light := resolve(theme.Dark), resolve(theme.Light)
	groups := map[string]bool{}
	for _, g := range dark.SyntaxGroups() {
		groups[g] = true
	}
	return tour.HTMLOptions{
		Palette: tour.Palette(exportColors(dark), exportColors(light)),
		Highlight: func(path, lang string, src []byte, start, end int) [][]tour.Span {
			if path == "" {
				path = exampleName(x.h, lang)
			}
			if path == "" || end < start {
				return nil
			}
			f := highlightAround(x.h, path, src, start, 0, end-start)
			out := make([][]tour.Span, len(f.spans))
			for i, line := range f.spans {
				for _, sp := range line {
					out[i] = append(out[i], tour.Span{Text: strings.TrimRight(sp.text, "\r"), Class: syntaxClass(groups, sp.capture)})
				}
			}
			return out
		},
	}
}

// exampleName is a file name a language would be found by, for an example fenced with its name.
func exampleName(r registry.Registry, lang string) string {
	if l, ok := languages(r).Language(strings.ToLower(lang)); ok && len(l.Extensions) > 0 {
		return "example" + l.Extensions[0]
	}
	if lang != "" {
		return "example." + lang
	}
	return ""
}

// syntaxClass is a capture's class in the export: its theme group's, "" when the theme has none for it.
func syntaxClass(groups map[string]bool, capture string) string {
	for name := "@" + capture; capture != ""; {
		if groups[name] {
			return "hl-" + strings.NewReplacer("@", "", ".", "-").Replace(name)
		}
		i := strings.LastIndexByte(name, '.')
		if i < 0 {
			return ""
		}
		name = name[:i]
	}
	return ""
}

func exportColors(r *theme.Resolved) tour.Colors {
	hex := func(c theme.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
	tok := func(name string, fallback theme.TokenID) string {
		if c, ok := r.Color(name); ok {
			return hex(c)
		}
		return hex(r.C(fallback))
	}
	c := tour.Colors{Vars: map[string]string{
		"bg": hex(r.C(theme.BgBase)), "bg-raised": hex(r.C(theme.BgSurface)), "bg-lift": hex(r.C(theme.BgRaised)), "bg-sunken": hex(r.C(theme.BgBase)),
		"fg": hex(r.C(theme.Fg)), "fg-muted": hex(r.C(theme.FgMuted)), "fg-faint": hex(r.C(theme.FgFaint)), "border": hex(r.C(theme.Line)),
		"border-strong": hex(r.C(theme.LineStrong)), "accent": hex(r.C(theme.Accent)), "accent-fg": hex(r.C(theme.AccentInk)),
		"add-fg": hex(r.C(theme.OK)), "del-fg": hex(r.C(theme.Error)), "agent": tok("violet", theme.StatusPermission)},
		Code: map[string]string{}}
	for _, g := range r.SyntaxGroups() {
		s := r.Syntax(g)
		decl := "color: " + hex(s.FG)
		if s.Italic {
			decl += "; font-style: italic"
		}
		if s.Bold {
			decl += "; font-weight: 600"
		}
		c.Code[syntaxClass(map[string]bool{g: true}, strings.TrimPrefix(g, "@"))] = decl
	}
	return c
}
