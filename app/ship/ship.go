package ship

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Ship page and its panels: each project's way from uncommitted work to a merged PR, read from
// the session host (host.ShipRead) and its code host (codehost.go), and the steps along it. Everything is a command, so keys, Lua and the buttons reach it
// the same way; the gates are ShipGate contributions, and ShipCommit and ShipRaise hooks can stop a step.

// shipPoll is the most often the session in front's git is read again as it changes.
const shipPoll = 10 * time.Second

// ShipCommit is the ShipCommit hook's event: a commit about to be made from the Ship page. A veto stops it, its
// reason shown in the commit box, and a rewrite changes it (a trailer, say).
type ShipCommit struct {
	Session string   `json:"session"`
	Project string   `json:"project"`
	Message string   `json:"message"`
	Files   []string `json:"files,omitempty"`
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

type Ship struct {
	kit.Disposer
	ConfigDir string // the user's config directory, for their prompts
	h         plugin.Host
	Clock     func() time.Time // the time Ship reads ages by; time.Now if nil
	ctx       context.Context
	cancel    context.CancelFunc
	busy      kit.Busy                          // reads and steps
	saving    sync.WaitGroup                    // drafts the user typed, on their way to the session host
	subs      kit.Subs                          // what it reads, from the sessions' managers (shipwatch.go)
	budget    kit.Budget                        // background PR polls' requests
	showQuiet map[core.SessionID]bool           // the Ship page shows the projects with nothing to ship; on the loop
	watching  map[core.SessionID]bool           // the sessions whose git follow watches; x.mu
	projects  map[core.SessionID][]core.Project // each session's projects as follow last saw them; x.mu

	mu       sync.Mutex
	reports  map[core.SessionID]*shipReport
	seenPRs  map[string]bool // the PRs PrSeen has fired for, by URL
	merged   map[string]bool // the PRs PrMerged has fired for, by URL
	opened   map[string]bool // the PRs read open by this process, by URL
	movedOn  map[string]bool // the merged PRs whose next PR has been started or asked about, by URL
	cache    prCache
	mirrored map[core.SessionID]time.Time // when each session's reviews were last mirrored from a code host
	reviewed map[string]map[string]string // each PR's reviewers' latest verdicts, by URL, from its mirrored threads

	// On the loop: what's typed, by session/project.
	commits  map[string]*commitForm
	prs      map[string]*prForm
	renaming map[string]*renameForm // the Git panel's branch names being typed
	merging  *mergeAsk
}

// shipReport is a session's projects as last read, and the steps running on them.
type shipReport struct {
	ships   []shipRead
	read    time.Time
	loading bool
	again   bool      // asked for again while loading: read once more when it's done
	since   time.Time // when the read running started, on the window's clock; set by the first frame that sees it
	err     string
	doing   map[string]string // project → the step running: "Pushing"
	failed  map[string]string // project → why its last step failed
	started int               // reads started; a read older than the one last kept is dropped
	kept    int
	fixed   bool           // a Fixture's: reads don't replace it
	seq     int            // reads started, a session's whole or a project's, in order
	from    map[string]int // project → the seq of the read its entry is from
}

func (x *Ship) Manifest() plugin.Manifest { return plugin.Manifest{ID: "ship"} }

func (x *Ship) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	if x.Clock == nil {
		x.Clock = time.Now
	}
	x.reports = map[core.SessionID]*shipReport{}
	x.commits, x.prs, x.renaming = map[string]*commitForm{}, map[string]*prForm{}, map[string]*renameForm{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "ShipCommit", Fired: true, Payload: ShipCommit{},
		Doc: "A commit about to be made from the Ship page; a veto stops it (its reason shows in the commit box), a rewrite changes it. { session, project, message, files }"}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "PrSeen", Fired: true, Payload: kit.PrSeen{},
		Doc: "A session's pull request, the first time Oriel reads it, whoever raised it; the subject is its URL. { session, project, ticket, number, url, title, state, draft, merged }"}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "PrMerged", Fired: true, Payload: kit.PrMerged{},
		Doc: "A session's pull request read as merged, the first time Oriel sees it so, whoever merged it; the subject is its URL. open is how many of the session's other PRs are still open. { session, project, ticket, number, url, title, open }"}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "ShipRaise", Fired: true, Payload: ShipRaise{},
		Doc: "A pull request about to be raised or edited from the Ship page; a veto stops it (a quiz to take first), a rewrite changes it. { session, project, title, body, draft, reviewers, edit, all }"}))
	x.Add(h.Contribute(uireg.ProjectState{Name: "ship", Order: 10, State: x.projectState}))
	x.Add(h.Contribute(registry.Service{Name: registry.ShipsService, Impl: ships{x}, Source: "builtin:ship"}))
	x.Add(h.Contribute(registry.CardChips{Name: "pr", Chips: x.prChips, Source: "builtin:ship"}))
	x.Add(h.Contribute(registry.SessionDone{Name: "ship.merged", Done: x.shipped, Source: "builtin:ship"}))
	x.gates()
	x.activateMerge(h)
	x.shadowEntries()
	x.commands()
	x.views()
	x.budget.PerHour = prBudget
	x.Add(kit.OnDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.SessionFocused, core.TabOpened, core.TabClosed, core.SessionOpened, core.SessionArchived, core.SessionClosed:
			for _, id := range x.follow() {
				x.refresh(id, false) // its git wasn't watched: read it whole once, then its watches keep it
			}
		case core.ProjectsChanged:
			if moved, ok := x.branchesMoved(ev.ID, ev.Projects); ok {
				for _, p := range moved {
					x.readSoon(ev.ID, p)
				}
			} else {
				x.refresh(ev.ID, false)
			}
			x.follow()
		case core.DraftSaved:
			x.readSoon(ev.Session, ev.Project)
		}
	})))
	x.follow()
	return nil
}

func (x *Ship) Deactivate(ctx context.Context) error {
	x.cancel()
	x.subs.Close()
	x.busy.Wait()
	return x.Disposer.Deactivate(ctx)
}

func (x *Ship) focused() core.SessionID { return x.h.State().Focus }

// client is the session host.
func (x *Ship) client(id core.SessionID) (host.Access, error) {
	return kit.SessionHost(x.h)
}

// session is the session in front, or {id}.
func (x *Ship) session(a registry.Args) (core.SessionState, bool) {
	se, err := kit.SessionFrom(x.h, a)
	return se, err == nil
}

// report is a copy of a session's report, with an empty one for a session not read yet.
func (x *Ship) report(id core.SessionID) shipReport {
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

// loadState is a project's reading as the loading widgets take it: pending until its first read, refreshing while
// a later one runs, failed (or ready with the error) when the read failed. Call it on the loop.
func (x *Ship) loadState(c *ui.Ctx, id core.SessionID, project string) widget.LoadState {
	x.mu.Lock()
	defer x.mu.Unlock()
	r := x.reports[id]
	if r == nil {
		return widget.LoadState{}
	}
	r.seen(c.Now())
	var err error
	if r.err != "" {
		err = errors.New(r.err)
	}
	s := widget.LoadState{Since: r.since, Err: err, Progress: -1}
	switch {
	case !slices.ContainsFunc(r.ships, func(s shipRead) bool { return s.Project == project }):
		s.Phase = widget.Pending
		if !r.loading && err != nil {
			s.Phase = widget.Failed
		}
	case r.loading:
		s.Phase, s.Stale = widget.Pending, true
	default:
		s.Phase = widget.Ready
	}
	return s
}

// seen notes the read running was seen at now: since is when a frame first saw it.
func (r *shipReport) seen(now time.Time) {
	switch {
	case !r.loading:
		r.since = time.Time{}
	case r.since.IsZero():
		r.since = now
	}
}

// ships is Ship's Ships service.
type ships struct{ x *Ship }

func (s ships) For(se core.SessionState) []registry.ProjectShip {
	x := s.x
	reads, _ := x.sessionShips(se)
	x.mu.Lock()
	defer x.mu.Unlock()
	r := x.reports[se.ID]
	if r != nil {
		r.seen(kit.WindowNow(x.h))
	}
	out := projectShips(reads)
	for i := range out {
		ps := &out[i]
		switch {
		case r == nil:
		case !slices.ContainsFunc(r.ships, func(s shipRead) bool { return s.Project == ps.Project }):
			ps.Load.Since = r.since
			if !r.loading && r.err != "" {
				ps.Load.Err = errors.New(r.err)
			}
		default:
			ps.Load.Done = true
		}
	}
	return out
}

// projectShips are reads as other plugins see them.
func projectShips(reads []shipRead) []registry.ProjectShip {
	out := make([]registry.ProjectShip, 0, len(reads))
	for _, p := range reads {
		var files []string
		for _, f := range p.Files {
			files = append(files, f.Path)
		}
		out = append(out, registry.ProjectShip{Project: p.Project, ReadOnly: p.ReadOnly, Files: files, Unpushed: len(p.Unpushed), Commits: p.Commits,
			Pushed: p.Pushed, PR: p.PR, Code: p.Code})
	}
	return out
}

func (s ships) Refresh(id core.SessionID, fresh bool) { s.x.refresh(id, fresh) }

func (s ships) ResolveThread(se core.SessionState, t core.Thread, resolved bool) error {
	return s.x.resolveHost(se, t, resolved)
}

func (s ships) ForwardThread(se core.SessionState, thread core.ThreadID, comment string) error {
	return s.x.forwardHost(se, thread, comment)
}

// stepOpts are a step's button options while the project's steps run: busy, labelled as the step, while it's this
// one's step; off while another runs.
func (x *Ship) stepOpts(id core.SessionID, project, step string, o widget.ButtonOpts) widget.ButtonOpts {
	switch d := x.doing(id, project); d {
	case "":
	case step:
		o.Busy, o.BusyLabel = true, step
	default:
		o.Disabled = true
	}
	return o
}

// ship is a project's last reading.
func (x *Ship) ship(id core.SessionID, project string) (shipRead, bool) {
	for _, s := range x.report(id).ships {
		if s.Project == project {
			return s, true
		}
	}
	return shipRead{}, false
}

// refresh reads a session's projects, off the loop; fresh fetches and asks their code hosts again.
func (x *Ship) refresh(id core.SessionID, fresh bool) {
	x.mu.Lock()
	if r := x.reports[id]; r != nil && r.loading && !fresh {
		r.again = true // what changed may be after what the read running has read
		x.mu.Unlock()
		return
	}
	r, n, seq := x.reading(id)
	x.mu.Unlock()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		x.readAs(id, r, n, seq, fresh)
	}()
}

// read reads a session's projects, on this goroutine.
func (x *Ship) read(id core.SessionID, fresh bool) {
	x.mu.Lock()
	r, n, seq := x.reading(id)
	x.mu.Unlock()
	x.readAs(id, r, n, seq, fresh)
}

// reading marks a session's read started, and numbers it among the whole reads and among every read; x.mu is held.
func (x *Ship) reading(id core.SessionID) (*shipReport, int, int) {
	r := x.reports[id]
	if r == nil {
		r = &shipReport{doing: map[string]string{}, failed: map[string]string{}}
		x.reports[id] = r
	}
	if !r.fixed {
		r.loading = true
		r.started++
		r.seq++
	}
	return r, r.started, r.seq
}

// readAs is read n into r: what it read is kept unless a read started after it was kept first, and a project read
// on its own since seq keeps that read's entry.
func (x *Ship) readAs(id core.SessionID, r *shipReport, n, seq int, fresh bool) {
	var ships []shipRead
	c, err := x.client(id)
	if err == nil {
		ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
		var read []host.Ship
		if read, err = c.ShipRead(ctx, host.ShipReq{Session: id, Fresh: fresh}); err == nil {
			ships = x.withPRs(ctx, id, read, fresh)
			x.mirrorReviews(ctx, id, ships, fresh)
		}
		cancel()
	}
	x.mu.Lock()
	if r.fixed {
		x.mu.Unlock()
		return
	}
	again := false
	if n == r.started {
		r.loading, again, r.again = false, r.again, false
	}
	if again {
		defer x.refresh(id, false)
	}
	if n < r.kept {
		x.mu.Unlock()
		return
	}
	r.kept = n
	if err != nil {
		r.err = err.Error()
	} else {
		ships = r.keepNewer(ships, seq)
		r.ships, r.err, r.read = ships, "", time.Now()
	}
	seen, merged, asks := x.newPRs(id, ships)
	x.mu.Unlock()
	x.fire(id, seen, merged, asks)
	x.h.Redraw()
}

// fire fires PrSeen and PrMerged for what a read found new, asks about the next PR, and follows the PRs open now.
func (x *Ship) fire(id core.SessionID, seen []kit.PrSeen, merged []kit.PrMerged, asks []shipRead) {
	if len(seen)+len(merged)+len(asks) > 0 {
		x.h.Do(func() {
			for _, ev := range seen {
				x.h.Fire(context.Background(), "PrSeen", ev.URL, &ev)
			}
			for _, ev := range merged {
				x.h.Fire(context.Background(), "PrMerged", ev.URL, &ev)
			}
			for _, s := range asks {
				x.askNextPR(id, s, s.PR)
			}
		})
	}
	if x.ctx.Err() == nil {
		x.follow()
	}
}

// newPRs are the PRs in ships not seen before, as PrSeen events, and those not seen merged before, as PrMerged
// events; x.mu is held.
func (x *Ship) newPRs(id core.SessionID, ships []shipRead) (seen []kit.PrSeen, merged []kit.PrMerged, asks []shipRead) {
	if x.seenPRs == nil {
		x.seenPRs, x.merged = map[string]bool{}, map[string]bool{}
	}
	ticket, nextPR := "", false
	if se, ok := x.h.Session(id); ok {
		ticket, nextPR = se.Snapshot().Ticket, registry.Can(x.h, se.Kind(), core.NextPR)
	}
	open := 0 // a project its code host didn't answer for counts as open
	for _, s := range ships {
		if s.PR.Open() || s.PR == nil && s.HostError != "" {
			open++
		}
	}
	for _, s := range ships {
		if s.PR == nil || s.PR.URL == "" {
			continue
		}
		if !x.seenPRs[s.PR.URL] {
			x.seenPRs[s.PR.URL] = true
			seen = append(seen, kit.PrSeen{Session: string(id), Project: s.Project, Ticket: ticket, Number: s.PR.Number, URL: s.PR.URL,
				Title: s.PR.Title, State: s.PR.State, Draft: s.PR.Draft, Merged: s.PR.Merged})
		}
		if s.PR.Open() {
			if x.opened == nil {
				x.opened = map[string]bool{}
			}
			x.opened[s.PR.URL] = true
		}
		if s.PR.Merged && x.opened[s.PR.URL] && !x.movedOn[s.PR.URL] && nextPR {
			if x.movedOn == nil {
				x.movedOn = map[string]bool{}
			}
			x.movedOn[s.PR.URL] = true
			asks = append(asks, s)
		}
		if s.PR.Merged && !x.merged[s.PR.URL] {
			x.merged[s.PR.URL] = true
			merged = append(merged, kit.PrMerged{Session: string(id), Project: s.Project, Ticket: ticket, Number: s.PR.Number, URL: s.PR.URL,
				Title: s.PR.Title, Open: open})
		}
	}
	return seen, merged, asks
}

// wait is until every read and step started has finished.
func (x *Ship) wait() { x.busy.Wait() }

// Wait waits for the reads and steps running.
func (x *Ship) Wait() { x.wait() }

// step runs one step on a project off the loop, showing doing while it runs and until the project's been read after
// it, so its panel never shows the state from before; then done, if it succeeded, on the loop. A failure is kept for
// the project and sent as a notice. The session is read again after, fetched.
func (x *Ship) step(id core.SessionID, project, doing string, fn func(ctx context.Context, c host.Access) error, done ...func()) error {
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
		x.readOne(id, project, true) // what the step changed is this project's: the others' readings stand
		x.h.Do(func() {
			if err == nil {
				for _, d := range done {
					d()
				}
			}
			x.mu.Lock()
			delete(r.doing, project)
			if err != nil {
				r.failed[project] = err.Error()
			}
			x.mu.Unlock()
			if err != nil {
				x.h.Notify(core.Notice{ID: "ship." + project, Severity: core.SevError, Key: true, Title: project, Text: err.Error()})
			}
		})
	}()
	return nil
}

// doing is the step running on a project, "" for none.
func (x *Ship) doing(id core.SessionID, project string) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r := x.reports[id]; r != nil {
		return r.doing[project]
	}
	return ""
}

func (x *Ship) failed(id core.SessionID, project string) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r := x.reports[id]; r != nil {
		return r.failed[project]
	}
	return ""
}

// projectState is a header's PR and CI ("rate-limits · #790 ● CI failing").
func (x *Ship) projectState(project, _ string) []uireg.StatePart {
	id := x.focused()
	if id == "" {
		return nil
	}
	s, ok := x.ship(id, project)
	if !ok || s.ReadOnly || s.Code == nil {
		return nil
	}
	if s.PR == nil {
		if s.Commits > 0 || len(s.Files) > 0 {
			return []uireg.StatePart{{Text: "no PR"}}
		}
		return nil
	}
	out := []uireg.StatePart{{Text: fmt.Sprintf("#%d", s.PR.Number)}}
	if s.PR.Merged {
		return append(out, uireg.StatePart{Text: "merged", Style: "violet", Dot: true})
	}
	switch ci := ciOf(s.Checks); ci {
	case registry.CheckFailed:
		out = append(out, uireg.StatePart{Text: "CI failing", Style: "error", Dot: true})
	case registry.CheckRunning:
		out = append(out, uireg.StatePart{Text: "CI running", Style: "cyan", Dot: true})
	case registry.CheckOK:
		out = append(out, uireg.StatePart{Text: "CI passing", Style: "ok", Dot: true})
	}
	return out
}

// ciOf is the checks in one word: fail if any failed, else pending if any are running, else pass; "" for none.
func ciOf(cs []registry.Check) string {
	if len(cs) == 0 {
		return ""
	}
	out := registry.CheckOK
	for _, c := range cs {
		switch {
		case c.State == registry.CheckFailed:
			return registry.CheckFailed
		case c.Pending():
			out = registry.CheckRunning
		}
	}
	return out
}

// gates are the "before this ships" gates Oriel can tell now; the work kind, the verify quiz and AI review register
// theirs with their tickets.
func (x *Ship) gates() {
	sess := func(id string) (core.SessionState, bool) {
		se, ok := x.h.Session(core.SessionID(id))
		if !ok {
			return core.SessionState{}, false
		}
		return se.Snapshot(), true
	}
	x.Add(x.h.Contribute(registry.ShipGate{Name: "threads", Title: "All threads resolved", Order: 20, Check: func(id string) registry.Gate {
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
	x.Add(x.h.Contribute(registry.ShipGate{Name: "tests", Title: "Tests ran this session", Order: 40, Check: func(id string) registry.Gate {
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
			note += " " + kit.AgoText(x.Clock(), last.Ended)
		}
		if !last.OK {
			return registry.Gate{State: registry.GateFail, Note: note}
		}
		return registry.Gate{State: registry.GatePass, Note: note}
	}}))
}

// shadowEntries gives the sidebar's Pull request entry its note ("#790", "not raised"), shadowing the rail's.
func (x *Ship) shadowEntries() {
	for _, e := range uireg.ProjectEntries(x.h) {
		if e.Name != "pull-request" {
			continue
		}
		e.Note = func(project string) string {
			w := kit.ProjectsHere(x.h)
			if w.Session == nil {
				return ""
			}
			if project == "" {
				project = w.Open
			}
			s, ok := x.ship(w.Session.ID, project)
			switch {
			case !ok:
				return ""
			case s.PR.Open() && !s.PR.Draft:
				return fmt.Sprintf("%s%d", s.words().Sigil, s.PR.Number)
			case s.PR != nil:
				return fmt.Sprintf("%s%d %s", s.words().Sigil, s.PR.Number, prStateWord(s.PR))
			case !x.prForm(w.Session.ID, s).edited && s.PRDraft.Empty():
				return "not raised"
			}
			return "drafting"
		}
		e.Source = "ship"
		x.Add(x.h.Contribute(e))
	}
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

// sessionShips is the session's projects as read, with a placeholder for one not read yet.
func (x *Ship) sessionShips(se core.SessionState) ([]shipRead, shipReport) {
	r := x.report(se.ID)
	var out []shipRead
	for _, p := range se.Projects {
		i := slices.IndexFunc(r.ships, func(s shipRead) bool { return s.Project == p.Name })
		if i >= 0 {
			out = append(out, r.ships[i])
		} else {
			out = append(out, shipRead{Ship: host.Ship{Project: p.Name, Branch: p.Branch, Base: p.Base}})
		}
	}
	return out, r
}

// ciTrouble is a project's failing checks as something to fix or explain, with the log's failing line.
func ciTrouble(s shipRead) (registry.Trouble, bool) {
	var failing []string
	for _, c := range s.Checks {
		if c.State == registry.CheckFailed {
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
func conflictTrouble(s shipRead) (registry.Trouble, bool) {
	if len(s.Conflicts) == 0 {
		return registry.Trouble{}, false
	}
	base := cmp.Or(s.Base, "the base")
	subject := fmt.Sprintf("%s conflicts with %s in %s", s.Project, base, strings.Join(s.Conflicts, ", "))
	ctx := fmt.Sprintf("%s is %d commits behind %s; rebasing onto it would conflict in:\n%s", s.Project, s.Behind, base,
		strings.Join(s.Conflicts, "\n"))
	return registry.Trouble{Kind: "conflict", Subject: subject, Context: ctx,
		Fix: fmt.Sprintf("Rebase %s onto %s and resolve the conflicts in %s, keeping both sides' intent.", s.Project, base,
			strings.Join(s.Conflicts, ", "))}, true
}

// prChips are the pull requests a session that ships has raised, as the window's Ships last read its projects: its
// card names them.
func (x *Ship) prChips(se core.SessionState) []registry.Chip {
	s, ok := registry.ShipsOf(x.h)
	if !ok || !registry.Can(x.h, se.Kind, core.Ships) {
		return nil
	}
	var out []registry.Chip
	for _, p := range s.For(se) {
		if p.PR != nil {
			out = append(out, registry.Chip{Text: fmt.Sprintf("#%d", p.PR.Number), Help: kit.MissionHelp + "#cards", Link: p.PR.URL})
		}
	}
	return out
}

// shipped says a session that ships is done once every PR is merged and nothing is uncommitted or unpushed, as last
// read.
func (x *Ship) shipped(se core.SessionState) string {
	s, ok := registry.ShipsOf(x.h)
	if !ok || !registry.Can(x.h, se.Kind, core.Ships) {
		return ""
	}
	ships := s.For(se)
	for _, p := range ships {
		if kit.LoadState(p.Load).Loading() || p.Unpushed > 0 {
			return ""
		}
	}
	if !kit.AllMerged(se, ships) || kit.Uncommitted(ships) > 0 {
		return ""
	}
	return "Every pull request is merged, and nothing is left to commit or push."
}

// Read is one of a session's projects as Ship read it: its git state from the session host, and its code host's PR
// and checks.
type Read = shipRead

// Fixture makes what Ship last read of session id's projects fn of it, loading or not, with no step running or
// failed, and keeps it over later reads: a showcase or a test drawn with no code host.
func (x *Ship) Fixture(id core.SessionID, loading bool, fn func([]Read) []Read) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var ships []Read
	if r := x.reports[id]; r != nil {
		ships = r.ships
	}
	x.reports[id] = &shipReport{ships: fn(ships), loading: loading, doing: map[string]string{}, failed: map[string]string{}, fixed: true}
}
