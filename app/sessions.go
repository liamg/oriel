package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/settingsfile"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/worker"
)

// sessions is the app's side of the workers (architecture.md): it mirrors each worker's sessions into the store,
// follows the session in front with the window, and answers what workers ask of the app: the tools plugins offer
// agents, and the Permission hooks (agents.md, layer 2). Commands drive it, so Lua and keys reach it the same way.
type sessions struct {
	disposer
	sh     *shell
	h      plugin.Host
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	workers map[core.WorkerID]worker.Client
	repos   []core.Repo // the session in front's worker's registered repos, for adding one

	sandboxKick chan struct{} // the sandbox settings are to go to the workers
	promptsKick chan struct{} // the user's prompts are to go to the workers
}

// AgentTurnEnd is the AgentTurnEnd hook's event.
type AgentTurnEnd struct {
	Session string `json:"session"`
}

// SessionStart is the SessionStart hook's event: a session just started, from the launcher, a command or a script.
type SessionStart struct {
	Session string `json:"session"`
	Kind    string `json:"kind"`
	Ticket  string `json:"ticket,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	Worker  string `json:"worker,omitempty"`
}

// CommentSend is the CommentSend hook's event: a comment about to go to an agent. A rewrite changes it, a veto
// stops it.
type CommentSend struct {
	Session string `json:"session"`
	Thread  string `json:"thread,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Body    string `json:"body"`
	Urgent  bool   `json:"urgent,omitempty"`
	Btw     bool   `json:"btw,omitempty"`
}

func (x *sessions) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sessions"} }

func (x *sessions) Activate(_ context.Context, h plugin.Host) error {
	x.sh.sessions, x.h = x, h
	ctx, cancel := context.WithCancel(context.Background())
	x.ctx, x.cancel = ctx, cancel
	x.workers = map[core.WorkerID]worker.Client{}
	x.sandboxKick = make(chan struct{}, 1)
	x.sandboxSettings(ctx)
	x.promptsKick = make(chan struct{}, 1)
	x.promptSettings(ctx)
	for _, c := range x.sh.cfg.Workers {
		x.serve(ctx, h, c, "")
	}
	x.add(h.DeclareEvent(registry.EventInfo{Name: "Permission", Fired: true, Payload: registry.PermissionEvent{},
		Doc: "An agent, the egress proxy or the gateway wants something; a Decide hook answers allow or deny before the user is asked."}))
	if len(x.sh.cfg.Workers) > 0 {
		x.add(h.Contribute(registry.Explainer{Name: "agent", Order: 10, Explain: x.explain, Label: x.explainer, Byline: x.explainerByline}))
		x.add(h.Contribute(registry.Forker{Name: "agent", Order: 10, Fork: x.fork, Label: x.forker}))
	}
	x.add(h.DeclareEvent(registry.EventInfo{Name: "AgentTurnEnd", Fired: true, Payload: AgentTurnEnd{},
		Doc: "A session's agent finished a turn."}))
	x.add(registry.On(h, registry.Hook{Kind: registry.Rewrite, Label: "files outside the session open read-only"},
		func(_ registry.Ctx, ev *BufRead) (registry.Verdict, error) {
			if why := x.readOnly(ev.File); why != "" {
				ev.ReadOnly = why
				return registry.Verdict{Rewrote: true}, nil
			}
			return registry.Verdict{}, nil
		}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "SessionStart", Fired: true, Payload: SessionStart{},
		Doc: "A session just started; the subject is its id. { session, kind, ticket, prompt, worker }"}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "CommentSend", Fired: true, Payload: CommentSend{},
		Doc: "A comment about to go to an agent; a rewrite changes it, a veto stops it. { session, thread, file, line, body, urgent, btw }"}))
	working := map[core.SessionID]bool{}
	signedOut := map[core.WorkerID][]core.Login{}
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.WorkerChanged:
			was := signedOut[ev.Worker.ID]
			signedOut[ev.Worker.ID] = ev.Worker.SignedOut
			if !slices.Equal(was, ev.Worker.SignedOut) {
				h.Do(func() { x.loginNotices(ev.Worker, was) })
			}
		case core.SessionFocused:
			if ev.ID != "" {
				h.Do(func() { x.follow(ev.ID) })
			}
		case core.ProjectsChanged:
			if ev.ID == x.focused() {
				h.Do(func() { x.follow(ev.ID) }) // its worktrees moved: Start work
			}
		case core.AgentChanged:
			was := working[ev.ID]
			working[ev.ID] = ev.Agent.Working
			if was && !ev.Agent.Working {
				h.Do(func() {
					h.Fire(context.Background(), "AgentTurnEnd", string(ev.ID), AgentTurnEnd{Session: string(ev.ID)})
				})
			}
		}
	})))
	has := func(registry.Args) bool { return len(x.clients()) > 0 }
	focused := func(registry.Args) bool { return has(nil) && x.focused() != "" }
	named := func(a registry.Args) bool { return has(nil) && (a.String("id") != "" || x.focused() != "") } // {id}, or the one in front
	planning := func(registry.Args) bool {
		se, ok := x.sh.cfg.Store.Session(x.focused())
		return ok && has(nil) && se.Snapshot().Planning()
	}
	x.add(h.Picker(x.addSource(h)))
	x.add(h.Keymap(registry.Keymap{Mode: WorkerDrainKeyMode, Keys: "<D-CR>", Command: "worker.hand-over-now", Desc: "Don't wait for the turns"}))
	x.add(h.View(OverlayPrefix+"worker.drain", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "workerdrain", Fn: x.drainView}
	})))
	for _, c := range []registry.Command{
		{ID: "session.start", Title: "Start a session: {kind}, {prompt}, {repos}, {provider}, {model}, {effort}, {worker}, {sandbox}…; {clone} the repos the worker lacks first, a URL by repo; {background} leaves it behind the one in front; {then} is a list of {command, args} run once the window is on it",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error { _, err := x.start(ctx, a); return err }},
		{ID: "repos.clone", Title: "Clone {url} onto {worker} (this machine's if none) as {name}, and register it", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				c, err := x.worker(core.WorkerID(a.String("worker")))
				if err != nil {
					return err
				}
				_, err = c.CloneRepo(ctx, worker.CloneReq{URL: a.String("url"), Name: a.String("name")})
				return err
			}},
		{ID: "session.open", Title: "Bring a session to the front: {id}, resuming it if it's stopped or archived; {then} is a list of {command, args} run once the window is on it",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error {
				then, err := callsOf(a["then"])
				if err != nil {
					return err
				}
				return x.open(ctx, core.SessionID(a.String("id")), then)
			}},
		{ID: "worker.hand-over-now", Title: "Hand this machine's worker over now, for a restart or a quit, not waiting for its agents' turns",
			Available: func(registry.Args) bool { return x.sh.draining.Load() != nil },
			Run: func(registry.Ctx, registry.Args) error {
				if r := x.sh.draining.Load(); r != nil {
					r.goNow()
				}
				return nil
			}},
		{ID: "session.resume", Title: "Resume a session: pick one", Available: has,
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "picker.open", registry.Args{"source": "sessions"})
			}},
		{ID: "session.stop", Title: "Stop the agent of {id}, or the session in front", Available: named,
			Run: x.onSession(func(ctx context.Context, c worker.Client, id core.SessionID) error { return c.Stop(ctx, id) })},
		{ID: "session.archive", Title: "Archive {id}, or the session in front: its worktrees go, its threads and transcript stay", Available: named,
			Run: x.onSession(func(ctx context.Context, c worker.Client, id core.SessionID) error { return c.Archive(ctx, id) })},
		{ID: "session.next-pr", Title: "Start the next PR in this session, on a fresh branch from the updated base", Available: focused,
			Run: x.onSession(func(ctx context.Context, c worker.Client, id core.SessionID) error { return c.NextPR(ctx, id) })},
		{ID: "session.start-work", Title: "Start work on this plan: worktrees, then the agent implements it; {clear}, {fresh}, {branches}, {sandbox}, {network}, {model}, {effort}",
			Available: planning,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.onSession(func(ctx context.Context, c worker.Client, id core.SessionID) error {
					return c.StartWork(ctx, startWorkReq(id, a))
				})(ctx, a)
			}},
		{ID: "session.interrupt", Title: "Interrupt the agent's turn: {id}'s, or the session in front's", Available: named,
			Run: x.onSession(func(ctx context.Context, c worker.Client, id core.SessionID) error { return c.Interrupt(ctx, id) })},
		{ID: "session.rename", Title: "Rename {id}, or the session in front: {name}", Available: named,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.onSession(func(ctx context.Context, c worker.Client, id core.SessionID) error {
					return c.Rename(ctx, id, a.String("name"))
				})(ctx, a)
			}},
		{ID: "session.add-repo", Title: "Add a repo to the session in front: {repo}, by name or path", Available: focused,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.onSession(func(ctx context.Context, c worker.Client, id core.SessionID) error {
					return c.AddRepo(ctx, id, a.String("repo"))
				})(ctx, a)
			}},
		{ID: "project.add", Title: "Add a project to the session in front: pick a registered repo, or type a checkout's path",
			Available: func(registry.Args) bool {
				se, ok := x.sh.cfg.Store.Session(x.focused())
				return focused(nil) && ok && se.Kind() != core.KindDirect
			},
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				x.loadRepos()
				return h.Run(ctx, "picker.open", registry.Args{"source": "projects.add"})
			}},
	} {
		x.add(h.Command(c))
	}
	return nil
}

func (x *sessions) Deactivate(ctx context.Context) error {
	if x.cancel != nil {
		x.cancel()
	}
	if x.sh.sessions == x {
		x.sh.sessions = nil
	}
	return x.disposer.Deactivate(ctx)
}

// serve mirrors one worker into the store and answers its calls, until ctx ends or it goes away. A worker speaks
// only for itself (core.State.FromWorker); want, when set, is the ID it must have: a remote worker's, from its key.
// A worker that connects again replaces its old connection.
func (x *sessions) serve(ctx context.Context, h plugin.Host, c worker.Client, want core.WorkerID) {
	c.Conn.Handle(worker.MethodAppTools, func(_ context.Context, raw json.RawMessage) (any, error) {
		var r worker.ToolsReq
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		return x.agentTools(h, r), nil
	})
	c.Conn.Handle(worker.MethodAppTool, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var call worker.ToolCall
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		return x.callTool(ctx, h, c, call)
	})
	c.Conn.Handle(worker.MethodAppDecide, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var d worker.Decide
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		return x.decide(ctx, h, d), nil
	})
	go func() {
		info, err := c.Info(ctx)
		if err != nil {
			h.Log().Warn("worker", "err", err)
			return
		}
		if want != "" && info.ID != want {
			h.Log().Warn("a worker claimed another's name", "key", want, "claimed", info.ID)
			c.Conn.Close()
			return
		}
		x.mu.Lock()
		old, had := x.workers[info.ID]
		x.workers[info.ID] = c
		x.mu.Unlock()
		if had && old.Conn != c.Conn {
			old.Conn.Close()
		}
		x.sendSandbox()
		x.sh.workerJoined(c, info)
		x.sendPrompts()
		h.Redraw()
		if x.sh.cfg.LocalWorker != nil && info.ID == core.LocalWorker && info.Build != worker.Build() {
			x.sh.restartWorker(c, info.ID)
		}
		store := x.sh.cfg.Store
		err = c.Events(ctx, func(ev core.Event) error {
			if !store.State().FromWorker(info.ID, ev) {
				return nil
			}
			return store.Apply(ev)
		})
		if r := x.sh.draining.Load(); r != nil && r.old.Conn == c.Conn {
			r.lost()
			return
		}
		if ctx.Err() != nil {
			return
		}
		x.mu.Lock()
		current := x.workers[info.ID].Conn == c.Conn
		if current {
			delete(x.workers, info.ID)
		}
		x.mu.Unlock()
		if current {
			h.Log().Warn("lost the worker", "worker", info.ID, "err", err)
			_ = store.Apply(core.WorkerGone{ID: info.ID, At: time.Now()})
			h.Redraw()
		}
	}()
}

// loginNotices tell of a worker's logins that have stopped working, naming the worker and the fix, and take back
// the notices of those that work again.
func (x *sessions) loginNotices(w core.WorkerState, was []core.Login) {
	source := "worker:" + string(w.ID)
	where := w.Title()
	if len(w.SignedOut) == 0 {
		for _, n := range x.h.State().Notifications {
			if n.ID == "agent.login" && n.Source == source && !n.Dismissed {
				_ = x.sh.cfg.Store.Apply(core.NoticeDismissed{Seq: n.Seq})
			}
		}
		return
	}
	for _, l := range w.SignedOut {
		if slices.Contains(was, l) {
			continue
		}
		n := core.Notice{ID: "agent.login", Severity: core.SevError, Source: source, Title: "Signed out", Time: time.Now(),
			Text: providerName(l.Provider) + " on " + where + " needs you to log in: " + l.Detail + "."}
		switch {
		case l.Terminal != "":
			n.Text += " Log in opens its terminal with " + l.Terminal + " typed."
			n.Actions = []core.Action{{Title: "Log in", Command: "agent.login", Args: map[string]any{"provider": l.Provider, "worker": string(w.ID)}}}
		case l.Shell != "":
			n.Text += " On " + where + ", run " + l.Shell + "."
		}
		x.h.Notify(n)
	}
}

func (x *sessions) clients() []worker.Client {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []worker.Client
	for _, c := range x.workers {
		out = append(out, c)
	}
	return out
}

// byWorker is the connected workers by ID.
func (x *sessions) byWorker() map[core.WorkerID]worker.Client {
	x.mu.Lock()
	defer x.mu.Unlock()
	return maps.Clone(x.workers)
}

// client is the worker a session lives on.
func (x *sessions) client(id core.SessionID) (worker.Client, error) {
	st := x.sh.cfg.Store.State()
	se, ok := st.Session(id)
	if !ok {
		return worker.Client{}, fmt.Errorf("no session %q", id)
	}
	wid := se.Snapshot().Worker
	if wid == "" {
		wid = core.LocalWorker
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	c, ok := x.workers[wid]
	if !ok {
		return worker.Client{}, fmt.Errorf("the worker %s isn't connected", wid)
	}
	return c, nil
}

// worker is a worker by ID, the local one when id is "".
func (x *sessions) worker(id core.WorkerID) (worker.Client, error) {
	if id == "" {
		id = core.LocalWorker
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if c, ok := x.workers[id]; ok {
		return c, nil
	}
	for _, c := range x.workers {
		if id == core.LocalWorker {
			return c, nil
		}
	}
	return worker.Client{}, fmt.Errorf("no worker %s", id)
}

func (x *sessions) focused() core.SessionID { return x.sh.cfg.Store.Focus() }

// onSession runs fn on the session {id}, or the one in front.
func (x *sessions) onSession(fn func(context.Context, worker.Client, core.SessionID) error) func(registry.Ctx, registry.Args) error {
	return func(ctx registry.Ctx, a registry.Args) error {
		id := core.SessionID(a.String("id"))
		if id == "" {
			id = x.focused()
		}
		if id == "" {
			return errors.New("no session is in front")
		}
		c, err := x.client(id)
		if err != nil {
			return err
		}
		return fn(ctx, c, id)
	}
}

// setThread sets a thread's status: resolved by default.
func (x *sessions) setThread(ctx registry.Ctx, a registry.Args) error {
	return x.onSession(func(cx context.Context, c worker.Client, id core.SessionID) error {
		status := core.ThreadStatus(cmpOr(a.String("status"), string(core.ThreadResolved)))
		return c.SetThread(cx, worker.ThreadReq{Session: id, Thread: core.ThreadID(a.String("thread")), Status: status})
	})(ctx, a)
}

// comment sends a comment through the CommentSend hooks to the session's worker, which hands it to the agent:
// on {thread}, or opening one about {file}:{line}, or the whole change.
func (x *sessions) comment(ctx registry.Ctx, a registry.Args) error {
	return x.onSession(func(cx context.Context, c worker.Client, id core.SessionID) error {
		ev := &CommentSend{Session: string(id), Thread: a.String("thread"), File: a.String("file"), Line: a.Int("line"), Body: a.String("body"),
			Urgent: a.Bool("urgent"), Btw: a.Bool("btw")}
		if v, _ := x.h.Fire(ctx, "CommentSend", ev.File, ev); v.Veto {
			return fmt.Errorf("a CommentSend hook stopped it: %s", v.Reason)
		}
		if strings.TrimSpace(ev.Body) == "" {
			return errors.New("the comment is empty")
		}
		req := worker.CommentReq{Session: id, Thread: core.ThreadID(ev.Thread), Title: a.String("title"), Body: ev.Body, Urgent: ev.Urgent, Btw: ev.Btw,
			Status: core.ThreadStatus(a.String("status")), Severity: a.String("severity"), Finding: a.String("finding")}
		if ev.Thread == "" && ev.File != "" {
			req.Anchor = x.anchor(id, ev.File, ev.Line, a.Int("end_line"), a.String("text"))
		}
		_, err := c.Comment(cx, req)
		return err
	})(ctx, a)
}

// anchor is a place in a session's project, from a path that may be absolute.
func (x *sessions) anchor(id core.SessionID, file string, line, end int, text string) *core.Anchor {
	an := &core.Anchor{File: file, Line: max(line, 1), EndLine: end, Text: text}
	se, ok := x.sh.cfg.Store.Session(id)
	if !ok {
		return an
	}
	st := se.Snapshot()
	if p, ok := st.Primary(); ok {
		an.Project = p.Name
	}
	if !filepath.IsAbs(file) {
		return an
	}
	for _, p := range st.Projects {
		if within(file, p.Dir) {
			rel, _ := filepath.Rel(p.Dir, file)
			an.Project, an.File = p.Name, filepath.ToSlash(rel)
			return an
		}
	}
	if within(file, st.Dir) {
		rel, _ := filepath.Rel(st.Dir, file)
		an.Project, an.File = core.DocsProject, filepath.ToSlash(rel)
	}
	return an
}

// loadRepos reads the session in front's worker's registered repos, off the loop.
func (x *sessions) loadRepos() {
	c, err := x.client(x.focused())
	if err != nil {
		return
	}
	go func() {
		repos, err := c.Repos(context.Background())
		if err != nil {
			x.h.Log().Warn("repos", "err", err)
			return
		}
		x.mu.Lock()
		x.repos = repos
		x.mu.Unlock()
		x.h.Redraw()
	}()
}

// addSource is the picker + in the sidebar opens (ui.md 01h): the worker's repos the session hasn't got, and the
// path typed. Choosing one adds it with session.add-repo, then opens it.
func (x *sessions) addSource(h plugin.Host) registry.PickerSource {
	return registry.PickerSource{ID: "projects.add", Title: "Add a project", PickerOnly: true, Unlisted: true, Live: true, Source: "builtin:sessions",
		Items: func(_ registry.Ctx, query string) []registry.PickerItem {
			se, ok := x.sh.cfg.Store.Session(x.focused())
			if !ok {
				return nil
			}
			var out []registry.PickerItem
			if q := strings.TrimSpace(query); q != "" && (strings.ContainsRune(q, filepath.Separator) || strings.HasPrefix(q, "~")) {
				out = append(out, registry.PickerItem{ID: q, Label: "Add " + q, Detail: "a checkout's path", Icon: "+"})
			}
			has := func(r core.Repo) bool {
				return slices.ContainsFunc(se.Projects(), func(p core.Project) bool {
					return p.Repo == r.Path || p.Dir == r.Path || p.Name == string(r.ID)
				})
			}
			x.mu.Lock()
			repos := slices.Clone(x.repos)
			x.mu.Unlock()
			for _, r := range repos {
				if has(r) || !strings.Contains(strings.ToLower(string(r.ID)+" "+r.Path), strings.ToLower(strings.TrimSpace(query))) {
					continue
				}
				out = append(out, registry.PickerItem{ID: string(r.ID), Label: string(r.ID), Detail: tildePath(r.Path)})
			}
			return out
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			id := x.focused()
			se, ok := x.sh.cfg.Store.Session(id)
			if !ok {
				return errors.New("no session is in front")
			}
			before := se.Projects()
			repo := it.ID
			if strings.HasPrefix(repo, "~") {
				if home, err := os.UserHomeDir(); err == nil {
					repo = filepath.Join(home, repo[1:])
				}
			}
			if err := h.Run(ctx, "session.add-repo", registry.Args{"repo": repo}); err != nil {
				return err
			}
			// The worker's events bring the project; open it once it's here.
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if se, ok := x.sh.cfg.Store.Session(id); ok {
					for _, p := range se.Projects() {
						if !slices.ContainsFunc(before, func(b core.Project) bool { return b.Name == p.Name }) {
							return h.Run(ctx, "project.open", registry.Args{"project": p.Name})
						}
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			return nil
		}}
}

// spec is a session spec from a command's args.
func specFrom(a registry.Args) core.SessionSpec {
	s := core.SessionSpec{Kind: core.Kind(a.String("kind")), Name: a.String("name"), Prompt: a.String("prompt"), Ticket: a.String("ticket"), Context: a.String("context"),
		PR: a.String("pr"), Dir: a.String("dir"), Branch: a.String("branch"), Base: a.String("base"), Provider: a.String("provider"),
		Model: a.String("model"), Effort: a.String("effort"), Worker: core.WorkerID(a.String("worker")), Template: a.String("template"),
		Variant: a.String("variant"), Sandbox: core.SandboxPolicy{Container: a.Bool("sandbox"), Network: a.String("network"), Gate: a.String("gate"),
			Backend: a.String("backend")}}
	if s.Kind == "" {
		s.Kind = core.KindWork
	}
	switch r := a["repos"].(type) {
	case []string:
		for _, x := range r {
			s.Repos = append(s.Repos, core.RepoID(x))
		}
	case []any:
		for _, x := range r {
			if v, ok := x.(string); ok {
				s.Repos = append(s.Repos, core.RepoID(v))
			}
		}
	case string:
		for _, x := range strings.Split(r, ",") {
			if x = strings.TrimSpace(x); x != "" {
				s.Repos = append(s.Repos, core.RepoID(x))
			}
		}
	}
	return s
}

// start starts a session on its worker and brings it to the front, unless {background}.
func (x *sessions) start(ctx context.Context, a registry.Args) (core.SessionID, error) {
	then, err := callsOf(a["then"])
	if err != nil {
		return "", err
	}
	spec := specFrom(a)
	c, err := x.worker(spec.Worker)
	if err != nil {
		return "", err
	}
	clones, _ := settingsfile.StringMap(a["clone"])
	for i, r := range spec.Repos {
		url, ok := clones[string(r)]
		if !ok {
			continue
		}
		repo, err := c.CloneRepo(ctx, worker.CloneReq{URL: url, Name: filepath.Base(string(r))})
		if err != nil {
			return "", err
		}
		spec.Repos[i] = repo.ID
	}
	id, err := c.Start(ctx, spec)
	if err != nil {
		return "", err
	}
	ev := SessionStart{Session: string(id), Kind: string(spec.Kind), Ticket: spec.Ticket, Prompt: spec.Prompt, Worker: string(spec.Worker)}
	x.h.Do(func() { x.h.Fire(context.Background(), "SessionStart", string(id), &ev) })
	// The worker's events bring the session; focus it once it's here.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := x.sh.cfg.Store.Session(id); ok {
			if a.Bool("background") {
				return id, nil
			}
			x.after(id, then)
			return id, x.sh.cfg.Store.Apply(core.SessionFocused{ID: id})
		}
		time.Sleep(10 * time.Millisecond)
	}
	return id, nil
}

// open brings a session to the front, resuming an archived one.
// call is a command to run, with its args.
type call struct {
	command string
	args    registry.Args
}

// callsOf reads a list of {command, args}.
func callsOf(v any) ([]call, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("then is a list of {command, args}, not %T", v)
	}
	var out []call
	for _, it := range list {
		m, ok := asArgs(it)
		if !ok || m.String("command") == "" {
			return nil, fmt.Errorf("then's %v isn't a {command, args}", it)
		}
		args, _ := asArgs(m["args"])
		out = append(out, call{command: m.String("command"), args: args})
	}
	return out, nil
}

func asArgs(v any) (registry.Args, bool) {
	switch v := v.(type) {
	case registry.Args:
		return v, true
	case map[string]any:
		return registry.Args(v), true
	}
	return nil, false
}

// runCalls runs calls in order, logging what fails.
func runCalls(h plugin.Host, calls []call) {
	for _, c := range calls {
		if err := h.Run(context.Background(), c.command, c.args); err != nil {
			h.Log().Warn("session.open then", "command", c.command, "err", err)
		}
	}
}

// open brings a session to the front, resuming it if it's stopped, and runs then once the window is on it: after it
// opens again on the session's directory, if it does.
func (x *sessions) open(ctx context.Context, id core.SessionID, then []call) error {
	se, ok := x.sh.cfg.Store.Session(id)
	if !ok {
		return fmt.Errorf("no session %q", id)
	}
	if st := se.Snapshot(); st.Archived || (st.Agent.Provider != "" && !st.Agent.Running) {
		c, err := x.client(id)
		if err != nil {
			return err
		}
		if err := c.Resume(ctx, id); err != nil {
			return err
		}
	}
	x.after(id, then)
	return x.sh.cfg.Store.Apply(core.SessionFocused{ID: id})
}

// after runs then once the window is on session id: after it opens again on the session's directory, if it does.
func (x *sessions) after(id core.SessionID, then []call) {
	if len(then) == 0 {
		return
	}
	if x.moves(id) {
		x.sh.afterOpen = &afterOpen{session: id, calls: then}
	} else {
		x.h.Do(func() { runCalls(x.h, then) })
	}
}

// sessionDir is where the window opens for a session: its primary project, or its own directory.
func sessionDir(se core.SessionState) string {
	if p, ok := se.Primary(); ok && p.Dir != "" {
		return p.Dir
	}
	return se.Dir
}

// moves says whether the window opens again on session id's directory as it comes to the front: one on this machine
// in another directory.
func (x *sessions) moves(id core.SessionID) bool {
	se, ok := x.sh.cfg.Store.Session(id)
	if !ok || x.sh.reopen == nil {
		return false
	}
	st := se.Snapshot()
	dir := sessionDir(st)
	return dir != "" && filepath.Clean(dir) != filepath.Clean(x.sh.cfg.WorkDir) && (st.Worker == "" || st.Worker == core.LocalWorker)
}

// follow reopens the window on the session now in front, when its directory is on this machine.
func (x *sessions) follow(id core.SessionID) {
	if !x.moves(id) {
		return
	}
	se, _ := x.sh.cfg.Store.Session(id)
	_ = x.sh.reopenOn(reopenTo{dir: sessionDir(se.Snapshot()), session: id})
}

// readOnly says why a file opens read-only in the session the window is on: it's outside the session's
// projects and directory, or in a project the session only reads (sessions.md → In the editor).
func (x *sessions) readOnly(path string) string {
	se, ok := x.sh.cfg.Store.Session(x.focused())
	if !ok || path == "" {
		return ""
	}
	st := se.Snapshot()
	work := x.sh.cfg.WorkDir
	if !filepath.IsAbs(path) {
		path = filepath.Join(work, path)
	}
	type root struct {
		dir      string
		readOnly bool
	}
	roots := []root{{dir: st.Dir}}
	for _, p := range st.Projects {
		roots = append(roots, root{p.Dir, p.ReadOnly})
	}
	if !slices.ContainsFunc(roots, func(r root) bool { return within(work, r.dir) }) {
		return "" // the window isn't on this session
	}
	best := -1
	for i, r := range roots {
		if within(path, r.dir) && (best < 0 || len(r.dir) > len(roots[best].dir)) {
			best = i
		}
	}
	switch {
	case best < 0:
		return "outside the session"
	case roots[best].readOnly:
		return "the session only reads it"
	}
	return ""
}

// within reports whether path is dir or under it, as written or with symbolic links resolved.
func within(path, dir string) bool {
	if dir == "" || path == "" {
		return false
	}
	in := func(p, d string) bool {
		rel, err := filepath.Rel(d, p)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if in(filepath.Clean(path), filepath.Clean(dir)) {
		return true
	}
	rp, err1 := filepath.EvalSymlinks(path)
	rd, err2 := filepath.EvalSymlinks(dir)
	return err1 == nil && err2 == nil && in(rp, rd)
}

// agentTools are the tools plugins offer an agent, as a worker hands them on.
func (x *sessions) agentTools(h plugin.Host, r worker.ToolsReq) []agent.Tool {
	var out []agent.Tool
	name, utility := x.agentName(r.Session, r.Agent)
	for _, t := range registry.AgentToolsFor(h, name, utility) {
		out = append(out, agent.Tool{Name: t.Name, Description: t.Desc, Schema: t.Schema(), ReadOnly: t.ReadOnly && t.Confirm == nil})
	}
	return out
}

// agentName is the agent a worker asks for: a session's, by its kind, or a utility agent with no session.
func (x *sessions) agentName(id core.SessionID, agent string) (string, bool) {
	if id == "" {
		return agent, true
	}
	if agent == "" {
		if se, ok := x.sh.cfg.Store.Session(id); ok {
			agent = string(se.Kind())
		}
	}
	return agent, false
}

// callTool runs a tool for an agent on worker c. A session's agent is owned by its worker: an act there is asked
// through the session's rules, the policy hooks and then the user's card, as the agent's own gate is.
func (x *sessions) callTool(ctx context.Context, h plugin.Host, c worker.Client, call worker.ToolCall) (string, error) {
	name, utility := x.agentName(call.Session, call.Agent)
	for _, t := range registry.AgentToolsFor(h, name, utility) {
		if t.Name != call.Tool || t.Run == nil {
			continue
		}
		var params map[string]any
		if len(call.Args) > 0 {
			if err := json.Unmarshal(call.Args, &params); err != nil {
				return "", err
			}
		}
		ctx := registry.WithAgent(registry.WithSession(ctx, string(call.Session)), name)
		if call.Session != "" {
			ctx = registry.WithOwner(ctx, registry.AgentOwner{Agent: name, Source: "builtin:sessions",
				Confirm: func(ctx registry.Ctx, tool string, cf registry.Confirmation) (bool, error) {
					return askForSession(ctx, c, call, cf)
				}})
		}
		return registry.CallAgentTool(ctx, h, t, params)
	}
	return "", fmt.Errorf("no tool %q", call.Tool)
}

// askForSession asks for a session's agent's act on its worker. The rule "Always" makes is the tool's name.
func askForSession(ctx context.Context, c worker.Client, call worker.ToolCall, cf registry.Confirmation) (bool, error) {
	tool := "mcp__oriel__" + call.Tool
	v, err := c.Ask(ctx, worker.AskReq{Session: call.Session, Ask: core.Ask{Kind: core.AskPermission, Tool: tool,
		Summary: cmpOr(cf.Line, cf.Title), Detail: cf.Lead, Input: string(call.Args), Rule: tool}})
	if err != nil {
		return false, err
	}
	if v.Decision == "allow" {
		return true, nil
	}
	if v.Note != "" {
		return false, fmt.Errorf("denied by %s: %s", cmpOr(v.By, "the user"), v.Note)
	}
	return false, fmt.Errorf("denied by %s, so nothing was done", cmpOr(v.By, "the user"))
}

// decide runs the Permission hooks on an ask: layer 2, before it reaches the user.
func (x *sessions) decide(ctx context.Context, h plugin.Host, d worker.Decide) worker.Verdict {
	ev := registry.PermissionEvent{Session: string(d.Session), Kind: string(d.Ask.Kind), Tool: d.Ask.Tool, Summary: d.Ask.Summary,
		Input: mainInput(d.Ask.Input), Rule: d.Ask.Rule, Host: d.Ask.Host}
	subject := d.Ask.Tool
	if subject == "" {
		subject = d.Ask.Host
	}
	var v registry.Verdict
	done := make(chan struct{})
	h.Do(func() {
		defer close(done)
		v, _ = h.Fire(ctx, "Permission", subject, ev)
	})
	select {
	case <-done:
	case <-ctx.Done():
		return worker.Verdict{}
	}
	switch {
	case v.Veto:
		return worker.Verdict{Decision: "deny", Note: v.Reason, By: "a Permission hook"}
	case !v.Decided:
		return worker.Verdict{}
	}
	switch val := v.Value.(type) {
	case string:
		if val == "allow" || val == "deny" {
			return worker.Verdict{Decision: val, By: "a Permission hook"}
		}
	case registry.PermissionVerdict:
		return worker.Verdict{Decision: val.Decision, Note: val.Note, By: "a Permission hook"}
	case map[string]any:
		dec, _ := val["decision"].(string)
		note, _ := val["note"].(string)
		return worker.Verdict{Decision: dec, Note: note, By: "a Permission hook"}
	case bool:
		if val {
			return worker.Verdict{Decision: "allow", By: "a Permission hook"}
		}
		return worker.Verdict{Decision: "deny", By: "a Permission hook"}
	}
	return worker.Verdict{}
}

// explain answers "ask to explain" from a tool-less fork of the session in front: it knows why the code is as
// it is, and changes nothing.
func (x *sessions) explain(ctx context.Context, r registry.ExplainRequest) (string, error) {
	id := x.focused()
	if id == "" {
		return "", errors.New("no session is in front to explain from")
	}
	c, err := x.client(id)
	if err != nil {
		return "", err
	}
	how := map[string]string{registry.ExplainDetailed: "in detail: what it does, step by step, and the cases it handles",
		registry.ExplainWhy: "and why it is the way it is, from its history below and what you know of this session"}[r.Depth]
	if how == "" {
		how = "briefly, in a short paragraph"
	}
	var b strings.Builder
	if r.Format != "" {
		fmt.Fprintf(&b, "Explain this %s. You have no tools here, and this exchange is not part of the main conversation.\n\n%s\n\n%s",
			r.Kind, r.Subject, r.Context)
	} else {
		fmt.Fprintf(&b, "Explain this %s %s, in markdown. You have no tools here, and this exchange is not part of the main conversation.\n\n%s\n\n%s",
			r.Kind, how, r.Subject, r.Context)
	}
	if len(r.Files) > 0 {
		fmt.Fprintf(&b, "\n\nFiles: %s", strings.Join(r.Files, ", "))
	}
	if r.Format != "" {
		b.WriteString("\n\n" + r.Format)
	}
	return c.Fork(ctx, id, b.String())
}

// explainer is who answers explain: the session in front's agent, forked.
func (x *sessions) explainer() string {
	se, ok := x.sh.cfg.Store.Session(x.focused())
	if !ok {
		return "no session in front"
	}
	return "fork of " + cmpOr(se.Snapshot().Agent.Provider, "the agent") + " · knows this session"
}

// explainerByline is who wrote a long answer, such as a walkthrough: the agent in front, from what it knows.
func (x *sessions) explainerByline() string {
	se, ok := x.sh.cfg.Store.Session(x.focused())
	if !ok {
		return "written by the agent"
	}
	return "written by " + cmpOr(se.Snapshot().Agent.Provider, "the agent") + " from the session's own context"
}

// fork answers a side question from a tool-less fork of the session's agent.
func (x *sessions) fork(ctx context.Context, r registry.ForkRequest) (string, error) {
	c, err := x.client(core.SessionID(r.Session))
	if err != nil {
		return "", err
	}
	return c.Fork(ctx, core.SessionID(r.Session), r.Prompt)
}

// forker is who answers a session's side questions: "claude · fork, no tools".
func (x *sessions) forker(id string) string {
	se, ok := x.sh.cfg.Store.Session(core.SessionID(id))
	if !ok {
		return ""
	}
	return cmpOr(se.Snapshot().Agent.Provider, "the agent") + " · fork, no tools"
}

// mainInput is what a hook matches a tool's input on: a shell's command, an edit's path, else the JSON.
func mainInput(raw string) string {
	var in map[string]any
	if json.Unmarshal([]byte(raw), &in) != nil {
		return raw
	}
	for _, k := range []string{"command", "file_path", "path", "url"} {
		if s, ok := in[k].(string); ok {
			return s
		}
	}
	return raw
}

// startWorkReq is session.start-work's args as the worker's request: {clear} and {fresh} (both on unless false),
// {branches} by project name, {sandbox}, {network}, {model} and {effort}, each the session's own when absent.
func startWorkReq(id core.SessionID, a registry.Args) worker.StartWorkReq {
	r := worker.StartWorkReq{Session: id, KeepThreads: a["clear"] == false, KeepConversation: a["fresh"] == false,
		Network: a.String("network"), Model: a.String("model"), Effort: a.String("effort")}
	if v, ok := a["sandbox"].(bool); ok {
		r.Sandbox = &v
	}
	switch b := a["branches"].(type) {
	case map[string]string:
		r.Branches = b
	case map[string]any:
		r.Branches = map[string]string{}
		for k, v := range b {
			if s, ok := v.(string); ok {
				r.Branches[k] = s
			}
		}
	}
	return r
}
