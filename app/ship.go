package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/worker"
)

// The Ship page and its panels (ui.md 18, 01h): each project's way from uncommitted work to a merged PR, read from
// its worker (worker.Ship), and the steps along it. Everything is a command, so keys, Lua and the buttons reach it
// the same way; the gates are ShipGate contributions, and ShipCommit and ShipRaise hooks can stop a step.

// The views.
const (
	ShipView        = "ship"         // the session's page: every project side by side
	PullRequestView = "pull-request" // a project's compact checklist, in the dock
	PRView          = "pr"           // Raise PR / Edit PR, a main tab; its instance is the project
)

// errStopped is a step a hook stopped; its reason is shown where the step was asked for.
var errStopped = errors.New("stopped by a hook")

// shipPoll is how often the session in front is read again while nothing else asks.
const shipPoll = 10 * time.Second

// ShipCommit is the ShipCommit hook's event: a commit about to be made from the Ship page. A veto stops it, its
// reason shown in the commit box; Force is "Commit anyway…", and a rewrite changes it (a trailer, say).
type ShipCommit struct {
	Session string   `json:"session"`
	Project string   `json:"project"`
	Message string   `json:"message"`
	Files   []string `json:"files,omitempty"`
	Force   bool     `json:"force,omitempty"`
}

// PrSeen is the PrSeen hook's event: a session's pull request, the first time the app reads it, whoever raised it
// (the Ship page, the agent, gh by hand).
type PrSeen struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Ticket  string `json:"ticket,omitempty"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Draft   bool   `json:"draft,omitempty"`
	Merged  bool   `json:"merged,omitempty"`
}

// PrMerged is the PrMerged hook's event: a session's pull request read as merged, the first time the app sees it so,
// whoever merged it. Open is how many of the session's other PRs are still open, or not known.
type PrMerged struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Ticket  string `json:"ticket,omitempty"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Open    int    `json:"open"`
}

// ShipRaise is the ShipRaise hook's event: a pull request about to be raised, or edited. A veto stops it, its reason
// shown on the Raise PR tab: a quiz to take first.
type ShipRaise struct {
	Session   string   `json:"session"`
	Project   string   `json:"project"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Draft     bool     `json:"draft,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
	Edit      bool     `json:"edit,omitempty"` // the PR is open already
	All       bool     `json:"all,omitempty"`  // one of ship.raise-all's, raised with the others in merge order
}

type shipExt struct {
	disposer
	sh     *shell
	h      ext.Host
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	busy   sync.WaitGroup // reads and steps
	saving sync.WaitGroup // drafts the user typed, on their way to the worker
	poller sync.WaitGroup

	mu      sync.Mutex
	reports map[core.SessionID]*shipReport
	seenPRs map[string]bool // the PRs PrSeen has fired for, by URL
	merged  map[string]bool // the PRs PrMerged has fired for, by URL

	// On the loop: what's typed, by session/project.
	commits map[string]*commitForm
	prs     map[string]*prForm
}

// shipReport is a session's projects as last read, and the steps running on them.
type shipReport struct {
	ships   []worker.Ship
	read    time.Time
	loading bool
	err     string
	doing   map[string]string // project → the step running: "Pushing…"
	failed  map[string]string // project → why its last step failed
}

// commitForm is a project's commit box.
type commitForm struct {
	message string
	edited  bool            // typed into, so the draft no longer fills it
	skip    map[string]bool // files left out
	veto    string          // why a commit gate or a ShipCommit hook stopped the last try
	held    []registry.GateCheck
	save    autosave
}

// prForm is a project's Raise PR tab.
type prForm struct {
	title, body string
	edited      bool
	save        autosave
	tab         int // write or preview
	reviewers   []string
	removed     []string // owners taken off
	adding      string
	veto        string
}

// draftSaveAfter is how long typing in a draft box rests before what's typed is kept on the worker.
const draftSaveAfter = time.Second

// autosave keeps what's typed into a draft box on the worker as the user's draft: at once on the first key, so the
// agent is neither asked for it nor overwrites it, then each time the typing rests.
type autosave struct {
	pending bool
	at      time.Time
}

// keepTyped is called each frame a draft box is drawn: changed says it was typed into, first that it hadn't been.
func (x *shipExt) keepTyped(c *ui.Ctx, a *autosave, changed, first bool, req worker.DraftReq) {
	now := c.Now()
	if changed {
		a.pending, a.at = true, now
	}
	if !a.pending {
		return
	}
	if !first && now.Sub(a.at) < draftSaveAfter {
		c.WakeAt(a.at.Add(draftSaveAfter))
		return
	}
	a.pending = false
	cl, err := x.client(req.Session)
	if err != nil {
		return
	}
	x.saving.Add(1)
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		defer x.saving.Done()
		ctx, cancel := context.WithTimeout(x.ctx, 30*time.Second)
		defer cancel()
		if err := cl.ShipDraft(ctx, req); err != nil {
			x.h.Log().Debug("ship", "draft", req.Project, "err", err)
		}
	}()
}

func (x *shipExt) Manifest() ext.Manifest { return ext.Manifest{ID: "ship"} }

func (x *shipExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	if x.now == nil {
		x.now = time.Now
	}
	x.reports = map[core.SessionID]*shipReport{}
	x.commits, x.prs = map[string]*commitForm{}, map[string]*prForm{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	x.add(h.DeclareEvent(registry.EventInfo{Name: "ShipCommit", Fired: true, Payload: ShipCommit{},
		Doc: "A commit about to be made from the Ship page; a veto stops it (its reason shows in the commit box) unless force, a rewrite changes it. { session, project, message, files, force }"}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "PrSeen", Fired: true, Payload: PrSeen{},
		Doc: "A session's pull request, the first time Oriel reads it, whoever raised it; the subject is its URL. { session, project, ticket, number, url, title, state, draft, merged }"}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "PrMerged", Fired: true, Payload: PrMerged{},
		Doc: "A session's pull request read as merged, the first time Oriel sees it so, whoever merged it; the subject is its URL. open is how many of the session's other PRs are still open. { session, project, ticket, number, url, title, open }"}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "ShipRaise", Fired: true, Payload: ShipRaise{},
		Doc: "A pull request about to be raised or edited from the Ship page; a veto stops it (a quiz to take first), a rewrite changes it. { session, project, title, body, draft, reviewers, edit, all }"}))
	x.add(h.Contribute(registry.Indicator{Name: "ci", Slot: registry.SlotHUD, Order: 70, Title: "CI queue", Help: hudHelp + "#ci", Note: x.ciNote,
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "ci", Fn: x.ciPanel}, nil }}))
	x.add(h.Contribute(registry.ProjectState{Name: "ship", Order: 10, State: x.projectState}))
	x.gates()
	x.shadowEntries()
	x.commands()
	x.views()
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.SessionFocused:
			if ev.ID != "" {
				x.refresh(ev.ID, false)
			}
		case core.ProjectsChanged:
			x.refresh(ev.ID, false)
		}
	})))
	x.poller.Add(1)
	go x.poll()
	return nil
}

func (x *shipExt) Deactivate(ctx context.Context) error {
	x.cancel()
	x.poller.Wait()
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

// poll reads the session in front again every shipPoll.
func (x *shipExt) poll() {
	defer x.poller.Done()
	t := time.NewTicker(shipPoll)
	defer t.Stop()
	for {
		select {
		case <-x.ctx.Done():
			return
		case <-t.C:
			if id := x.focused(); id != "" {
				x.refresh(id, false)
			}
		}
	}
}

func (x *shipExt) focused() core.SessionID { return x.sh.cfg.Store.Focus() }

// client is the worker a session is on.
func (x *shipExt) client(id core.SessionID) (worker.Client, error) {
	if x.sh.sessions == nil {
		return worker.Client{}, errors.New("no worker is connected")
	}
	return x.sh.sessions.client(id)
}

// session is the session in front, or {id}.
func (x *shipExt) session(a registry.Args) (core.SessionState, bool) {
	id := core.SessionID(a.String("id"))
	if id == "" {
		id = x.focused()
	}
	se, ok := x.h.Session(id)
	if !ok {
		return core.SessionState{}, false
	}
	return se.Snapshot(), true
}

// report is a copy of a session's report, with an empty one for a session not read yet.
func (x *shipExt) report(id core.SessionID) shipReport {
	x.mu.Lock()
	defer x.mu.Unlock()
	r, ok := x.reports[id]
	if !ok {
		return shipReport{}
	}
	out := *r
	out.ships = slices.Clone(r.ships)
	return out
}

// ship is a project's last reading.
func (x *shipExt) ship(id core.SessionID, project string) (worker.Ship, bool) {
	for _, s := range x.report(id).ships {
		if s.Project == project {
			return s, true
		}
	}
	return worker.Ship{}, false
}

// refresh reads a session's projects from its worker, off the loop; fresh asks GitHub (and fetches) again.
func (x *shipExt) refresh(id core.SessionID, fresh bool) {
	x.mu.Lock()
	r := x.reports[id]
	if r == nil {
		r = &shipReport{doing: map[string]string{}, failed: map[string]string{}}
		x.reports[id] = r
	}
	if r.loading && !fresh {
		x.mu.Unlock()
		return
	}
	r.loading = true
	x.mu.Unlock()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		var ships []worker.Ship
		c, err := x.client(id)
		if err == nil {
			ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
			ships, err = c.ShipRead(ctx, worker.ShipReq{Session: id, Fresh: fresh})
			cancel()
		}
		x.mu.Lock()
		r.loading = false
		if err != nil {
			r.err = err.Error()
		} else {
			r.ships, r.err, r.read = ships, "", time.Now()
		}
		seen, merged := x.newPRs(id, ships)
		x.mu.Unlock()
		if len(seen)+len(merged) > 0 {
			x.h.Do(func() {
				for _, ev := range seen {
					x.h.Fire(context.Background(), "PrSeen", ev.URL, &ev)
				}
				for _, ev := range merged {
					x.h.Fire(context.Background(), "PrMerged", ev.URL, &ev)
				}
			})
		}
		x.h.Redraw()
	}()
}

// newPRs are the PRs in ships not seen before, as PrSeen events, and those not seen merged before, as PrMerged
// events; x.mu is held.
func (x *shipExt) newPRs(id core.SessionID, ships []worker.Ship) (seen []PrSeen, merged []PrMerged) {
	if x.seenPRs == nil {
		x.seenPRs, x.merged = map[string]bool{}, map[string]bool{}
	}
	ticket := ""
	if se, ok := x.h.Session(id); ok {
		ticket = se.Snapshot().Ticket
	}
	open := 0 // a project GitHub didn't answer for counts as open
	for _, s := range ships {
		if s.PR.Open() || s.PR == nil && s.GitHubError != "" {
			open++
		}
	}
	for _, s := range ships {
		if s.PR == nil || s.PR.URL == "" {
			continue
		}
		if !x.seenPRs[s.PR.URL] {
			x.seenPRs[s.PR.URL] = true
			seen = append(seen, PrSeen{Session: string(id), Project: s.Project, Ticket: ticket, Number: s.PR.Number, URL: s.PR.URL,
				Title: s.PR.Title, State: s.PR.State, Draft: s.PR.Draft, Merged: s.PR.Merged})
		}
		if s.PR.Merged && !x.merged[s.PR.URL] {
			x.merged[s.PR.URL] = true
			merged = append(merged, PrMerged{Session: string(id), Project: s.Project, Ticket: ticket, Number: s.PR.Number, URL: s.PR.URL,
				Title: s.PR.Title, Open: open})
		}
	}
	return seen, merged
}

// wait is until every read and step started has finished.
func (x *shipExt) wait() { x.busy.Wait() }

// step runs one step on a project off the loop, showing doing while it runs; a failure is kept for the project and
// sent as a notice. The session is read again after.
func (x *shipExt) step(id core.SessionID, project, doing string, fn func(ctx context.Context, c worker.Client) error) error {
	c, err := x.client(id)
	if err != nil {
		return err
	}
	x.mu.Lock()
	r := x.reports[id]
	if r == nil {
		r = &shipReport{doing: map[string]string{}, failed: map[string]string{}}
		x.reports[id] = r
	}
	if d := r.doing[project]; d != "" {
		x.mu.Unlock()
		return fmt.Errorf("%s is busy: %s", project, d)
	}
	r.doing[project] = doing
	delete(r.failed, project)
	x.mu.Unlock()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(x.ctx, 5*time.Minute)
		err := fn(ctx, c)
		cancel()
		x.mu.Lock()
		delete(r.doing, project)
		if err != nil {
			r.failed[project] = err.Error()
		}
		x.mu.Unlock()
		if err != nil {
			x.h.Notify(core.Notice{ID: "ship." + project, Severity: core.SevError, Key: true, Title: project, Text: err.Error()})
		}
		x.refresh(id, true)
	}()
	return nil
}

// doing is the step running on a project, "" for none.
func (x *shipExt) doing(id core.SessionID, project string) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r := x.reports[id]; r != nil {
		return r.doing[project]
	}
	return ""
}

func (x *shipExt) failed(id core.SessionID, project string) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r := x.reports[id]; r != nil {
		return r.failed[project]
	}
	return ""
}

func formKey(id core.SessionID, project string) string { return string(id) + "/" + project }

// commitForm is a project's commit box, made on first use.
func (x *shipExt) commitForm(id core.SessionID, project string) *commitForm {
	k := formKey(id, project)
	f := x.commits[k]
	if f == nil {
		f = &commitForm{skip: map[string]bool{}}
		x.commits[k] = f
	}
	return f
}

// message is what a project's commit uses: what's typed, else the draft.
func (f *commitForm) messageFor(s worker.Ship) string {
	if f.edited {
		return f.message
	}
	return s.CommitDraft.Body
}

// files are the files a project's commit takes: every uncommitted one not left out.
func (f *commitForm) files(s worker.Ship) []string {
	var out []string
	for _, fl := range s.Files {
		if !f.skip[fl.Path] {
			out = append(out, fl.Path)
		}
	}
	return out
}

// prForm is a project's Raise PR tab, made on first use and filled from the PR, the draft or the template.
func (x *shipExt) prForm(id core.SessionID, s worker.Ship) *prForm {
	k := formKey(id, s.Project)
	f := x.prs[k]
	if f == nil {
		f = &prForm{}
		x.prs[k] = f
	}
	if !f.edited {
		f.title, f.body = prDefaults(s)
	}
	return f
}

// prDefaults are a PR's title and body before anything's typed: the open PR's, else the agent's draft, else the first
// commit's subject and the repo's template.
func prDefaults(s worker.Ship) (title, body string) {
	switch {
	case s.PR.Open():
		return s.PR.Title, s.PR.Body
	case !s.PRDraft.Empty():
		return s.PRDraft.Title, s.PRDraft.Body
	}
	if len(s.Unpushed) > 0 {
		title = s.Unpushed[len(s.Unpushed)-1].Subject
	}
	return title, s.Template
}

// reviewersOf are who a project's PR asks: the CODEOWNERS of its change and those added, less those taken off.
func (f *prForm) reviewersOf(s worker.Ship) []string {
	var out []string
	for _, r := range append(slices.Clone(s.Owners), f.reviewers...) {
		if !slices.Contains(out, r) && !slices.Contains(f.removed, r) {
			out = append(out, r)
		}
	}
	return out
}

// projectState is a header's PR and CI (ui.md 01h: "rate-limits · #790 ● CI failing").
func (x *shipExt) projectState(project, _ string) []registry.StatePart {
	id := x.focused()
	if id == "" {
		return nil
	}
	s, ok := x.ship(id, project)
	if !ok || s.ReadOnly || s.GitHub == "" {
		return nil
	}
	if s.PR == nil {
		if s.Commits > 0 || len(s.Files) > 0 {
			return []registry.StatePart{{Text: "no PR"}}
		}
		return nil
	}
	out := []registry.StatePart{{Text: fmt.Sprintf("#%d", s.PR.Number)}}
	if s.PR.Merged {
		return append(out, registry.StatePart{Text: "merged", Style: "violet", Dot: true})
	}
	switch ci := ciOf(s.Checks); ci {
	case gh.CheckFail:
		out = append(out, registry.StatePart{Text: "CI failing", Style: "error", Dot: true})
	case gh.CheckPending:
		out = append(out, registry.StatePart{Text: "CI running", Style: "cyan", Dot: true})
	case gh.CheckPass:
		out = append(out, registry.StatePart{Text: "CI passing", Style: "ok", Dot: true})
	}
	return out
}

// ciOf is the checks in one word: fail if any failed, else pending if any are running, else pass; "" for none.
func ciOf(cs []gh.Check) string {
	if len(cs) == 0 {
		return ""
	}
	out := gh.CheckPass
	for _, c := range cs {
		switch c.State() {
		case gh.CheckFail:
			return gh.CheckFail
		case gh.CheckPending:
			out = gh.CheckPending
		}
	}
	return out
}

// gates are the "before this ships" gates Oriel can tell now; the review ledger, the verify quiz and AI review
// register theirs with their tickets.
func (x *shipExt) gates() {
	sess := func(id string) (core.SessionState, bool) {
		se, ok := x.h.Session(core.SessionID(id))
		if !ok {
			return core.SessionState{}, false
		}
		return se.Snapshot(), true
	}
	x.add(x.h.Contribute(registry.ShipGate{Name: "plan", Title: "Plan approved", Order: 10, Check: func(id string) registry.Gate {
		se, ok := sess(id)
		switch {
		case !ok:
			return registry.Gate{}
		case se.Planning():
			return registry.Gate{State: registry.GatePending, Note: "planning"}
		case se.StartWork && se.Started:
			return registry.Gate{State: registry.GatePass}
		case se.Ticket != "":
			return registry.Gate{State: registry.GatePass, Note: se.Ticket}
		case se.Unplanned:
			return registry.Gate{State: registry.GateWarn, Note: "unplanned"}
		}
		return registry.Gate{}
	}}))
	x.add(x.h.Contribute(registry.ShipGate{Name: "threads", Title: "All threads resolved", Order: 20, Check: func(id string) registry.Gate {
		se, ok := sess(id)
		if !ok || len(se.Threads) == 0 {
			return registry.Gate{}
		}
		open := 0
		for _, t := range se.Threads {
			if t.Open() {
				open++
			}
		}
		if open > 0 {
			return registry.Gate{State: registry.GateWarn, Note: fmt.Sprintf("%d open", open)}
		}
		return registry.Gate{State: registry.GatePass, Note: fmt.Sprint(len(se.Threads))}
	}}))
	x.add(x.h.Contribute(registry.ShipGate{Name: "tests", Title: "Tests ran this session", Order: 40, Check: func(id string) registry.Gate {
		se, ok := sess(id)
		if !ok {
			return registry.Gate{}
		}
		if len(se.Runs) == 0 {
			return registry.Gate{State: registry.GatePending, Note: "none yet"}
		}
		last := se.Runs[len(se.Runs)-1]
		note := strings.Join(strings.Fields(last.Command), " ")
		if f := strings.Fields(note); len(f) > 2 {
			note = strings.Join(f[:2], " ")
		}
		if !last.Ended.IsZero() {
			note += " " + agoText(x.now(), last.Ended)
		}
		if !last.OK {
			return registry.Gate{State: registry.GateFail, Note: note}
		}
		return registry.Gate{State: registry.GatePass, Note: note}
	}}))
}

// shadowEntries gives the sidebar's Pull request entry its note ("#790", "not raised"), shadowing the rail's.
func (x *shipExt) shadowEntries() {
	for _, e := range registry.ProjectEntries(x.h) {
		if e.Name != "pull-request" {
			continue
		}
		e.Note = func(project string) string {
			w := projectsHere(x.h)
			if w.session == nil {
				return ""
			}
			if project == "" {
				project = w.open
			}
			s, ok := x.ship(w.session.ID, project)
			switch {
			case !ok:
				return ""
			case s.PR != nil:
				return fmt.Sprintf("#%d", s.PR.Number)
			case !x.prForm(w.session.ID, s).edited && s.PRDraft.Empty():
				return "not raised"
			}
			return "drafting"
		}
		e.Source = "ship"
		x.add(x.h.Contribute(e))
	}
}

// project is the project a command names, else the open one.
func (x *shipExt) project(a registry.Args) (core.SessionState, string, error) {
	se, ok := x.session(a)
	if !ok {
		return se, "", errors.New("no session is in front")
	}
	p := a.String("project")
	if p == "" {
		p = se.OpenProject()
	}
	if !slices.ContainsFunc(se.Projects, func(q core.Project) bool { return q.Name == p }) {
		return se, "", fmt.Errorf("the session has no project %q", p)
	}
	return se, p, nil
}

func (x *shipExt) commands() {
	inFront := func(registry.Args) bool { _, ok := x.session(nil); return ok && x.sh.sessions != nil }
	run := func(fn func(ctx registry.Ctx, se core.SessionState, project string, a registry.Args) error) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, a registry.Args) error {
			se, p, err := x.project(a)
			if err != nil {
				return err
			}
			return fn(ctx, se, p, a)
		}
	}
	for _, c := range []registry.Command{
		{ID: "ship.open", Title: "Open the Ship page: every project's way to a merged PR", Available: inFront,
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return x.h.Run(ctx, "view.open", registry.Args{"id": ShipView, "place": "tab", "title": "Ship"})
			}},
		{ID: "ship.refresh", Title: "Read the session's projects again, from their worktrees and GitHub", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				x.refresh(se.ID, true)
				return nil
			}},
		{ID: "ship.commit", Title: "Commit a project's changes: {project}, {message} (its draft by default), {files} (all by default); {force} commits past a gate",
			Available: inFront, Run: run(x.commit)},
		{ID: "ship.commit-all", Title: "Commit every project's changes, each with its message", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				var errs []error
				for _, s := range x.report(se.ID).ships {
					if len(s.Files) > 0 && !s.ReadOnly {
						errs = append(errs, x.commit(ctx, se, s.Project, registry.Args{}))
					}
				}
				return errors.Join(errs...)
			}},
		{ID: "ship.push", Title: "Push a project's branch to origin: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				return x.step(se.ID, p, "Pushing…", func(ctx context.Context, c worker.Client) error {
					return c.ShipPush(ctx, worker.ShipRef{Session: se.ID, Project: p})
				})
			})},
		{ID: "ship.sync", Title: "Rebase a project's branch onto its base, fetched: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				return x.sync(se, p)
			})},
		{ID: "ship.sync-all", Title: "Rebase every project that's behind its base", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				var errs []error
				for _, s := range x.report(se.ID).ships {
					if !s.ReadOnly && s.Remote {
						errs = append(errs, x.sync(se, s.Project))
					}
				}
				return errors.Join(errs...)
			}},
		{ID: "ship.raise", Title: "Push a project and raise its PR, or update the open one: {project}, {title}, {body}, {draft}, {reviewers}",
			Available: inFront, Run: run(x.raise)},
		{ID: "ship.raise-all", Title: "Push and raise every project's PR, in merge order", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				return x.raiseAll(ctx, se)
			}},
		{ID: "ship.ready", Title: "Mark a project's draft PR ready for review: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				return x.step(se.ID, p, "Marking ready…", func(ctx context.Context, c worker.Client) error {
					return c.ShipReady(ctx, worker.ShipRef{Session: se.ID, Project: p})
				})
			})},
		{ID: "ship.pr.open", Title: "Open a project's Raise PR tab, or Edit PR once it's raised: {project}", Available: inFront,
			Run: run(func(ctx registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				title := "Raise PR · " + p
				if s, ok := x.ship(se.ID, p); ok && s.PR.Open() {
					title = "Edit PR · " + p
				}
				return x.h.Run(ctx, "view.open", registry.Args{"id": PRView, "instance": p, "place": "tab", "title": title})
			})},
		{ID: "ship.fix-ci", Title: "Write a request asking the agent to fix a project's failing CI: {project}", Available: inFront,
			Run: run(func(ctx registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				s, _ := x.ship(se.ID, p)
				t, ok := ciTrouble(s)
				if !ok {
					return fmt.Errorf("%s has no failing checks", p)
				}
				return x.h.Run(ctx, registry.FixCommand, t.Args())
			})},
		{ID: "ship.redraft", Title: "Ask the agent to draft a project's {kind} again: commit or pr", Available: inFront,
			Run: run(func(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
				kind := cmpOr(a.String("kind"), "commit")
				text := fmt.Sprintf("Draft the commit message for %s's uncommitted changes again, and save it with mcp__oriel__commit_draft (project %q).", p, p)
				if kind == "pr" {
					text = fmt.Sprintf("Draft %s's pull request again from its commits and the repo's template, and save it with mcp__oriel__pr_draft (project %q).", p, p)
				}
				c, err := x.client(se.ID)
				if err != nil {
					return err
				}
				if kind == "commit" {
					f := x.commitForm(se.ID, p)
					f.edited, f.save = false, autosave{}
				} else if s, ok := x.ship(se.ID, p); ok {
					f := x.prForm(se.ID, s)
					f.edited, f.save = false, autosave{}
				}
				x.saving.Wait()
				if err := c.ShipDraft(ctx, worker.DraftReq{Session: se.ID, Project: p, Kind: kind, Drop: true}); err != nil {
					return err
				}
				return c.Send(ctx, worker.SendReq{Session: se.ID, Text: text})
			})},
		{ID: "ship.draft.save", Title: "Save a project's draft as yours, which the agent leaves: {project}, {kind} pr (the Raise PR tab's, by default) or commit (the commit box's), {title}, {body}",
			Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, a registry.Args) error {
				kind := cmpOr(a.String("kind"), "pr")
				s, _ := x.ship(se.ID, p)
				req := worker.DraftReq{Session: se.ID, Project: p, Kind: kind}
				switch kind {
				case "pr":
					f := x.prForm(se.ID, s)
					req.Title, req.Body = cmpOr(a.String("title"), f.title), cmpOr(a.String("body"), f.body)
				case "commit":
					req.Body = cmpOr(a.String("body"), x.commitForm(se.ID, p).messageFor(s))
				default:
					return fmt.Errorf("a draft is a commit or a pr, not %q", kind)
				}
				return x.step(se.ID, p, "Saving…", func(ctx context.Context, c worker.Client) error { return c.ShipDraft(ctx, req) })
			})},
		{ID: "ship.github", Title: "Open a project's PR on GitHub: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				s, _ := x.ship(se.ID, p)
				if s.PR == nil || s.PR.URL == "" {
					return fmt.Errorf("%s has no PR yet", p)
				}
				return x.sh.desktop.OpenURL(s.PR.URL)
			})},
	} {
		x.add(x.h.Command(c))
	}
}

// commit fires ShipCommit, then commits on the worker. A veto is kept in the commit box.
func (x *shipExt) commit(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
	s, _ := x.ship(se.ID, p)
	f := x.commitForm(se.ID, p)
	ev := &ShipCommit{Session: string(se.ID), Project: p, Message: cmpOr(a.String("message"), f.messageFor(s)), Force: a.Bool("force")}
	if fs := paths(a["files"]); len(fs) > 0 {
		ev.Files = fs
	} else if len(f.skip) > 0 {
		ev.Files = f.files(s)
		if len(ev.Files) == 0 {
			return fmt.Errorf("no files of %s are ticked to commit", p)
		}
	}
	if strings.TrimSpace(ev.Message) == "" {
		return fmt.Errorf("%s's commit needs a message", p)
	}
	dir := sessionProjectDir(se, p)
	files := ev.Files
	if len(files) == 0 {
		for _, fl := range s.Files {
			files = append(files, fl.Path)
		}
	}
	var held []registry.GateCheck
	var trailers []string
	if dir != "" {
		for _, g := range registry.CheckCommit(x.h, registry.CommitCheck{Root: dir, Files: files, Message: ev.Message}) {
			if g.Pending {
				return x.commitLater(se, p, a)
			}
			if g.Blocks() || g.Asks() {
				held = append(held, g)
				if g.Blocks() && g.Trailer != "" {
					trailers = append(trailers, g.Trailer)
				}
			}
		}
	}
	f.held = held
	if len(held) > 0 && !ev.Force {
		var why []string
		for _, g := range held {
			why = append(why, g.Reason)
		}
		f.veto = strings.Join(why, "; ")
		return fmt.Errorf("%w: %s", errStopped, f.veto)
	}
	if v, _ := x.h.Fire(ctx, "ShipCommit", p, ev); v.Veto {
		f.veto = v.Reason
		return fmt.Errorf("%w: %s", errStopped, v.Reason)
	}
	f.veto = ""
	return x.step(se.ID, p, "Committing…", func(cx context.Context, c worker.Client) error {
		x.saving.Wait()
		hash, err := c.ShipCommit(cx, worker.ShipCommitReq{Session: se.ID, Project: p, Message: ev.Message, Files: ev.Files, Trailers: trailers})
		if err != nil {
			return err
		}
		x.h.Do(func() {
			*f = commitForm{skip: map[string]bool{}}
			if len(held) > 0 && x.sh.git != nil {
				x.sh.git.recordOverrides(se, p, true, hash, ev.Message, held)
			}
		})
		return nil
	})
}

// commitLater runs ship.commit again shortly, while the commit gates are still counting, until commitWait has gone.
func (x *shipExt) commitLater(se core.SessionState, p string, a registry.Args) error {
	started, _ := a["started"].(time.Time)
	if started.IsZero() {
		started = time.Now()
	}
	if time.Since(started) > commitWait {
		return fmt.Errorf("%s's commit gates didn't finish counting; try again", p)
	}
	more := maps.Clone(a)
	more["project"], more["started"] = p, started
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		time.Sleep(commitPoll)
		x.h.Do(func() { x.run("ship.commit", more) })
	}()
	return nil
}

// sessionProjectDir is a session's project's work tree, "" if it hasn't got it.
func sessionProjectDir(se core.SessionState, name string) string {
	for _, p := range se.Projects {
		if p.Name == name {
			return p.Dir
		}
	}
	return ""
}

// ledger is a project's review ledger, if one is kept.
func (x *shipExt) ledger(se core.SessionState, p string) (registry.Ledger, bool) {
	dir := sessionProjectDir(se, p)
	if dir == "" {
		return registry.Ledger{}, false
	}
	return registry.LedgerOf(x.h, dir)
}

// gated says a commit gate is hard or soft for a project's commits.
func (x *shipExt) gated(se core.SessionState, p string) bool {
	dir := sessionProjectDir(se, p)
	if dir == "" {
		return false
	}
	for _, g := range registry.CheckCommit(x.h, registry.CommitCheck{Root: dir}) {
		if g.Mode == registry.GateHard || g.Mode == registry.GateSoft {
			return true
		}
	}
	return false
}

// paths reads a list of strings from an arg.
func paths(v any) []string {
	switch v := v.(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, s := range v {
			if s, ok := s.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v != "" {
			return []string{v}
		}
	}
	return nil
}

func (x *shipExt) sync(se core.SessionState, p string) error {
	return x.step(se.ID, p, "Rebasing…", func(ctx context.Context, c worker.Client) error {
		return c.ShipSync(ctx, worker.ShipRef{Session: se.ID, Project: p})
	})
}

// raiseReq is the PR a project's raise sends: the args', else the tab's, else its defaults, through ShipRaise.
func (x *shipExt) raiseReq(ctx registry.Ctx, se core.SessionState, p string, a registry.Args, all bool) (worker.PRReq, error) {
	s, ok := x.ship(se.ID, p)
	if !ok {
		return worker.PRReq{}, fmt.Errorf("%s hasn't been read yet", p)
	}
	f := x.prForm(se.ID, s)
	ev := &ShipRaise{Session: string(se.ID), Project: p, Title: cmpOr(a.String("title"), f.title), Body: cmpOr(a.String("body"), f.body),
		Draft: a.Bool("draft"), Reviewers: f.reviewersOf(s), Edit: s.PR.Open(), All: all}
	if rs := paths(a["reviewers"]); len(rs) > 0 {
		ev.Reviewers = rs
	}
	if strings.TrimSpace(ev.Title) == "" {
		return worker.PRReq{}, fmt.Errorf("%s's PR needs a title", p)
	}
	if v, _ := x.h.Fire(ctx, "ShipRaise", p, ev); v.Veto {
		f.veto = v.Reason
		return worker.PRReq{}, fmt.Errorf("%w: %s", errStopped, v.Reason)
	}
	f.veto = ""
	return worker.PRReq{Session: se.ID, Project: p, Title: ev.Title, Body: ev.Body, Draft: ev.Draft, Reviewers: ev.Reviewers}, nil
}

// raise pushes what's unpushed, then raises the PR or edits it.
func (x *shipExt) raise(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
	req, err := x.raiseReq(ctx, se, p, a, false)
	if err != nil {
		return err
	}
	s, _ := x.ship(se.ID, p)
	label := "Raising…"
	if s.PR.Open() {
		label = "Updating…"
	}
	return x.step(se.ID, p, label, func(cx context.Context, c worker.Client) error {
		if len(s.Unpushed) > 0 || !s.Pushed {
			if err := c.ShipPush(cx, worker.ShipRef{Session: se.ID, Project: p}); err != nil {
				return err
			}
		}
		_, err := c.ShipPR(cx, req)
		return err
	})
}

// raiseAll raises each project with commits, one after another in merge order, so each PR links the ones before it.
func (x *shipExt) raiseAll(ctx registry.Ctx, se core.SessionState) error {
	var reqs []worker.PRReq
	for _, s := range x.report(se.ID).ships {
		if s.ReadOnly || s.GitHub == "" || s.Commits == 0 && s.PR == nil {
			continue
		}
		req, err := x.raiseReq(ctx, se, s.Project, registry.Args{}, true)
		if err != nil {
			return err
		}
		reqs = append(reqs, req)
	}
	if len(reqs) == 0 {
		return errors.New("no project has commits to raise")
	}
	if _, err := x.client(se.ID); err != nil {
		return err
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		for _, req := range reqs {
			done := make(chan struct{})
			err := x.step(se.ID, req.Project, "Raising…", func(cx context.Context, c worker.Client) error {
				defer close(done)
				if err := c.ShipPush(cx, worker.ShipRef{Session: se.ID, Project: req.Project}); err != nil {
					return err
				}
				_, err := c.ShipPR(cx, req)
				return err
			})
			if err != nil {
				x.h.Notify(core.Notice{ID: "ship." + req.Project, Severity: core.SevError, Key: true, Title: req.Project, Text: err.Error()})
				continue
			}
			<-done
		}
	}()
	return nil
}

// sessionShips is the session's projects as read, with a placeholder for one not read yet.
func (x *shipExt) sessionShips(se core.SessionState) ([]worker.Ship, shipReport) {
	r := x.report(se.ID)
	var out []worker.Ship
	for _, p := range se.Projects {
		i := slices.IndexFunc(r.ships, func(s worker.Ship) bool { return s.Project == p.Name })
		if i >= 0 {
			out = append(out, r.ships[i])
		} else {
			out = append(out, worker.Ship{Project: p.Name, Branch: p.Branch, Base: p.Base})
		}
	}
	return out, r
}

// ciTrouble is a project's failing checks as something to fix or explain, with the log's failing line.
func ciTrouble(s worker.Ship) (registry.Trouble, bool) {
	var failing []string
	for _, c := range s.Checks {
		if c.State() == gh.CheckFail {
			failing = append(failing, c.Name)
		}
	}
	if len(failing) == 0 {
		return registry.Trouble{}, false
	}
	what := s.Project
	if s.PR != nil {
		what += fmt.Sprintf("'s PR #%d", s.PR.Number)
	}
	subject := fmt.Sprintf("CI is failing on %s: %s", what, strings.Join(failing, ", "))
	return registry.Trouble{Kind: "ci-failure", Subject: subject, Context: strings.TrimSpace(s.FailLine),
		Fix: subject + ". Find out why and fix it."}, true
}

// conflictTrouble is the conflicts a rebase onto the base would stop on, as something to fix or explain.
func conflictTrouble(s worker.Ship) (registry.Trouble, bool) {
	if len(s.Conflicts) == 0 {
		return registry.Trouble{}, false
	}
	base := cmpOr(s.Base, "the base")
	subject := fmt.Sprintf("%s conflicts with %s in %s", s.Project, base, strings.Join(s.Conflicts, ", "))
	ctx := fmt.Sprintf("%s is %d commits behind %s; rebasing onto it would conflict in:\n%s", s.Project, s.Behind, base,
		strings.Join(s.Conflicts, "\n"))
	return registry.Trouble{Kind: "conflict", Subject: subject, Context: ctx,
		Fix: fmt.Sprintf("Rebase %s onto %s and resolve the conflicts in %s, keeping both sides' intent.", s.Project, base,
			strings.Join(s.Conflicts, ", "))}, true
}
