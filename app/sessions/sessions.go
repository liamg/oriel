package sessions

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/app/kit/agentkit"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// Sessions is the app's side of the session host (docs/architecture.md): it follows the session in front with the
// window, and answers what the host asks of the app: the tools plugins offer agents, and the Permission hooks.
// Commands drive it, so Lua and keys reach it the same way.
type Sessions struct {
	kit.Disposer
	Host      *host.Host // the session host; nil for none
	ConfigDir string
	h         plugin.Host
	ctx       context.Context
	cancel    context.CancelFunc
	starts    atomic.Int64 // session.start's runs, naming each one's slow work
}

// openedKey keeps the directory a session's kind's opening commands last ran on: they run again only on another.
const openedKey = "session.opened"

func (x *Sessions) Manifest() plugin.Manifest { return plugin.Manifest{ID: "sessions"} }

func (x *Sessions) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	ctx, cancel := context.WithCancel(context.Background())
	x.ctx, x.cancel = ctx, cancel
	if w := x.Host; w != nil {
		w.SetApp(host.App{
			Tools:    func(r host.ToolsReq) []agent.Tool { return x.agentTools(h, r) },
			CallTool: func(ctx context.Context, call host.ToolCall) (string, error) { return x.callTool(ctx, h, w, call) },
			Decide:   func(ctx context.Context, d host.Decide) host.Verdict { return x.decide(ctx, h, d) },
		})
		x.Add(registry.DisposeFunc(func() { w.SetApp(host.App{}) }))
	}
	x.contributeKinds(h)
	landAtLaunch(h)
	x.Add(keepATabInFront(h))
	x.Add(h.Option(registry.OptionSpec{Name: autoNameOption, Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Name a session started from a request, and the request, with a small model (Claude's Haiku). Off, the name is the request's first words."}))
	x.Add(h.Option(registry.OptionSpec{Name: restartOption, Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Start an open session's agent again, on its conversation, when it exits on its own. One that exits 3 times in 2 minutes stays stopped."}))
	x.Add(h.Options().Watch(restartOption, x.sendRestart))
	x.sendRestart()
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "Permission", Fired: true, Payload: registry.PermissionEvent{},
		Doc: "An agent, the egress proxy or the gateway wants something; a Decide hook answers allow or deny before the user is asked."}))
	if x.Host != nil {
		x.Add(h.Contribute(registry.Explainer{Name: "agent", Order: 10, Explain: x.explain, Label: x.explainer, Byline: x.explainerByline}))
		x.Add(h.Contribute(registry.Forker{Name: "agent", Order: 10, Fork: x.fork, Label: x.forker}))
	}
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "AgentTurnEnd", Fired: true, Payload: kit.AgentTurnEnd{},
		Doc: "A session's agent finished a turn."}))
	x.Add(registry.On(h, registry.Hook{Kind: registry.Rewrite, Label: "files outside the session open read-only"},
		func(_ registry.Ctx, ev *kit.BufRead) (registry.Verdict, error) {
			if why := x.readOnly(ev.File); why != "" {
				ev.ReadOnly = why
				return registry.Verdict{Rewrote: true}, nil
			}
			return registry.Verdict{}, nil
		}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "SessionStart", Fired: true, Payload: kit.SessionStart{},
		Doc: "A session just started; the subject is its id. { session, kind, ticket, prompt }"}))
	x.Add(h.DeclareEvent(registry.EventInfo{Name: "CommentSend", Fired: true, Payload: kit.CommentSend{},
		Doc: "A comment about to go to an agent; a rewrite changes it, a veto stops it. { session, thread, file, line, body, urgent, btw }"}))
	working := map[core.SessionID]bool{}
	var signedOut []core.Login
	x.Add(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.MachineChanged:
			was := signedOut
			signedOut = ev.Machine.SignedOut
			if !slices.EqualFunc(was, signedOut, core.Login.Same) {
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
					h.Fire(context.Background(), "AgentTurnEnd", string(ev.ID), kit.AgentTurnEnd{Session: string(ev.ID)})
				})
			}
		}
	}))
	x.Add(h.Picker(x.addSource(h)))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	return nil
}

// follow moves the window onto session id, if it's still the one in front: a move asked for before another session
// came to the front, run after that one's, would take the window away from it.
func (x *Sessions) follow(id core.SessionID) {
	if x.focused() == id {
		kit.Follow(x.h, id)
	}
}

func (x *Sessions) commands() []registry.Command {
	h := x.h
	has := func(registry.Args) bool { return x.Host != nil }
	focused := func(registry.Args) bool { return has(nil) && x.focused() != "" }
	named := func(a registry.Args) bool { return has(nil) && (a.String("id") != "" || x.focused() != "") } // {id}, or the one in front
	planning := func(registry.Args) bool {
		se, ok := x.h.Session(x.focused())
		return ok && has(nil) && se.Snapshot().Planning()
	}
	return []registry.Command{
		{ID: "session.start", Title: "Start a session: {kind}, {prompt}, {repos}, {provider}, {model}, {effort}, {sandbox}…; {background} opens its tab behind the one in front; {then} is a list of {command, args} run once the window is on it",
			Available: has, Run: x.startCommand},
		{ID: "session.opens", Title: "Run session {id}'s kind's opening commands (KindSpec.Opens) its first time in front, however it got there, when the window shows nothing in its main area",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				se, ok := x.h.Session(core.SessionID(a.String("id")))
				if !ok || len(x.h.Layout().Shown()) > 0 {
					return nil
				}
				dir := kit.SessionDir(se.Snapshot())
				if w, ok := kit.WindowOf(x.h); ok {
					dir = w.Dir()
				}
				var last string
				if b, opened := x.h.Kept(se.ID(), openedKey); opened && json.Unmarshal(b, &last) == nil && last == dir {
					return nil
				}
				if err := x.h.Keep(se.ID(), openedKey, dir); err != nil {
					return err
				}
				k, ok := registry.KindOf(h, se.Snapshot().Kind)
				if !ok {
					return nil
				}
				var errs []error
				for _, o := range k.Spec().Opens {
					errs = append(errs, x.h.Run(ctx, o.Command, o.Args))
				}
				return errors.Join(errs...)
			}},
		{ID: "session.open", Title: "Bring a session to the front: {id}, resuming it if it's stopped or archived; {then} is a list of {command, args} run once the window is on it",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error {
				then, err := kit.CallsOf(a["then"])
				if err != nil {
					return err
				}
				return x.open(ctx, core.SessionID(a.String("id")), then)
			}},
		{ID: "session.open-dir", Title: "Bring the direct session on {dir} to the front: its own, with no agent, started if it hasn't one", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.openDirect(ctx, a.String("dir")) }},
		{ID: "session.resume", Title: "Resume a session: pick one", Available: has,
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "picker.open", registry.Args{"source": "sessions"})
			}},
		{ID: "session.stop", Title: "Stop the agent of {id}, or the session in front", Available: named,
			Run: x.slowSession("session.stop", "Stopping", func(ctx context.Context, w host.SessionCalls, id core.SessionID) error { return w.Stop(ctx, id) })},
		{ID: "session.archive", Title: "Archive {id}, or the session in front: its worktrees go, its threads and transcript stay", Available: named,
			Run: x.slowSession("session.archive", "Archiving", func(ctx context.Context, w host.SessionCalls, id core.SessionID) error { return w.Archive(ctx, id) })},
		{ID: "session.start-work", Title: "Start work on this plan: worktrees, then the agent implements it; {clear}, {fresh}, {branches}, {sandbox}, {network}, {model}, {effort}",
			Available: planning,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.slowSession("session.start-work", "Starting work on", func(ctx context.Context, w host.SessionCalls, id core.SessionID) error {
					return w.StartWork(ctx, startWorkReq(id, a))
				})(ctx, a)
			}},
		{ID: "session.configure", Title: "Switch the agent of {id}, or the session in front, to {model} at {effort}, its conversation kept; only while it's idle",
			Available: named,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.slowSession("session.configure", "Switching the agent of", func(ctx context.Context, w host.SessionCalls, id core.SessionID) error {
					return w.Configure(ctx, host.ConfigureReq{Session: id, Model: a.String("model"), Effort: a.String("effort")})
				})(ctx, a)
			}},
		{ID: "session.interrupt", Title: "Interrupt the agent's turn: {id}'s, or the session in front's", Available: named,
			Run: x.onSession(func(_ context.Context, w host.SessionCalls, id core.SessionID) error { return w.Interrupt(id) })},
		{ID: "session.rename", Title: "Rename {id}, or the session in front: {name}", Available: named,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.onSession(func(_ context.Context, w host.SessionCalls, id core.SessionID) error {
					return w.Rename(id, a.String("name"))
				})(ctx, a)
			}},
		{ID: "session.add-repo", Title: "Add a repo to the session in front: {repo}, by name or path", Available: focused,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return x.slowSession("session.add-repo", "Adding "+a.String("repo")+" to", func(ctx context.Context, w host.SessionCalls, id core.SessionID) error {
					return w.AddRepo(ctx, id, core.RepoID(a.String("repo")))
				})(ctx, a)
			}},
		{ID: "project.add", Title: "Add a project to the session in front: pick a registered repo, or type a checkout's path",
			When: core.AddProjects, Available: focused,
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "picker.open", registry.Args{"source": "projects.add"})
			}},
	}
}

func (x *Sessions) Deactivate(ctx context.Context) error {
	if x.cancel != nil {
		x.cancel()
	}
	return x.Disposer.Deactivate(ctx)
}

// loginNotices tell of logins that have stopped working, and the fix, and take back the notices of those that
// work again.
func (x *Sessions) loginNotices(signedOut, was []core.Login) {
	const source = "login"
	if len(signedOut) == 0 {
		for _, n := range x.h.State().Notifications {
			if n.ID == "agent.login" && strings.HasPrefix(n.Source, source) && !n.Dismissed {
				_ = x.h.Apply(core.Dismissal(n))
			}
		}
		return
	}
	for _, l := range signedOut {
		if slices.ContainsFunc(was, l.Same) {
			continue
		}
		n := core.Notice{ID: "agent.login", Severity: core.SevError, Source: source + ":" + l.Provider, Title: "Signed out",
			Text: services.ProviderTitle(x.h, l.Provider) + " needs you to log in: " + l.Detail + "."}
		switch {
		case l.Browser:
			n.Text += " Log in opens its sign-in in your browser."
			n.Actions = []core.Action{{Title: "Log in", Command: "agent.login", Args: map[string]any{"provider": l.Provider}}}
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
func (x *Sessions) host() (host.SessionCalls, error) {
	if x.Host == nil {
		return nil, errors.New("there are no sessions in this window")
	}
	return x.Host, nil
}

func (x *Sessions) focused() core.SessionID { return x.h.State().Focus }

// onSession runs fn on the session {id}, or the one in front.
func (x *Sessions) onSession(fn func(context.Context, host.SessionCalls, core.SessionID) error) func(registry.Ctx, registry.Args) error {
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
func (x *Sessions) slowSession(command, doing string, fn func(context.Context, host.SessionCalls, core.SessionID) error) func(registry.Ctx, registry.Args) error {
	return x.onSession(func(ctx context.Context, w host.SessionCalls, id core.SessionID) error {
		return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: command + "." + string(id), Doing: doing + " " + x.label(id)},
			func(ctx context.Context) (struct{}, error) { return struct{}{}, fn(ctx, w, id) }, nil)
	})
}

// label is how session id is named to the user.
func (x *Sessions) label(id core.SessionID) string {
	if se, ok := x.h.Session(id); ok {
		return kit.Label(se.Snapshot())
	}
	return string(id)
}

// addNewRepo is the sidebar picker's last item: the Add repo dialog, the repo it adds then added to the session.
const addNewRepo = "\x00repos.new"

// addSource is the picker + in the sidebar opens: the registered repos the session hasn't got, and the
// path typed. Choosing one adds it with session.add-repo, then opens it.
func (x *Sessions) addSource(h plugin.Host) uireg.PickerSource {
	return uireg.PickerSource{ID: "projects.add", Title: "Add a project", PickerOnly: true, Unlisted: true, Live: true, Source: "builtin:sessions",
		Items: func(_ registry.Ctx, query string) []uireg.PickerItem {
			se, ok := x.h.Session(x.focused())
			if !ok {
				return nil
			}
			var out []uireg.PickerItem
			if q := strings.TrimSpace(query); q != "" && (strings.ContainsRune(q, filepath.Separator) || strings.HasPrefix(q, "~")) {
				out = append(out, uireg.PickerItem{ID: q, Label: "Add " + q, Detail: "a checkout's path", Icon: "+"})
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
				out = append(out, uireg.PickerItem{ID: string(r.ID), Label: string(r.ID), Detail: kit.TildePath(r.Path)})
			}
			return append(out, uireg.PickerItem{ID: addNewRepo, Label: "Add a new repo…", Detail: "a checkout, or a clone", Icon: "+"})
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			id := x.focused()
			se, ok := x.h.Session(id)
			if !ok {
				return errors.New("no session is in front")
			}
			if it.ID == addNewRepo {
				return h.Run(ctx, "repos.new", registry.Args{"then": kit.Then(kit.Call{Command: "session.add-repo"})})
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
func (x *Sessions) addProject(id core.SessionID, repo string, before []core.Project) {
	h := x.h
	const notice = "projects.add"
	h.Notify(core.Notice{ID: notice, Severity: core.SevInfo, Source: "sessions", Title: "Add a project", Text: "Adding " + repo + " to the session"})
	added := func() (string, bool) {
		se, ok := x.h.Session(id)
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
	plugin.Go(h, "sessions: add "+repo, func() {
		arrived := make(chan struct{}, 1)
		stop := h.Subscribe(core.Filter{Session: id}, func(core.Event) {
			select {
			case arrived <- struct{}{}:
			default:
			}
		})
		defer stop.Dispose()
		err := plugin.Await(x.ctx, func(ctx registry.Ctx) error {
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
	})
}

// autoNameOption has a small model name the sessions started from a request.
const autoNameOption = "sessions.auto_name"

// restartOption starts an agent that exits on its own again.
const restartOption = "sessions.restart_on_exit"

func (x *Sessions) sendRestart() {
	if x.Host == nil {
		return
	}
	on, set := registry.Option[bool](x.h.Options(), restartOption, registry.At{})
	x.Host.SetRestartOnExit(on || !set) // on unless it's set false
}

// start starts a session and brings it to the front, unless {background}.
func (x *Sessions) start(ctx context.Context, a registry.Args) (core.SessionID, error) {
	then, err := kit.CallsOf(a["then"])
	if err != nil {
		return "", err
	}
	id, err := x.launch(ctx, a)
	if err != nil {
		return "", err
	}
	return id, x.front(id, a.Bool("background"), then)
}

// startCommand is session.start: the kind's Prepare and the host's start (a PR's lookup and checkout, worktrees)
// off the loop, then the session to the front.
func (x *Sessions) startCommand(ctx registry.Ctx, a registry.Args) error {
	then, err := kit.CallsOf(a["then"])
	if err != nil {
		return err
	}
	if _, err := x.host(); err != nil {
		return err
	}
	n := x.starts.Add(1)
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: fmt.Sprintf("session.start.%d", n), Doing: "Starting " + startName(a)},
		func(ctx context.Context) (core.SessionID, error) { return x.launch(ctx, a) },
		func(_ registry.Ctx, id core.SessionID) error { return x.front(id, a.Bool("background"), then) })
}

// startName is how session.start's a names the session to the user, before it has an id.
func startName(a registry.Args) string {
	if d := a.String("dir"); d != "" && a.String("name") == "" {
		return kit.TildePath(d)
	}
	return cmp.Or(a.String("name"), "a session")
}

// launch prepares and starts a session from a, and fires SessionStart on the loop.
func (x *Sessions) launch(ctx context.Context, a registry.Args) (core.SessionID, error) {
	spec := kit.SpecFrom(a)
	if on, _ := registry.Option[bool](x.h.Options(), autoNameOption, registry.At{}); on && spec.Name == "" {
		spec.AutoName = true
	}
	w, err := x.host()
	if err != nil {
		return "", err
	}
	if k, ok := registry.KindOf(x.h, spec.Kind); ok {
		if err := registry.Call(func() error { return k.Prepare(ctx, &spec) }); err != nil {
			return "", err
		}
	}
	id, err := w.Start(ctx, spec)
	if err != nil {
		return "", err
	}
	ev := kit.SessionStart{Session: string(id), Kind: string(spec.Kind), Ticket: spec.Ticket, Prompt: spec.Prompt}
	x.h.Do(func() { x.h.Fire(context.Background(), "SessionStart", string(id), &ev) })
	return id, nil
}

// front brings a started session to the front, or in the background opens its tab behind the one in front, and
// runs then once the window is on it.
func (x *Sessions) front(id core.SessionID, background bool, then []kit.Call) error {
	if background {
		return x.h.Apply(core.TabOpened{ID: id})
	}
	return kit.Focus(x.h, id, then)
}

// open brings a session to the front, resuming it if it's stopped, and runs then once the window is on it: after it
// opens again on the session's directory, if it does.
func (x *Sessions) open(ctx context.Context, id core.SessionID, then []kit.Call) error {
	se, ok := x.h.Session(id)
	if !ok {
		return fmt.Errorf("no session %q", id)
	}
	front := func() error { return kit.Focus(x.h, id, then) }
	if st := se.Snapshot(); st.Archived || (st.Agent.Provider != "" && !st.Agent.Running) {
		w, err := x.host()
		if err != nil {
			return err
		}
		return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "session.open." + string(id), Doing: "Resuming " + kit.Label(st)},
			func(ctx context.Context) (struct{}, error) { return struct{}{}, w.Resume(ctx, id) },
			func(registry.Ctx, struct{}) error { return front() })
	}
	return front()
}

// openDirect brings dir's direct session with no agent to the front, starting one if it hasn't one.
func (x *Sessions) openDirect(ctx registry.Ctx, dir string) error {
	for _, se := range x.h.State().Sessions {
		if se.InPlace() && se.Agent.Provider == "" && filepath.Clean(se.Dir) == filepath.Clean(dir) {
			return x.open(ctx, se.ID, nil)
		}
	}
	return x.startCommand(ctx, registry.Args{"kind": string(core.KindDirect), "dir": dir})
}

// readOnly says why a file opens read-only in the session the window is on: it's outside the session's
// projects and directory, or in a project the session only reads.
func (x *Sessions) readOnly(path string) string {
	se, ok := x.h.Session(x.focused())
	if !ok || path == "" {
		return ""
	}
	st := se.Snapshot()
	work := ""
	if w, ok := kit.WindowOf(x.h); ok {
		work = w.Dir()
	}
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
	if !slices.ContainsFunc(roots, func(r root) bool { return kit.Within(work, r.dir) }) {
		return "" // the window isn't on this session
	}
	best := -1
	for i, r := range roots {
		if kit.Within(path, r.dir) && (best < 0 || len(r.dir) > len(roots[best].dir)) {
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

// agentTools are the tools plugins offer an agent, as the host hands them on.
func (x *Sessions) agentTools(h plugin.Host, r host.ToolsReq) []agent.Tool {
	var out []agent.Tool
	name, utility := x.agentName(r.Session, r.Agent)
	for _, t := range registry.AgentToolsFor(h, name, utility) {
		out = append(out, agent.Tool{Name: t.Name, Description: t.Desc, Schema: t.Schema(), ReadOnly: t.ReadOnly && t.Confirm == nil,
			CoveredBy: t.CoveredBy})
	}
	return out
}

// agentName is the agent the host asks for: a session's, by its kind, or a utility agent with no session.
func (x *Sessions) agentName(id core.SessionID, agent string) (string, bool) {
	if id == "" {
		return agent, true
	}
	if agent == "" {
		if se, ok := x.h.Session(id); ok {
			agent = string(se.Kind())
		}
	}
	return agent, false
}

// callTool runs a tool for an agent. A session's agent is owned by the host: an act there is asked through the
// session's rules, the policy hooks and then the user's card, as the agent's own gate is.
func (x *Sessions) callTool(ctx context.Context, h plugin.Host, w host.SessionCalls, call host.ToolCall) (string, error) {
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
func askForSession(ctx context.Context, w host.SessionCalls, call host.ToolCall, cf registry.Confirmation) (bool, error) {
	tool := "mcp__oriel__" + call.Tool
	v, err := w.Ask(ctx, host.AskReq{Session: call.Session, Ask: core.Ask{Kind: core.AskPermission, Tool: tool,
		Summary: cmp.Or(cf.Line, cf.Title), Detail: cf.Lead, Input: string(call.Args), Rule: tool}})
	if err != nil {
		return false, err
	}
	if v.Decision == host.DecisionAllow {
		return true, nil
	}
	if v.Note != "" {
		return false, fmt.Errorf("denied by %s: %s", cmp.Or(v.By, "the user"), v.Note)
	}
	return false, fmt.Errorf("denied by %s, so nothing was done", cmp.Or(v.By, "the user"))
}

// decide runs the Permission hooks on an ask: layer 2, before it reaches the user.
func (x *Sessions) decide(ctx context.Context, h plugin.Host, d host.Decide) host.Verdict {
	ev := registry.PermissionEvent{Session: string(d.Session), Kind: string(d.Ask.Kind), Tool: d.Ask.Tool, Summary: d.Ask.Summary,
		Input: kit.MainInput(d.Ask.Input), Rule: d.Ask.Rule, Host: d.Ask.Host}
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
		return host.Verdict{Decision: host.DecisionDeny, Note: v.Reason, By: "a Permission hook"}
	case !v.Decided:
		return host.Verdict{}
	}
	return hookVerdict(v.Value)
}

// hookVerdict reads a Permission hook's answer, in any of the shapes it may give ("allow", true, a
// registry.PermissionVerdict, a {decision, note} table), as allow or deny; anything else leaves the ask to the user.
func hookVerdict(answer any) host.Verdict {
	var dec, note string
	switch val := answer.(type) {
	case string:
		dec = val
	case bool:
		dec = map[bool]string{true: "allow", false: "deny"}[val]
	case registry.PermissionVerdict:
		dec, note = val.Decision, val.Note
	case map[string]any:
		dec, _ = val["decision"].(string)
		note, _ = val["note"].(string)
	}
	if d := host.Decision(dec); d.Decided() {
		return host.Verdict{Decision: d, Note: note, By: "a Permission hook"}
	}
	return host.Verdict{}
}

// explain answers "ask to explain" from a tool-less fork of the session in front: it knows why the code is as
// it is, and changes nothing.
func (x *Sessions) explain(ctx context.Context, r registry.ExplainRequest) (string, error) {
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
	b.WriteString(kit.RenderPrompt(x.ConfigDir, "explain.request", map[string]string{"kind": r.Kind, "how": how}))
	fmt.Fprintf(&b, "\n\n%s\n\n%s", r.Subject, r.Context)
	if len(r.Files) > 0 {
		fmt.Fprintf(&b, "\n\nFiles: %s", strings.Join(r.Files, ", "))
	}
	if r.Format != "" {
		b.WriteString("\n\n" + r.Format)
	}
	return w.Fork(ctx, id, b.String())
}

// explainer is who answers explain: the session in front's agent, forked.
func (x *Sessions) explainer() string {
	se, ok := x.h.Session(x.focused())
	if !ok {
		return "no session in front"
	}
	return "fork of " + cmp.Or(se.Snapshot().Agent.Provider, "the agent") + " · knows this session"
}

// explainerByline is who wrote a long answer, such as a walkthrough: the agent in front, from what it knows.
func (x *Sessions) explainerByline() string {
	se, ok := x.h.Session(x.focused())
	if !ok {
		return "written by the agent"
	}
	return "written by " + cmp.Or(se.Snapshot().Agent.Provider, "the agent") + " from the session's own context"
}

// fork answers a side question from a tool-less fork of the session's agent.
func (x *Sessions) fork(ctx context.Context, r registry.ForkRequest) (string, error) {
	w, err := x.host()
	if err != nil {
		return "", err
	}
	return w.Fork(ctx, core.SessionID(r.Session), r.Prompt)
}

// forker is who answers a session's side questions: "claude · fork, no tools"; "" for an agent whose provider
// says it can't fork without tools (agent.Features), which answers none.
func (x *Sessions) forker(id string) string {
	se, ok := x.h.Session(core.SessionID(id))
	if !ok {
		return ""
	}
	p := se.Snapshot().Agent.Provider
	if f, known := agentkit.ProviderFeatures(x.h, p); known && !f.Fork {
		return ""
	}
	return cmp.Or(p, "the agent") + " · fork, no tools"
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
