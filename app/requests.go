package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The requests' views (ui.md 01b): the list, one request's conversation, and 01's summary at the right.
const (
	RequestsView = "requests"
	RequestView  = "request"
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
)

// reqGroup is where a request sits in the list: what it waits on.
type reqGroup int

const (
	groupWorking reqGroup = iota
	groupNeedsYou
	groupOpen
	groupResolved
)

var groupNames = []string{"Working", "Needs you", "Open", "Resolved"}

var groupTokens = []theme.TokenID{theme.StatusWorking, theme.StatusNeedsYou, theme.FgFaint, theme.StatusQuiet}

func groupOf(t core.Thread) reqGroup {
	switch {
	case !t.Open():
		return groupResolved
	case t.Busy:
		return groupWorking
	case t.AgentAnswered() && !t.Btw:
		return groupNeedsYou
	}
	return groupOpen
}

// requests is AI-5: the Requests view, a request's conversation and composer, threads between the editor's lines,
// the threads picker, the summary at 01's right, and the request.* commands they all run.
type requests struct {
	disposer
	sh *shell
	h  ext.Host
	by map[core.SessionID]*requestsState

	anchors map[threadAnchor]text.AnchorID
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
	return cmpOr(a.title, "attached text")
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
	list      scrollState
	convo     scrollState
	focusList bool
	inline    *inlineNew
}

func (x *requests) Manifest() ext.Manifest { return ext.Manifest{ID: "requests"} }

func (x *requests) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.by = map[core.SessionID]*requestsState{}
	x.anchors = map[threadAnchor]text.AnchorID{}
	inFront := func(registry.Args) bool { return x.sh.sessions != nil && h.State().Focus != "" }
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
	cmds := []registry.Command{
		{ID: "request.comment", Title: "Comment on a request: {thread} of {id} (the session in front by default), or a new one about {file}:{line} or the whole change; {body}, {title}, {urgent} interrupts the turn, {btw} asks a fork on the side",
			Available: inFront, Run: func(ctx registry.Ctx, a registry.Args) error { return x.sh.sessions.comment(ctx, a) }},
		{ID: "request.resolve", Title: "Resolve a request: {thread} of {id}, the one selected by default; or set its {status}: open, resolved, deferred, dismissed",
			Available: inFront, Run: func(ctx registry.Ctx, a registry.Args) error {
				a = selected(a)
				if a.String("thread") == "" {
					return errors.New("no request is selected")
				}
				return x.sh.sessions.setThread(ctx, a)
			}},
		{ID: "request.reopen", Title: "Reopen a resolved request: {thread}, the one selected by default", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				a = selected(a)
				a["status"] = string(core.ThreadOpen)
				return x.sh.sessions.setThread(ctx, a)
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
		{ID: "thread.new", Title: "Open a thread on the line at the cursor: ask the agent about it, between the lines", Available: inFront,
			Run: func(registry.Ctx, registry.Args) error { return x.newInline() }},
		{ID: "requests.summary", Title: "Show the session's summary at the right: its requests, tasks and tally, or hide it", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.toggle", registry.Args{"id": SummaryView, "place": "dock", "edge": "right", "size": 380, "title": "Session"})
		}},
	}
	for _, c := range cmds {
		x.add(h.Command(c))
	}
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
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: RequestComposerKeyMode, Keys: "<D-CR>", Command: "request.send", Args: registry.Args{"urgent": true},
		Desc: "Send now, interrupting the turn"}))
	x.add(h.Contribute(registry.MenuItem{Name: "requests.ask", Label: "Ask about this…", Group: "agent", Order: 10, When: registry.MenuWhen{View: EditorView},
		Command: "thread.new"}))
	x.add(h.Picker(x.threadsSource()))
	x.add(h.Keymap(registry.Keymap{Mode: "n", Keys: "<Leader>r", Command: "picker.open", Args: registry.Args{"source": "threads"}, Desc: "Threads"}))
	x.add(h.Decorations("threads", registry.DecorationFunc(x.decorations)))
	x.add(h.View(RequestsView, requestsList{x}))
	x.add(h.View(RequestView, requestView{x}))
	x.add(h.View(SummaryView, summaryView{x}))
	return nil
}

// front is the session in front.
func (x *requests) front() (core.SessionState, bool) {
	st := x.h.State()
	if se, ok := st.Session(st.Focus); ok {
		return se.Snapshot(), true
	}
	return core.SessionState{}, false
}

// state is a session's requests state, the one in front's for "".
func (x *requests) state(id core.SessionID) *requestsState {
	if id == "" {
		id = x.h.State().Focus
	}
	st := x.by[id]
	if st == nil {
		st = &requestsState{drafts: map[core.ThreadID]*draft{}, list: scrollState{}, convo: scrollState{follow: true}}
		x.by[id] = st
	}
	return st
}

func (x *requests) draftFor(st *requestsState, id core.ThreadID) *draft {
	d := st.drafts[id]
	if d == nil {
		d = &draft{}
		st.drafts[id] = d
	}
	return d
}

// target is the thread the composer writes to: the selected one, or newKey for a new request.
func (x *requests) target(st *requestsState) core.ThreadID {
	if st.writing {
		return newKey
	}
	return st.sel
}

// visible are the session's requests in the list's order, filtered: by group, then the latest first.
func (x *requests) visible(se core.SessionState, st *requestsState) []core.Thread {
	var out []core.Thread
	q := strings.ToLower(strings.TrimSpace(st.query))
	for _, t := range se.Threads {
		if st.filter > 0 && int(groupOf(t)) != filterGroups[st.filter] {
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
		return cmp.Or(cmp.Compare(groupOf(a), groupOf(b)), b.Updated.Compare(a.Updated))
	})
	return out
}

// filterGroups are the filter chips' groups, after All.
var filterGroups = []int{-1, int(groupNeedsYou), int(groupWorking), int(groupOpen), int(groupResolved)}

// selection is the selected thread, following a new request to its thread once it arrives, and defaulting to the
// first in the list.
func (x *requests) selection(se core.SessionState, st *requestsState) (core.Thread, bool) {
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

func (x *requests) move(d int) {
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
	st.list.reveal = string(st.sel)
	st.convo.follow = true
}

// openRequest shows the request view in the main area.
func (x *requests) openRequest(ctx context.Context) error {
	for _, v := range x.h.Layout().Visible() {
		if v.ID == RequestView {
			return nil
		}
	}
	return x.h.Run(ctx, "view.open", registry.Args{"id": RequestView, "place": "tab"})
}

// newRequest starts writing a request that isn't a thread yet.
func (x *requests) newRequest(ctx context.Context, a registry.Args) error {
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
func (x *requests) attach(ctx context.Context, a registry.Args) error {
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
			fmt.Fprintf(&b, "%s:\n```\n%s\n```", cmpOr(a.title, "attached"), strings.TrimRight(a.text, "\n"))
		}
	}
	return strings.TrimSpace(b.String())
}

// send sends the draft for id (newKey: a new request) through request.comment.
func (x *requests) send(ctx context.Context, st *requestsState, id core.ThreadID, urgent bool) error {
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
	st.convo.follow = true
	return nil
}

// openAnchor opens the selected request's anchor in the editor.
func (x *requests) openAnchor(ctx context.Context) error {
	se, ok := x.front()
	if !ok {
		return errors.New("no session is in front")
	}
	t, ok := x.selection(se, x.state(se.ID))
	if !ok || t.Anchor == nil {
		return errors.New("the request isn't anchored to a line")
	}
	return x.h.Run(ctx, "editor.open", registry.Args{"path": anchorPath(se, t.Anchor), "line": t.Anchor.Line})
}

// anchorPath is where an anchor's file is on the session's worker.
func anchorPath(se core.SessionState, a *core.Anchor) string {
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

// run runs a command from a click, saying what went wrong in a notice.
func (x *requests) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error(), Time: time.Now()})
	}
}

// threadsSource is the threads picker (␣t): every request of the session in front.
func (x *requests) threadsSource() registry.PickerSource {
	return registry.PickerSource{ID: "threads", Title: "Threads", Order: 35,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			se, ok := x.front()
			if !ok {
				return nil
			}
			var out []registry.PickerItem
			now := time.Now()
			for _, t := range x.visible(se, &requestsState{}) {
				g := groupOf(t)
				it := registry.PickerItem{ID: string(t.ID), Label: t.Heading(), Detail: threadPlace(t), Note: ago(now, t.Updated),
					IconStyle: groupTokens[g].Name(), Data: t}
				if g == groupNeedsYou {
					it.Boost = 30
				}
				out = append(out, it)
			}
			return out
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
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
	switch g := groupOf(t); g {
	case groupWorking:
		return providerName(se.Agent.Provider) + " · working", theme.StatusWorking
	case groupNeedsYou:
		return "needs you", theme.StatusNeedsYou
	case groupResolved:
		return string(t.Status), theme.StatusQuiet
	}
	switch {
	case t.Btw:
		return "btw", theme.FgFaint
	case len(t.Undelivered()) > 0:
		return "queued", tQueued
	}
	return "open", theme.FgFaint
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
		return providerName(cmpOr(c.Name, se.Agent.Provider)) + ": " + firstLine(strings.TrimSpace(plainMarkdown(c.Body)))
	}
	return firstLine(strings.TrimSpace(plainMarkdown(c.Body)))
}

func msgs(n int) string { return fmt.Sprintf("%d %s", n, plural(n, "msg", "msgs")) }

// avatar is a comment's author as a small square of initials.
func avatar(c *ui.Ctx, key any, cm core.Comment) {
	th := c.Theme()
	tone, label := th.C(tReqAvatarYou), cmpOr(initials(cm.Name), "U")
	if cm.Author == core.AuthorAgent {
		tone, label = th.C(tReqAvatarAI), strings.ToUpper(providerName(cm.Name)[:1])
	}
	c.Box(key, ui.Style{W: ui.Px(28), H: ui.Px(28), Radius: 6, Fill: tone, Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", label, ui.TextStyle{Font: kickerFont(render.Color{}).Font, Color: th.C(theme.AccentInk)})
	})
}
