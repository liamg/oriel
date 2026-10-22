package agentui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The requests' views: the list, one request's conversation, and the summary at the right.
const (
	RequestsView = kit.RequestsView
	RequestView  = kit.RequestView
	SummaryView  = "requests.summary"
)

// Their key modes: the list and the conversation, and the composer.
const (
	RequestsKeyMode        = "requests"
	RequestComposerKeyMode = "request.composer"
)

var (
	tReqRowOn     = theme.Declare("requests.row.selected", "accent.bg")
	tReqRowEdge   = theme.Declare("requests.row.selected.edge", "accent")
	tReqAvatarYou = theme.Declare("requests.avatar.you", "amber")
	tReqAvatarAI  = theme.Declare("requests.avatar.agent", "brand")
	tReqAnchorOn  = theme.DeclareOn("requests.anchor.line", "alpha(accent, 8%)", "bg.surface")
	tReqUnbacked  = theme.Declare("requests.unbacked", "warn")
	tReqThreadBg  = theme.Declare("requests.inline.bg", "bg.surface")
	tReqThreadBd  = theme.Declare("requests.inline.border", "line.strong")
	tReqDraftBg   = theme.Declare("requests.draft.bg", "alpha(violet, 4%)")
	tReqDraftBd   = theme.Declare("requests.draft.border", "alpha(violet, 35%)")
)

var groupNames = []string{"Working", "Needs you", "Open", "Resolved"}

var groupTokens = []theme.TokenID{theme.StatusWorking, theme.StatusNeedsYou, theme.FgFaint, theme.StatusQuiet}

// Requests is the Requests view, a request's conversation and composer, threads between the editor's lines,
// the threads picker, the summary at the IDE's right, and the request.* commands they all run.
type Requests struct {
	kit.Disposer
	h  plugin.Host
	by map[core.SessionID]*requestsState

	anchors map[threadAnchor]text.AnchorID

	shown      map[string]threadsShown          // windows that showed or hid their threads
	bubbles    map[string]map[int]core.ThreadID // each window's speech bubbles, by line, as last decorated
	lineNow    map[core.ThreadID]int            // where each thread was last decorated, 0-based
	reveal     core.ThreadID                    // a thread to bring into view
	focusReply core.ThreadID                    // a thread whose reply field takes focus
	unfolded   map[core.ThreadID]bool           // closed threads opened to read in a presentation's margin

	edits map[string]*agentEdit // the agent's latest edit to each file decorated, by path
	files kit.FileSrcs          // the anchored files' text, by path
}

// draft is what's being written to a request: its text, how it goes, and what's attached.
type draft struct {
	text          string
	urgent, btw   bool
	attach        []attachment
	compose, done bool // focus its input next frame; it was just sent
}

// attachment is context given to a request: a place in a file, or text (a terminal's output, a failure).
type attachment struct {
	path  string
	line  int
	title string
	text  string
}

func (a attachment) label() string {
	if a.path != "" {
		if a.line > 0 {
			return fmt.Sprintf("@%s:%d", a.path, a.line)
		}
		return "@" + a.path
	}
	return cmp.Or(a.title, "attached text")
}

// newKey is the draft of a request not yet sent.
const newKey = ""

// requestsState is one session's requests: what's selected, filtered and being written.
type requestsState struct {
	sel       core.ThreadID
	writing   bool // the new request's composer is showing, not a thread
	newAnchor *core.Anchor
	filter    int
	query     string
	drafts    map[core.ThreadID]*draft
	awaiting  string // a new request's body, sent, to select once its thread arrives
	list      kit.Scroll
	convo     kit.Scroll
	focusList bool
	inline    *inlineNew
}

func (x *Requests) Manifest() plugin.Manifest { return plugin.Manifest{ID: "requests"} }

func (x *Requests) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.by = map[core.SessionID]*requestsState{}
	x.anchors = map[threadAnchor]text.AnchorID{}
	x.shown, x.bubbles, x.lineNow = map[string]threadsShown{}, map[string]map[int]core.ThreadID{}, map[core.ThreadID]int{}
	x.unfolded = map[core.ThreadID]bool{}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Contribute(registry.MenuItem{Name: "requests.ask", Label: "Ask about this…", Group: "agent", Order: 10, When: registry.MenuWhen{View: kit.EditorView},
		Command: "thread.new"}))
	x.Add(h.Picker(x.threadsSource()))
	x.Add(h.Decorations("threads", uireg.KeyedDecorations{Key: x.threadsKey, Make: x.decorations}))
	x.Add(h.Decorations("agent.edits", uireg.KeyedDecorations{Key: func(uireg.DecorationCtx) any {
		se, ok := x.front()
		if !ok {
			return nil
		}
		return kit.TranscriptOf(se)
	}, Make: x.agentEdits}))
	for _, o := range threadsOptions() {
		x.Add(h.Option(o))
	}
	x.Add(h.Contribute(x.threadsAction()))
	x.Add(h.Contribute(x.hiddenLayer()))
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	for id, v := range x.views() {
		x.Add(h.View(id, v))
	}
	return nil
}

func (x *Requests) commands() []registry.Command {
	h := x.h
	inFront := func(registry.Args) bool { return h.State().Focus != "" }
	selected := func(a registry.Args) registry.Args {
		out := registry.Args{}
		for k, v := range a {
			out[k] = v
		}
		if out.String("thread") == "" {
			if st := x.state(""); st != nil && !st.writing {
				out["thread"] = string(st.sel)
			}
		}
		return out
	}
	return []registry.Command{
		{ID: "request.comment", Title: "Comment on a request: {thread} of {id} (the session in front by default), or a new one about {file}:{line} or the whole change; {body}, {title}, {urgent} interrupts the turn, {btw} asks a fork on the side, {status} opens a new one resolved (a note the agent doesn't get); a new one rated {severity}, opened from the AI review's {finding}; {outgoing} a new one for a pull request's author, in the review you'll submit, not the agent's to answer",
			Available: inFront, Run: func(ctx registry.Ctx, a registry.Args) error { return x.commentOn(ctx, a) }},
		{ID: "request.resolve", Title: "Resolve a request: {thread} of {id}, the one selected by default; or set its {status}: open, resolved, deferred, dismissed",
			Available: inFront, Run: func(ctx registry.Ctx, a registry.Args) error {
				a = selected(a)
				if a.String("thread") == "" {
					return errors.New("no request is selected")
				}
				return x.setThread(a)
			}},
		{ID: "request.reopen", Title: "Reopen a resolved request: {thread}, the one selected by default", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				a = selected(a)
				a["status"] = string(core.ThreadOpen)
				return x.setThread(a)
			}},
		{ID: "request.new", Title: "Write a new request: {text} to start from, {context} attached, about {file}:{line} or the whole change", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.newRequest(ctx, a) }},
		{ID: "request.open", Title: "Open a request: {thread}, the one selected by default, in the main area", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				st := x.state("")
				if t := a.String("thread"); t != "" {
					st.sel, st.writing = core.ThreadID(t), false
				}
				return x.openRequest(ctx)
			}},
		{ID: "request.next", Title: "Requests: the next request", Run: func(registry.Ctx, registry.Args) error { x.move(1); return nil }},
		{ID: "request.prev", Title: "Requests: the request before", Run: func(registry.Ctx, registry.Args) error { x.move(-1); return nil }},
		{ID: "request.compose", Title: "Requests: write to the selected request", Available: inFront, Run: func(ctx registry.Ctx, _ registry.Args) error {
			st := x.state("")
			x.draftFor(st, x.target(st)).compose = true
			return x.openRequest(ctx)
		}},
		{ID: "request.anchor", Title: "Requests: open the selected request's anchor in the editor", Available: inFront,
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.openAnchor(ctx) }},
		{ID: "request.send", Title: "Requests: send what's in the composer; {urgent} interrupts the turn", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				st := x.state("")
				return x.send(ctx, st, x.target(st), a.Bool("urgent"))
			}},
		{ID: "request.attach", Title: "Attach to the request being written: {items} ({path, line, text} each), or {text} with a {title}", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.attach(ctx, a) }},
		{ID: "thread.new", Title: "Open a thread on {file}'s {line} through {end_line}, or the line at the cursor, between the lines: {to} the agent, to ask it, or the author, a comment in the review you'll submit (a review session's default)",
			Available: inFront, Run: func(_ registry.Ctx, a registry.Args) error { return x.newInline(a) }},
		{ID: "threads.toggle", Title: "Show or hide the threads of the file in a {window}, the focused one by default",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.toggleThreads(a) }},
		{ID: "threads.show", Title: "Show the threads of the file in a {window}, at {thread} (the cursor line's by default) with its reply field ready",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.showThreads(a) }},
		{ID: "requests.summary", Title: "Show the session's summary at the right: its requests, tasks and stats, or hide it", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.toggle", registry.Args{"id": SummaryView, "place": "dock", "edge": "right", "size": 380, "title": "Session"})
		}},
	}
}

func (x *Requests) keymaps() []registry.Keymap {
	var out []registry.Keymap
	for _, k := range []registry.Keymap{
		{Keys: "j", Command: "request.next", Desc: "Next request"},
		{Keys: "<Down>", Command: "request.next", Desc: "Next request"},
		{Keys: "k", Command: "request.prev", Desc: "Request before"},
		{Keys: "<Up>", Command: "request.prev", Desc: "Request before"},
		{Keys: "<CR>", Command: "request.compose", Desc: "Write to it"},
		{Keys: "o", Command: "request.anchor", Desc: "Open its anchor"},
		{Keys: "R", Command: "request.resolve", Desc: "Resolve it"},
		{Keys: "<D-n>", Command: "request.new", Desc: "New request"},
	} {
		k.Mode = RequestsKeyMode
		out = append(out, k)
	}
	return append(out, []registry.Keymap{
		{Mode: RequestComposerKeyMode, Keys: "<D-CR>", Command: "request.send", Args: registry.Args{"urgent": true},
			Desc: "Send now, interrupting the turn"},
		{Mode: "n", Keys: "<Leader>r", Command: "picker.open", Args: registry.Args{"source": "threads"}, Desc: "Threads"},
		{Mode: "n", Keys: "ga", Command: "thread.new", Args: registry.Args{"to": "agent"}, Desc: "Ask the agent about this line"},
		{Mode: "n", Keys: "<Leader>R", Command: "threads.toggle", Desc: "Show or hide the file's threads"},
		{Mode: kit.PresentKeyMode, Keys: "<Leader>R", Command: "threads.toggle", Desc: "Show or hide the file's threads"},
		{Mode: ThreadsHiddenKeyMode, Keys: "<CR>", Command: "threads.show", Desc: "Show the line's threads"},
	}...)
}

func (x *Requests) views() map[string]uireg.View {
	return map[string]uireg.View{RequestsView: requestsList{x}, RequestView: requestView{x}, SummaryView: summaryView{x}}
}

// front is the session in front.
func (x *Requests) front() (core.SessionState, bool) {
	st := x.h.State()
	if se, ok := st.Session(st.Focus); ok {
		return se.Snapshot(), true
	}
	return core.SessionState{}, false
}

// state is a session's requests state, the one in front's for "".
func (x *Requests) state(id core.SessionID) *requestsState {
	if id == "" {
		id = x.h.State().Focus
	}
	st := x.by[id]
	if st == nil {
		st = &requestsState{drafts: map[core.ThreadID]*draft{}, list: kit.Scroll{}, convo: kit.Scroll{Follow: true}}
		x.by[id] = st
	}
	return st
}

func (x *Requests) draftFor(st *requestsState, id core.ThreadID) *draft {
	d := st.drafts[id]
	if d == nil {
		d = &draft{}
		st.drafts[id] = d
	}
	return d
}

// target is the thread the composer writes to: the selected one, or newKey for a new request.
func (x *Requests) target(st *requestsState) core.ThreadID {
	if st.writing {
		return newKey
	}
	return st.sel
}

// visible are the session's requests in the list's order, filtered: by group, then the latest first.
func (x *Requests) visible(se core.SessionState, st *requestsState) []core.Thread {
	var out []core.Thread
	q := strings.ToLower(strings.TrimSpace(st.query))
	for _, t := range se.Threads {
		if st.filter > 0 && int(kit.GroupOf(t)) != filterGroups[st.filter] {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(t.Heading()), q) && !slices.ContainsFunc(t.Comments, func(c core.Comment) bool {
			return strings.Contains(strings.ToLower(c.Body), q)
		}) {
			continue
		}
		out = append(out, t)
	}
	slices.SortStableFunc(out, func(a, b core.Thread) int {
		return cmp.Or(cmp.Compare(kit.GroupOf(a), kit.GroupOf(b)), b.Updated.Compare(a.Updated))
	})
	return out
}

// filterGroups are the filter chips' groups, after All.
var filterGroups = []int{-1, int(kit.GroupNeedsYou), int(kit.GroupWorking), int(kit.GroupOpen), int(kit.GroupResolved)}

// selection is the selected thread, following a new request to its thread once it arrives, and defaulting to the
// first in the list.
func (x *Requests) selection(se core.SessionState, st *requestsState) (core.Thread, bool) {
	if st.awaiting != "" {
		for _, t := range se.Threads {
			if c, ok := firstComment(t); ok && c.Body == st.awaiting {
				st.sel, st.writing, st.awaiting = t.ID, false, ""
			}
		}
	}
	if st.writing {
		return core.Thread{}, false
	}
	if t, ok := se.Thread(st.sel); ok {
		return t, true
	}
	if vs := x.visible(se, st); len(vs) > 0 {
		st.sel = vs[0].ID
		return vs[0], true
	}
	return core.Thread{}, false
}

func firstComment(t core.Thread) (core.Comment, bool) {
	if len(t.Comments) == 0 {
		return core.Comment{}, false
	}
	return t.Comments[0], true
}

func (x *Requests) move(d int) {
	se, ok := x.front()
	if !ok {
		return
	}
	st := x.state(se.ID)
	vs := x.visible(se, st)
	if len(vs) == 0 {
		return
	}
	i := slices.IndexFunc(vs, func(t core.Thread) bool { return t.ID == st.sel })
	switch {
	case i < 0 || st.writing:
		i = 0
	default:
		i = min(max(i+d, 0), len(vs)-1)
	}
	st.sel, st.writing = vs[i].ID, false
	st.list.Reveal = string(st.sel)
	st.convo.Follow = true
}

// openRequest shows the request view in the main area.
func (x *Requests) openRequest(ctx context.Context) error {
	for _, v := range x.h.Layout().Visible() {
		if v.ID == RequestView {
			return nil
		}
	}
	return x.h.Run(ctx, "view.open", registry.Args{"id": RequestView, "place": "tab"})
}

// newRequest starts writing a request that isn't a thread yet.
func (x *Requests) newRequest(ctx context.Context, a registry.Args) error {
	st := x.state("")
	st.writing = true
	st.newAnchor = nil
	d := x.draftFor(st, newKey)
	if t := a.String("text"); t != "" {
		d.text = t
	}
	if c := a.String("context"); c != "" {
		d.attach = append(d.attach, attachment{title: "context", text: c})
	}
	if f := a.String("file"); f != "" {
		st.newAnchor = &core.Anchor{File: f, Line: a.Int("line")}
	}
	d.compose = true
	return x.openRequest(ctx)
}

// attach adds the picker's items, or text, to the request being written, and opens it.
func (x *Requests) attach(ctx context.Context, a registry.Args) error {
	se, ok := x.front()
	if !ok {
		return errors.New("no session is in front")
	}
	st := x.state(se.ID)
	if _, ok := se.Thread(st.sel); !ok {
		st.writing = true
	}
	d := x.draftFor(st, x.target(st))
	items, _ := a["items"].([]any)
	if m, ok := a["items"].([]map[string]any); ok {
		for _, it := range m {
			items = append(items, it)
		}
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		path, _ := m["path"].(string)
		txt, _ := m["text"].(string)
		if strings.HasSuffix(filepath.ToSlash(path), filepath.ToSlash(txt)) {
			txt = "" // a file's own name says nothing more
		}
		at := attachment{path: relTo(se, path), text: strings.TrimSpace(txt), line: registry.Args(m).Int("line")}
		d.attach = append(d.attach, at)
	}
	if t := a.String("text"); t != "" {
		d.attach = append(d.attach, attachment{title: a.String("title"), text: t})
	}
	d.compose = true
	return x.openRequest(ctx)
}

// relTo is path relative to the session's project it's in, as the agent reads it.
func relTo(se core.SessionState, path string) string {
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	for _, p := range se.Projects {
		if rel, err := filepath.Rel(p.Dir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// body is what a draft sends: its text, then each attachment.
func (d *draft) body() string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(d.text))
	for _, a := range d.attach {
		b.WriteString("\n\n")
		switch {
		case a.path != "" && a.text != "":
			fmt.Fprintf(&b, "%s: %s", a.label(), a.text)
		case a.path != "":
			b.WriteString(a.label())
		default:
			fmt.Fprintf(&b, "%s:\n```\n%s\n```", cmp.Or(a.title, "attached"), strings.TrimRight(a.text, "\n"))
		}
	}
	return strings.TrimSpace(b.String())
}

// send sends the draft for id (newKey: a new request) through request.comment.
func (x *Requests) send(ctx context.Context, st *requestsState, id core.ThreadID, urgent bool) error {
	d := x.draftFor(st, id)
	body := d.body()
	if body == "" {
		return nil
	}
	a := registry.Args{"body": body, "urgent": urgent || d.urgent, "btw": d.btw}
	if id != newKey {
		a["thread"] = string(id)
	} else if an := st.newAnchor; an != nil {
		a["file"], a["line"] = an.File, an.Line
	}
	if err := x.h.Run(ctx, "request.comment", a); err != nil {
		return err
	}
	*d = draft{done: true}
	if id == newKey {
		st.awaiting, st.newAnchor = body, nil
	}
	st.convo.Follow = true
	return nil
}

// openAnchor opens the selected request's anchor in the editor.
func (x *Requests) openAnchor(ctx context.Context) error {
	se, ok := x.front()
	if !ok {
		return errors.New("no session is in front")
	}
	t, ok := x.selection(se, x.state(se.ID))
	if !ok || t.Anchor == nil {
		return errors.New("the request isn't anchored to a line")
	}
	return x.h.Run(ctx, "editor.open", registry.Args{"path": kit.AnchorPath(se, t.Anchor), "line": t.Anchor.Line})
}

// run runs a command from a click, saying what went wrong in a notice.
func (x *Requests) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error(), Time: time.Now()})
	}
}

// threadsSource is the threads picker (␣t): every request of the session in front.
func (x *Requests) threadsSource() uireg.PickerSource {
	return uireg.PickerSource{ID: "threads", Title: "Threads", Order: 35,
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			se, ok := x.front()
			if !ok {
				return nil
			}
			var out []uireg.PickerItem
			now := time.Now()
			for _, t := range x.visible(se, &requestsState{}) {
				g := kit.GroupOf(t)
				it := uireg.PickerItem{ID: string(t.ID), Label: t.Heading(), Detail: threadPlace(t), Note: kit.Ago(now, t.Updated),
					IconStyle: groupTokens[g].Name(), Data: t}
				if g == kit.GroupNeedsYou {
					it.Boost = 30
				}
				out = append(out, it)
			}
			return out
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			return x.h.Run(ctx, "request.open", registry.Args{"thread": it.ID})
		},
	}
}

// threadPlace is a thread's place as a list shows it: round.go:46, or the whole change.
func threadPlace(t core.Thread) string {
	if t.Anchor == nil {
		return "whole change"
	}
	return fmt.Sprintf("%s:%d", filepath.Base(t.Anchor.File), t.Anchor.Line)
}

// statusLabel is a request's chip: who has it and what it's doing.
func statusLabel(se core.SessionState, t core.Thread) (string, theme.TokenID) {
	switch g := kit.GroupOf(t); g {
	case kit.GroupWorking:
		return kit.ProviderName(se.Agent.Provider) + " · working", theme.StatusWorking
	case kit.GroupNeedsYou:
		return "needs you", theme.StatusNeedsYou
	case kit.GroupResolved:
		return string(t.Status), theme.StatusQuiet
	}
	switch {
	case t.Btw:
		return "btw", theme.FgFaint
	case t.Outgoing:
		return "for the author", theme.Accent
	case len(t.Undelivered()) > 0:
		return queuedLabel(se), tQueued
	}
	return "open", theme.FgFaint
}

// queuedLabel is what a request waiting for the agent says: that the agent is starting, when it is.
func queuedLabel(se core.SessionState) string {
	if se.Agent.Starting != nil {
		return "queued · agent starting"
	}
	return "queued"
}

// preview is the list's second line for a request: the last word in it.
func preview(se core.SessionState, t core.Thread) string {
	c, ok := t.Last()
	if !ok {
		return ""
	}
	switch {
	case t.Btw && c.Author == core.AuthorYou:
		return "side question · answered by a fork, not the session"
	case c.Author == core.AuthorYou && !c.Delivered && se.Agent.Working:
		return "queued · sends when the current turn ends"
	case c.Author == core.AuthorAgent:
		return kit.ProviderName(cmp.Or(c.Name, se.Agent.Provider)) + ": " + core.Summary(c.Body)
	}
	return core.Summary(c.Body)
}

func msgs(n int) string { return fmt.Sprintf("%d %s", n, kit.Plural(n, "msg", "msgs")) }

// avatar is a comment's author as a small square of initials.
func avatar(c *ui.Ctx, key any, cm core.Comment) {
	th := c.Theme()
	tone, label := th.C(tReqAvatarYou), cmp.Or(kit.Initials(cm.Name), "U")
	switch cm.Author {
	case core.AuthorAgent:
		tone, label = th.C(tReqAvatarAI), strings.ToUpper(kit.ProviderName(cm.Name)[:1])
	case core.AuthorHost:
		tone, label = th.C(kit.TAvatarGitHub), cmp.Or(kit.Initials(strings.NewReplacer("-", " ", "_", " ", ".", " ").Replace(cm.Name)), "GH")
	}
	c.Box(key, ui.Style{W: ui.Px(28), H: ui.Px(28), Radius: 6, Fill: tone, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", label, ui.TextStyle{Font: kit.KickerFont(render.Color{}).Font, Color: th.C(theme.AccentInk)})
	})
}

// requestsArgs are view.toggle's args for the Requests panel, as the rail and its key run it.
func requestsArgs() registry.Args {
	return kit.SidebarArgs("view.toggle", registry.Args{"id": RequestsView, "place": "dock", "edge": "left", "title": "Requests"})
}

// setThread sets a thread's status: resolved by default.
func (x *Requests) setThread(a registry.Args) error {
	se, w, err := kit.SessionArgs(x.h, a)
	if err != nil {
		return err
	}
	status := core.ThreadStatus(cmp.Or(a.String("status"), string(core.ThreadResolved)))
	return w.SetThread(host.ThreadReq{Session: se.ID, Thread: core.ThreadID(a.String("thread")), Status: status})
}

// commentOn sends a comment through the CommentSend hooks to the session host, which hands it to the agent:
// on {thread}, or opening one about {file}:{line}, or the whole change.
func (x *Requests) commentOn(ctx registry.Ctx, a registry.Args) error {
	se, w, err := kit.SessionArgs(x.h, a)
	if err != nil {
		return err
	}
	ev := &kit.CommentSend{Session: string(se.ID), Thread: a.String("thread"), File: a.String("file"), Line: a.Int("line"), Body: a.String("body"),
		Urgent: a.Bool("urgent"), Btw: a.Bool("btw")}
	if v, _ := x.h.Fire(ctx, "CommentSend", ev.File, ev); v.Veto {
		return fmt.Errorf("a CommentSend hook stopped it: %s", v.Reason)
	}
	if strings.TrimSpace(ev.Body) == "" {
		return errors.New("the comment is empty")
	}
	req := host.CommentReq{Session: se.ID, Thread: core.ThreadID(ev.Thread), Title: a.String("title"), Body: ev.Body, Urgent: ev.Urgent, Btw: ev.Btw,
		Status: core.ThreadStatus(a.String("status")), Severity: a.String("severity"), Finding: a.String("finding"), Outgoing: a.Bool("outgoing")}
	if ev.Thread == "" && ev.File != "" {
		req.Anchor = anchorIn(se, ev.File, ev.Line, a.Int("end_line"), a.String("text"))
	}
	_, err = w.Comment(ctx, req)
	return err
}

// anchorIn is a place in one of st's projects, from a path that may be absolute.
func anchorIn(st core.SessionState, file string, line, end int, text string) *core.Anchor {
	an := &core.Anchor{File: file, Line: max(line, 1), EndLine: end, Text: text}
	if p, ok := st.Primary(); ok {
		an.Project = p.Name
	}
	if !filepath.IsAbs(file) {
		return an
	}
	for _, p := range st.Projects {
		if kit.Within(file, p.Dir) {
			rel, _ := filepath.Rel(p.Dir, file)
			an.Project, an.File = p.Name, filepath.ToSlash(rel)
			return an
		}
	}
	if kit.Within(file, st.Dir) {
		rel, _ := filepath.Rel(st.Dir, file)
		an.Project, an.File = core.DocsProject, filepath.ToSlash(rel)
	}
	return an
}
