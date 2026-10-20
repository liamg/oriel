package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// sessions is the app's side of the session host (architecture.md): it follows the session in front with the
// window, and answers what the host asks of the app: the tools plugins offer agents, and the Permission hooks
// (agents.md, layer 2). Commands drive it, so Lua and keys reach it the same way.
type sessions struct {
	disposer
	sh     *shell
	h      plugin.Host
	ctx    context.Context
	cancel context.CancelFunc

	sandboxKick chan struct{} // the sandbox settings are to go to the host
	promptsKick chan struct{} // the user's prompts are to go to the host
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
	x.sandboxKick = make(chan struct{}, 1)
	x.sandboxSettings(ctx)
	x.promptsKick = make(chan struct{}, 1)
	x.promptSettings(ctx)
	if w := x.sh.cfg.Host; w != nil {
		w.SetApp(host.App{
			Tools:    func(r host.ToolsReq) []agent.Tool { return x.agentTools(h, r) },
			CallTool: func(ctx context.Context, call host.ToolCall) (string, error) { return x.callTool(ctx, h, w, call) },
			Decide:   func(ctx context.Context, d host.Decide) host.Verdict { return x.decide(ctx, h, d) },
		})
		x.add(onDispose(func() { w.SetApp(host.App{}) }))
		x.sendSandbox()
		x.sendPrompts()
	}
	x.add(h.DeclareEvent(registry.EventInfo{Name: "Permission", Fired: true, Payload: registry.PermissionEvent{},
		Doc: "An agent, the egress proxy or the gateway wants something; a Decide hook answers allow or deny before the user is asked."}))
	if x.sh.cfg.Host != nil {
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
		Doc: "A session just started; the subject is its id. { session, kind, ticket, prompt }"}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "CommentSend", Fired: true, Payload: CommentSend{},
		Doc: "A comment about to go to an agent; a rewrite changes it, a veto stops it. { session, thread, file, line, body, urgent, btw }"}))
	working := map[core.SessionID]bool{}
	var signedOut []core.Login
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.MachineChanged:
			was := signedOut
			signedOut = ev.Machine.SignedOut
			if !slices.Equal(was, signedOut) {
				h.Do(func() { x.loginNotices(ev.Machine.SignedOut, was) })
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
	has := func(registry.Args) bool { return x.sh.cfg.Host != nil }
	focused := func(registry.Args) bool { return has(nil) && x.focused() != "" }
	named := func(a registry.Args) bool { return has(nil) && (a.String("id") != "" || x.focused() != "") } // {id}, or the one in front
	planning := func(registry.Args) bool {
		se, ok := x.sh.cfg.Store.Session(x.focused())
		return ok && has(nil) && se.Snapshot().Planning()
	}
	x.add(h.Picker(x.addSource(h)))
	x.add(h.Keymap(registry.Keymap{Mode: QuitKeyMode, Keys: "<D-CR>", Command: "quit.now", Desc: "Don't wait for the turns"}))
	// While Oriel is quitting, its dialog keeps the keys: nothing else opens over it or runs.
	x.add(h.Contribute(registry.ExclusiveKeys{Mode: QuitKeyMode, Source: "builtin:sessions"}))
	x.add(h.View(OverlayPrefix+"quit", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "quit", Fn: x.drainView}
	})))
	for _, c := range []registry.Command{
		{ID: "session.start", Title: "Start a session: {kind}, {prompt}, {repos}, {provider}, {model}, {effort}, {sandbox}…; {background} leaves it behind the one in front; {then} is a list of {command, args} run once the window is on it",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error { _, err := x.start(ctx, a); return err }},
		{ID: "session.open", Title: "Bring a session to the front: {id}, resuming it if it's stopped or archived; {then} is a list of {command, args} run once the window is on it",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error {
				then, err := callsOf(a["then"])
				if err != nil {
					return err
				}
				return x.open(ctx, core.SessionID(a.String("id")), then)
			}},
		{ID: "quit.now", Title: "Quit now, not waiting for the agents' turns",
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
			Run: x.slowSession("session.stop", "Stopping", func(ctx context.Context, w *host.Host, id core.SessionID) error { return w.Stop(ctx, id) })},
		{ID: "session.archive", Title: "Archive {id}, or the session in front: its worktrees go, its threads and transcript stay", Available: named,
			Run: x.slowSession("session.archive", "Archiving", func(ctx context.Context, w *host.Host, id core.SessionID) error { return w.Archive(ctx, id) })},
		{ID: "session.start-work", Title: "Start work on this plan: worktrees, then the agent implements it; {clear}, {fresh}, {branches}, {sandbox}, {network}, {model}, {effort}",
			Available: planning,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.slowSession("session.start-work", "Starting work on", func(ctx context.Context, w *host.Host, id core.SessionID) error {
					return w.StartWork(ctx, startWorkReq(id, a))
				})(ctx, a)
			}},
		{ID: "session.configure", Title: "Switch the agent of {id}, or the session in front, to {model} at {effort}, its conversation kept; only while it's idle",
			Available: named,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.slowSession("session.configure", "Switching the agent of", func(ctx context.Context, w *host.Host, id core.SessionID) error {
					return w.Configure(ctx, host.ConfigureReq{Session: id, Model: a.String("model"), Effort: a.String("effort")})
				})(ctx, a)
			}},
		{ID: "session.interrupt", Title: "Interrupt the agent's turn: {id}'s, or the session in front's", Available: named,
			Run: x.onSession(func(_ context.Context, w *host.Host, id core.SessionID) error { return w.Interrupt(id) })},
		{ID: "session.rename", Title: "Rename {id}, or the session in front: {name}", Available: named,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.onSession(func(_ context.Context, w *host.Host, id core.SessionID) error {
					return w.Rename(id, a.String("name"))
				})(ctx, a)
			}},
		{ID: "session.add-repo", Title: "Add a repo to the session in front: {repo}, by name or path", Available: focused,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.slowSession("session.add-repo", "Adding "+a.String("repo")+" to", func(ctx context.Context, w *host.Host, id core.SessionID) error {
					return w.AddRepo(ctx, id, core.RepoID(a.String("repo")))
				})(ctx, a)
			}},
		{ID: "project.add", Title: "Add a project to the session in front: pick a registered repo, or type a checkout's path",
			Available: func(registry.Args) bool {
				se, ok := x.sh.cfg.Store.Session(x.focused())
				return focused(nil) && ok && se.Kind() != core.KindDirect
			},
			Run: func(ctx registry.Ctx, _ registry.Args) error {
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

// loginNotices tell of logins that have stopped working, and the fix, and take back the notices of those that
// work again.
func (x *sessions) loginNotices(signedOut, was []core.Login) {
	const source = "login"
	if len(signedOut) == 0 {
		for _, n := range x.h.State().Notifications {
			if n.ID == "agent.login" && strings.HasPrefix(n.Source, source) && !n.Dismissed {
				_ = x.sh.cfg.Store.Apply(core.Dismissal(n))
			}
		}
		return
	}
	for _, l := range signedOut {
		if slices.Contains(was, l) {
			continue
		}
		n := core.Notice{ID: "agent.login", Severity: core.SevError, Source: source + ":" + l.Provider, Title: "Signed out", Time: time.Now(),
			Text: providerName(l.Provider) + " needs you to log in: " + l.Detail + "."}
		switch {
		case l.Terminal != "":
			n.Text += " Log in opens its terminal with " + l.Terminal + " typed."
			n.Actions = []core.Action{{Title: "Log in", Command: "agent.login", Args: map[string]any{"provider": l.Provider}}}
		case l.Shell != "":
			n.Text += " Run " + l.Shell + " in a terminal."
		}
		x.h.Notify(n)
	}
}

// host is the session host, or why there's none.
func (x *sessions) host() (*host.Host, error) {
	if x.sh.cfg.Host == nil {
		return nil, errors.New("there are no sessions in this window")
	}
	return x.sh.cfg.Host, nil
}

func (x *sessions) focused() core.SessionID { return x.sh.cfg.Store.Focus() }

// onSession runs fn on the session {id}, or the one in front.
func (x *sessions) onSession(fn func(context.Context, *host.Host, core.SessionID) error) func(registry.Ctx, registry.Args) error {
	return func(ctx registry.Ctx, a registry.Args) error {
		id := core.SessionID(a.String("id"))
		if id == "" {
			id = x.focused()
		}
		if id == "" {
			return errors.New("no session is in front")
		}
		w, err := x.host()
		if err != nil {
			return err
		}
		return fn(ctx, w, id)
	}
}

// slowSession is onSession for host work that runs off the loop (plugin.RunSlow): doing, then the session, says what
// it is.
func (x *sessions) slowSession(command, doing string, fn func(context.Context, *host.Host, core.SessionID) error) func(registry.Ctx, registry.Args) error {
	return x.onSession(func(ctx context.Context, w *host.Host, id core.SessionID) error {
		return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: command + "." + string(id), Doing: doing + " " + x.label(id)},
			func(ctx context.Context) (struct{}, error) { return struct{}{}, fn(ctx, w, id) }, nil)
	})
}

// label is how session id is named to the user.
func (x *sessions) label(id core.SessionID) string {
	if se, ok := x.sh.cfg.Store.Session(id); ok {
		return label(se.Snapshot())
	}
	return string(id)
}

// setThread sets a thread's status: resolved by default.
func (x *sessions) setThread(ctx registry.Ctx, a registry.Args) error {
	return x.onSession(func(_ context.Context, w *host.Host, id core.SessionID) error {
		status := core.ThreadStatus(cmpOr(a.String("status"), string(core.ThreadResolved)))
		return w.SetThread(host.ThreadReq{Session: id, Thread: core.ThreadID(a.String("thread")), Status: status})
	})(ctx, a)
}

// comment sends a comment through the CommentSend hooks to the session host, which hands it to the agent:
// on {thread}, or opening one about {file}:{line}, or the whole change.
func (x *sessions) comment(ctx registry.Ctx, a registry.Args) error {
	return x.onSession(func(cx context.Context, w *host.Host, id core.SessionID) error {
		ev := &CommentSend{Session: string(id), Thread: a.String("thread"), File: a.String("file"), Line: a.Int("line"), Body: a.String("body"),
			Urgent: a.Bool("urgent"), Btw: a.Bool("btw")}
		if v, _ := x.h.Fire(ctx, "CommentSend", ev.File, ev); v.Veto {
			return fmt.Errorf("a CommentSend hook stopped it: %s", v.Reason)
		}
		if strings.TrimSpace(ev.Body) == "" {
			return errors.New("the comment is empty")
		}
		req := host.CommentReq{Session: id, Thread: core.ThreadID(ev.Thread), Title: a.String("title"), Body: ev.Body, Urgent: ev.Urgent, Btw: ev.Btw,
			Status: core.ThreadStatus(a.String("status")), Severity: a.String("severity"), Finding: a.String("finding")}
		if ev.Thread == "" && ev.File != "" {
			req.Anchor = x.anchor(id, ev.File, ev.Line, a.Int("end_line"), a.String("text"))
		}
		_, err := w.Comment(cx, req)
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

// addNewRepo is the sidebar picker's last item: the Add repo dialog, the repo it adds then added to the session.
const addNewRepo = "\x00repos.new"

// addSource is the picker + in the sidebar opens (ui.md 01h): the registered repos the session hasn't got, and the
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
			for _, r := range x.h.State().Repos {
				if has(r) || !strings.Contains(strings.ToLower(string(r.ID)+" "+r.Path), strings.ToLower(strings.TrimSpace(query))) {
					continue
				}
				out = append(out, registry.PickerItem{ID: string(r.ID), Label: string(r.ID), Detail: tildePath(r.Path)})
			}
			return append(out, registry.PickerItem{ID: addNewRepo, Label: "Add a new repo…", Detail: "a checkout, or a clone", Icon: "+"})
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			id := x.focused()
			se, ok := x.sh.cfg.Store.Session(id)
			if !ok {
				return errors.New("no session is in front")
			}
			if it.ID == addNewRepo {
				return h.Run(ctx, "repos.new", registry.Args{"then": []any{map[string]any{"command": "session.add-repo"}}})
			}
			before := se.Projects()
			repo := it.ID
			if strings.HasPrefix(repo, "~") {
				if home, err := os.UserHomeDir(); err == nil {
					repo = filepath.Join(home, repo[1:])
				}
			}
			x.addProject(id, repo, before)
			return nil
		}}
}

// addProject adds repo to session id off the loop, a notice up while it works, and opens the project once the host's
// events bring it.
func (x *sessions) addProject(id core.SessionID, repo string, before []core.Project) {
	h := x.h
	const notice = "projects.add"
	h.Notify(core.Notice{ID: notice, Severity: core.SevInfo, Source: "sessions", Title: "Add a project", Text: "Adding " + repo + " to the session"})
	added := func() (string, bool) {
		se, ok := x.sh.cfg.Store.Session(id)
		if !ok {
			return "", false
		}
		for _, p := range se.Projects() {
			if !slices.ContainsFunc(before, func(b core.Project) bool { return b.Name == p.Name }) {
				return p.Name, true
			}
		}
		return "", false
	}
	go func() {
		arrived := make(chan struct{}, 1)
		stop := h.Subscribe(core.Filter{Session: id}, func(core.Event) {
			select {
			case arrived <- struct{}{}:
			default:
			}
		})
		defer stop()
		err := plugin.Await(context.Background(), func(ctx registry.Ctx) error {
			return h.Run(ctx, "session.add-repo", registry.Args{"id": string(id), "repo": repo})
		})
		name, ok := added()
		for timeout := time.After(30 * time.Second); err == nil && !ok; name, ok = added() {
			select {
			case <-arrived:
			case <-timeout:
				err = fmt.Errorf("%s was added, but its project never arrived", repo)
			}
		}
		h.Do(func() {
			h.Dismiss(notice)
			if err == nil {
				err = h.Run(context.Background(), "project.open", registry.Args{"project": name})
			}
			if err != nil {
				h.Notify(core.Notice{ID: notice, Severity: core.SevError, Source: "sessions", Key: true, Text: err.Error()})
			}
		})
		h.Redraw()
	}()
}

// spec is a session spec from a command's args.
func specFrom(a registry.Args) core.SessionSpec {
	s := core.SessionSpec{Kind: core.Kind(a.String("kind")), Name: a.String("name"), Prompt: a.String("prompt"), Ticket: a.String("ticket"), Context: a.String("context"),
		PR: a.String("pr"), PRRemote: a.String("pr_remote"), PRRef: a.String("pr_ref"), Dir: a.String("dir"), Branch: a.String("branch"), Base: a.String("base"), Provider: a.String("provider"),
		Model: a.String("model"), Effort: a.String("effort"), Template: a.String("template"),
		Sandbox: core.SandboxPolicy{Container: a.Bool("sandbox"), Network: a.String("network"), Gate: a.String("gate"),
			Backend: a.String("backend")}}
	if s.Kind == "" {
		s.Kind = core.KindWork
	}
	for _, r := range a.Strings("repos") {
		s.Repos = append(s.Repos, core.RepoID(r))
	}
	return s
}

// prCheckout asks a review's source where its PR is fetched from, unless the spec says already.
func (x *sessions) prCheckout(ctx context.Context, spec *core.SessionSpec) error {
	if spec.PR == "" || spec.PRRef != "" {
		return nil
	}
	src, ok := x.sh.sites.url(spec.PR)
	if !ok || src.Checkout == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, err := registry.CheckoutFor(ctx, src, spec.PR)
	if err != nil {
		return fmt.Errorf("%s couldn't say where to fetch %s from: %w", src.Label(), spec.PR, err)
	}
	spec.PRRemote, spec.PRRef = c.Remote, c.Ref
	return nil
}

// start starts a session and brings it to the front, unless {background}.
func (x *sessions) start(ctx context.Context, a registry.Args) (core.SessionID, error) {
	then, err := callsOf(a["then"])
	if err != nil {
		return "", err
	}
	spec := specFrom(a)
	w, err := x.host()
	if err != nil {
		return "", err
	}
	if err := x.prCheckout(ctx, &spec); err != nil {
		return "", err
	}
	id, err := w.Start(ctx, spec)
	if err != nil {
		return "", err
	}
	ev := SessionStart{Session: string(id), Kind: string(spec.Kind), Ticket: spec.Ticket, Prompt: spec.Prompt}
	x.h.Do(func() { x.h.Fire(context.Background(), "SessionStart", string(id), &ev) })
	if a.Bool("background") {
		return id, nil
	}
	x.after(id, then)
	return id, x.sh.cfg.Store.Apply(core.SessionFocused{ID: id})
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
	front := func() error {
		x.after(id, then)
		return x.sh.cfg.Store.Apply(core.SessionFocused{ID: id})
	}
	if st := se.Snapshot(); st.Archived || (st.Agent.Provider != "" && !st.Agent.Running) {
		w, err := x.host()
		if err != nil {
			return err
		}
		return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "session.open." + string(id), Doing: "Resuming " + label(st)},
			func(ctx context.Context) (struct{}, error) { return struct{}{}, w.Resume(ctx, id) },
			func(registry.Ctx, struct{}) error { return front() })
	}
	return front()
}

// openDirect brings dir's direct session with no agent to the front, starting one if it hasn't one.
func (x *sessions) openDirect(ctx context.Context, dir string) error {
	for _, se := range x.h.State().Sessions {
		if se.Kind == core.KindDirect && se.Agent.Provider == "" && filepath.Clean(se.Dir) == filepath.Clean(dir) {
			return x.open(ctx, se.ID, nil)
		}
	}
	_, err := x.start(ctx, registry.Args{"kind": string(core.KindDirect), "dir": dir})
	return err
}

// holds says whether the session in front has dir: its own directory, or one of its projects'.
func (x *sessions) holds(dir string) bool {
	se, ok := x.sh.cfg.Store.Session(x.focused())
	if !ok {
		return false
	}
	st := se.Snapshot()
	return slices.ContainsFunc(append([]string{st.Dir}, dirsOf(st.Projects)...), func(d string) bool { return d != "" && inside(dir, d) })
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

// moves says whether the window opens again on session id's directory as it comes to the front: one in another
// directory.
func (x *sessions) moves(id core.SessionID) bool {
	se, ok := x.sh.cfg.Store.Session(id)
	if !ok || x.sh.reopen == nil {
		return false
	}
	st := se.Snapshot()
	dir := sessionDir(st)
	return dir != "" && filepath.Clean(dir) != filepath.Clean(x.sh.cfg.WorkDir)
}

// follow reopens the window on the session now in front, when it's in another directory.
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

// agentTools are the tools plugins offer an agent, as the host hands them on.
func (x *sessions) agentTools(h plugin.Host, r host.ToolsReq) []agent.Tool {
	var out []agent.Tool
	name, utility := x.agentName(r.Session, r.Agent)
	for _, t := range registry.AgentToolsFor(h, name, utility) {
		out = append(out, agent.Tool{Name: t.Name, Description: t.Desc, Schema: t.Schema(), ReadOnly: t.ReadOnly && t.Confirm == nil})
	}
	return out
}

// agentName is the agent the host asks for: a session's, by its kind, or a utility agent with no session.
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

// callTool runs a tool for an agent. A session's agent is owned by the host: an act there is asked through the
// session's rules, the policy hooks and then the user's card, as the agent's own gate is.
func (x *sessions) callTool(ctx context.Context, h plugin.Host, w *host.Host, call host.ToolCall) (string, error) {
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
					return askForSession(ctx, w, call, cf)
				}})
		}
		return registry.CallAgentTool(ctx, h, t, params)
	}
	return "", fmt.Errorf("no tool %q", call.Tool)
}

// askForSession asks for a session's agent's act. The rule "Always" makes is the tool's name.
func askForSession(ctx context.Context, w *host.Host, call host.ToolCall, cf registry.Confirmation) (bool, error) {
	tool := "mcp__oriel__" + call.Tool
	v, err := w.Ask(ctx, host.AskReq{Session: call.Session, Ask: core.Ask{Kind: core.AskPermission, Tool: tool,
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
func (x *sessions) decide(ctx context.Context, h plugin.Host, d host.Decide) host.Verdict {
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
		return host.Verdict{}
	}
	switch {
	case v.Veto:
		return host.Verdict{Decision: "deny", Note: v.Reason, By: "a Permission hook"}
	case !v.Decided:
		return host.Verdict{}
	}
	switch val := v.Value.(type) {
	case string:
		if val == "allow" || val == "deny" {
			return host.Verdict{Decision: val, By: "a Permission hook"}
		}
	case registry.PermissionVerdict:
		return host.Verdict{Decision: val.Decision, Note: val.Note, By: "a Permission hook"}
	case map[string]any:
		dec, _ := val["decision"].(string)
		note, _ := val["note"].(string)
		return host.Verdict{Decision: dec, Note: note, By: "a Permission hook"}
	case bool:
		if val {
			return host.Verdict{Decision: "allow", By: "a Permission hook"}
		}
		return host.Verdict{Decision: "deny", By: "a Permission hook"}
	}
	return host.Verdict{}
}

// explain answers "ask to explain" from a tool-less fork of the session in front: it knows why the code is as
// it is, and changes nothing.
func (x *sessions) explain(ctx context.Context, r registry.ExplainRequest) (string, error) {
	id := x.focused()
	if id == "" {
		return "", errors.New("no session is in front to explain from")
	}
	w, err := x.host()
	if err != nil {
		return "", err
	}
	how := map[string]string{registry.ExplainDetailed: "in detail: what it does, step by step, and the cases it handles, in markdown",
		registry.ExplainWhy: "and why it is the way it is, from its history below and what you know of this session, in markdown"}[r.Depth]
	switch {
	case r.Format != "":
		how = "in the form asked for at the end"
	case how == "":
		how = "briefly, in a short paragraph, in markdown"
	}
	var b strings.Builder
	b.WriteString(x.sh.prompt("explain.request", map[string]string{"kind": r.Kind, "how": how}))
	fmt.Fprintf(&b, "\n\n%s\n\n%s", r.Subject, r.Context)
	if len(r.Files) > 0 {
		fmt.Fprintf(&b, "\n\nFiles: %s", strings.Join(r.Files, ", "))
	}
	if r.Format != "" {
		b.WriteString("\n\n" + r.Format)
	}
	return w.Fork(id, b.String())
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
func (x *sessions) fork(_ context.Context, r registry.ForkRequest) (string, error) {
	w, err := x.host()
	if err != nil {
		return "", err
	}
	return w.Fork(core.SessionID(r.Session), r.Prompt)
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

// startWorkReq is session.start-work's args as the host's request: {clear} and {fresh} (both on unless false),
// {branches} by project name, {sandbox}, {network}, {model} and {effort}, each the session's own when absent.
func startWorkReq(id core.SessionID, a registry.Args) host.StartWorkReq {
	r := host.StartWorkReq{Session: id, KeepThreads: a["clear"] == false, KeepConversation: a["fresh"] == false,
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
