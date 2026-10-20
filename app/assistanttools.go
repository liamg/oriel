package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/assistant"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/testrun"
)

// The assistant's own tools (crit-reference.md, "Assistant"), offered only to it. Reading tools read; the ones that
// show something just do it; those that start, stop or run an agent are acts, which wait for the user's yes on a
// card. Whatever can be undone says how. my_tickets, mission_control and request_review are the tools of the
// features that own that data (tickets, mission control, agentbus): they offer them for "assistant" the same way.

var replySchema = json.RawMessage(`{"type":"object","properties":{` +
	`"text":{"type":"string","description":"your answer, in markdown"},` +
	`"expects_reply":{"type":"boolean","description":"true only when you asked them a direct question"},` +
	`"items":{"type":"array","description":"things they can act on, in order: they pick one with a number key","items":{"type":"object","properties":{` +
	`"title":{"type":"string","description":"a few words: the session's name, review #4410"},` +
	`"text":{"type":"string","description":"what it's about, in a line"},` +
	`"meta":{"type":"string","description":"how long ago, and where: 22m · devbox"},` +
	`"session":{"type":"string","description":"a session's id: picking it opens it"},` +
	`"url":{"type":"string","description":"a link: picking it opens it"},` +
	`"file":{"type":"string","description":"a file, as changed_files gives it: picking it opens it, as focus_file does"},` +
	`"line":{"type":"number","description":"the file's line to open at"},` +
	`"project":{"type":"string","description":"the file's project"}},"required":["title"]}}},"required":["text"]}`)

func (x *assistantPlugin) tools() []registry.AgentTool {
	id := map[string]string{"id": "string"}
	return []registry.AgentTool{
		{Name: "reply", InputSchema: replySchema, Run: x.toolReply,
			Desc: "Your answer for this turn, said aloud in a spoken turn. Call exactly once, last. items are rows the user can pick."},
		{Name: "sessions", ReadOnly: true, Reads: "sessions", Run: x.toolSessions,
			Desc: "Every open Oriel session: id, name, kind, status (needs_you, permission, working, quiet…) and why, repos, branch, prompt, ticket, pull request, unfinished tasks and what it asks."},
		{Name: "past_sessions", ReadOnly: true, Reads: "sessions", Run: x.toolPastSessions,
			Desc: "Finished sessions that can be resumed, newest first: id, name, kind, repos, ticket, pull request."},
		{Name: "open_session", Params: id, Run: x.toolOpenSession, Desc: "Bring a session to the front. id is from sessions."},
		{Name: "rename_session", Params: map[string]string{"id": "string", "name": "string"}, Run: x.toolRenameSession,
			Desc: "Give a session a new name. id is from sessions."},
		{Name: "resume_session", Params: id, Run: x.toolResumeSession,
			Desc: "Bring a finished or stopped session back and open it. id is from past_sessions."},
		{Name: "new_session", Params: map[string]string{"kind": "string?", "prompt": "string?", "repos": "string?"}, Run: x.toolNewSession,
			Desc: "Open the new-session launcher for the user to finish, with a kind (work, plan, review, job), prompt and repos (names, comma separated) filled in."},
		{Name: "start_session", Params: map[string]string{"repo": "string", "prompt": "string", "kind": "string?"},
			Confirm: x.confirmStart, Run: x.toolStart,
			Desc: "Start a session: kind plan (the default) drafts a plan first, work starts at once on a fresh branch. repo is a name from repos. Check sessions first: when one is already on that ticket or work, open it instead."},
		{Name: "stop_session", Params: id, Confirm: x.confirmStop, Run: x.toolStop,
			Desc: "Stop a session's agent, only when the user asked to. Its workspace stays and it can be resumed. id is from sessions."},
		{Name: "review_pr", Params: map[string]string{"pr": "string"}, Confirm: x.confirmReview, Run: x.toolReview,
			Desc: "Start a review session on any pull request, by URL or owner/name#N: one the user was sent, not only those on their lists. Check sessions first: when one is already on that PR, open it instead."},
		{Name: "jobs", ReadOnly: true, Reads: "jobs", Run: x.toolJobs,
			Desc: "The user's saved jobs (prompts run on demand or on a schedule): name, title, schedule, and how the last run went."},
		{Name: "run_job", Params: map[string]string{"name": "string"}, Confirm: x.confirmRunJob, Run: x.toolRunJob,
			Desc: "Run a saved job now, in a fresh workspace. name is from jobs."},
		{Name: "repos", ReadOnly: true, Reads: "repos", Run: x.toolRepos,
			Desc: "The registered repositories: name, path and remote."},
		{Name: "my_prs", ReadOnly: true, Reads: "github", Run: x.toolPRs(gh.SearchMine),
			Desc: "The user's open pull requests across GitHub: number, title, repo, url, draft, when last updated, and the session on it if there is one."},
		{Name: "usage", ReadOnly: true, Reads: "usage", Run: x.toolUsage,
			Desc: "The user's plan usage for each agent and account: how much of each limit window is used and when it resets."},
		{Name: "where", ReadOnly: true, Reads: "editor", Run: x.toolWhere,
			Desc: "What's in front of the user now: the session, its projects and the open one, and the file and line the editor is on, relative to its project. Read it for \"this file\", \"here\"."},
		{Name: "changed_files", ReadOnly: true, Reads: "git", Params: map[string]string{"session": "string?", "project": "string?", "tests": "boolean?", "limit": "number?"},
			Run:  x.toolChangedFiles,
			Desc: "The files a session changed, on its branch or not committed yet, across its projects (or one), the most recently modified first: project, path, area, lines added and removed, how long ago it was modified, and whether it's a test. tests keeps only the tests. session is from sessions; the one in front by default."},
		{Name: "open_project", Params: map[string]string{"project": "string", "session": "string?"}, Run: x.toolOpenProject,
			Desc: "Make a project of a session the open one, bringing the session to the front first. session is from sessions; the one in front by default."},
		{Name: "focus_file", Params: map[string]string{"path": "string", "line": "number?", "project": "string?", "session": "string?"}, Run: x.toolFocusFile,
			Desc: "Show a file in the editor, at a line, bringing its session and project to the front first. path is relative to the project (as changed_files gives it); the open project by default. Your window stays open, so you can ask whether it's the one."},
		{Name: "settings", Params: map[string]string{"section": "string?", "query": "string?"}, Run: x.toolSettings,
			Desc: "Open Oriel's settings page, at a section (editor, git, voice, assistant…) or searching for a setting."},
	}
}

func pstr(p map[string]any, k string) string {
	switch v := p[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// run runs a command on the app's loop, as a key would.
func (x *assistantPlugin) run(ctx context.Context, id string, args registry.Args) error {
	var err error
	if lerr := onLoop(ctx, x.h, func() { err = x.h.Run(ctx, id, args) }); lerr != nil {
		return lerr
	}
	return err
}

func (x *assistantPlugin) toolReply(_ registry.Ctx, p map[string]any) (any, error) {
	text := pstr(p, "text")
	if text == "" {
		return nil, errors.New("the reply is empty")
	}
	var items []assistant.Item
	if raw, err := json.Marshal(p["items"]); err == nil {
		_ = json.Unmarshal(raw, &items)
	}
	st := x.h.State()
	for i, it := range items {
		if se, ok := st.Session(core.SessionID(it.Session)); ok {
			items[i].Status = string(se.Status())
		}
	}
	expects, _ := p["expects_reply"].(bool)
	x.conv.Reply(text, expects, items)
	return "shown", nil
}

// sessionLine is a session as the assistant reads it.
type sessionLine struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Kind    string   `json:"kind"`
	Status  string   `json:"status"`
	Why     string   `json:"why,omitempty"`
	Repos   []string `json:"repos,omitempty"`
	Branch  string   `json:"branch,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Ticket  string   `json:"ticket,omitempty"`
	PR      string   `json:"pr,omitempty"`
	Tasks   []string `json:"tasks,omitempty"`
	Asks    []string `json:"asks,omitempty"`
	Stopped bool     `json:"stopped,omitempty"`
	Since   string   `json:"since,omitempty"` // a finished one's
}

func clipLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func (x *assistantPlugin) lineOf(st core.State, s core.SessionState, now time.Time) sessionLine {
	l := sessionLine{ID: string(s.ID), Name: s.Name, Kind: string(s.Kind), Status: string(s.Status), Why: why(s, now),
		Branch: s.Branch, Prompt: clipLine(s.Prompt, 200), Ticket: s.Ticket, PR: s.PR,
		Stopped: s.Agent.Provider != "" && !s.Agent.Live()}
	for _, p := range s.Projects {
		l.Repos = append(l.Repos, p.Name)
	}
	for _, t := range s.Tasks {
		if t.Status != core.TaskCompleted {
			l.Tasks = append(l.Tasks, t.Subject)
		}
	}
	for _, a := range s.Asks {
		l.Asks = append(l.Asks, strings.TrimSpace(a.Tool+" "+firstLine(cmpOr(a.Summary, a.Host))))
	}
	return l
}

func (x *assistantPlugin) toolSessions(registry.Ctx, map[string]any) (any, error) {
	st := x.h.State()
	now := time.Now()
	var ss []core.SessionState
	for _, s := range st.Sessions {
		if !s.Archived {
			ss = append(ss, s)
		}
	}
	slices.SortStableFunc(ss, func(a, b core.SessionState) int {
		return cmp.Or(slices.Index(statusOrder, a.Status)-slices.Index(statusOrder, b.Status), a.Created.Compare(b.Created))
	})
	out := []sessionLine{}
	for _, s := range ss {
		out = append(out, x.lineOf(st, s, now))
	}
	return out, nil
}

// finished says whether a session can be resumed: archived, or its agent stopped.
func finished(s core.SessionState) bool {
	return s.Archived || s.Agent.Provider != "" && !s.Agent.Live()
}

func (x *assistantPlugin) toolPastSessions(registry.Ctx, map[string]any) (any, error) {
	st := x.h.State()
	var ss []core.SessionState
	for _, s := range st.Sessions {
		if finished(s) {
			ss = append(ss, s)
		}
	}
	at := func(s core.SessionState) time.Time {
		if !s.Agent.StoppedAt.IsZero() {
			return s.Agent.StoppedAt
		}
		return s.Created
	}
	slices.SortStableFunc(ss, func(a, b core.SessionState) int { return at(b).Compare(at(a)) })
	out := []sessionLine{}
	for _, s := range ss[:min(len(ss), 30)] {
		l := x.lineOf(st, s, time.Now())
		l.Status, l.Why, l.Since = "", "", at(s).Format(time.RFC3339)
		out = append(out, l)
	}
	return out, nil
}

// session finds a session by its id, or by its name.
func (x *assistantPlugin) session(ref string) (core.SessionState, error) {
	st := x.h.State()
	if s, ok := st.Session(core.SessionID(ref)); ok {
		return s.Snapshot(), nil
	}
	var hits []core.SessionState
	for _, s := range st.Sessions {
		if ref != "" && strings.EqualFold(s.Name, ref) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return core.SessionState{}, fmt.Errorf("no session %q: read sessions again", ref)
	case 1:
		return hits[0], nil
	}
	return core.SessionState{}, fmt.Errorf("more than one session is called %q: ask which, by id", ref)
}

func label(s core.SessionState) string { return cmpOr(s.Name, string(s.ID)) }

func (x *assistantPlugin) toolOpenSession(ctx registry.Ctx, p map[string]any) (any, error) {
	s, err := x.session(pstr(p, "id"))
	if err != nil {
		return nil, err
	}
	if err := x.run(ctx, "session.open", registry.Args{"id": string(s.ID)}); err != nil {
		return nil, err
	}
	return registry.Acted{Result: "opened", Line: "opened " + label(s)}, nil
}

func (x *assistantPlugin) toolRenameSession(ctx registry.Ctx, p map[string]any) (any, error) {
	s, err := x.session(pstr(p, "id"))
	if err != nil {
		return nil, err
	}
	name := pstr(p, "name")
	if name == "" {
		return nil, errors.New("what should it be called?")
	}
	if err := x.run(ctx, "session.rename", registry.Args{"id": string(s.ID), "name": name}); err != nil {
		return nil, err
	}
	old := s.Name
	return registry.Acted{Result: "renamed", Line: "renamed " + label(s) + " to " + name, Undo: func(ctx registry.Ctx) error {
		return x.run(ctx, "session.rename", registry.Args{"id": string(s.ID), "name": old})
	}}, nil
}

func (x *assistantPlugin) toolResumeSession(ctx registry.Ctx, p map[string]any) (any, error) {
	s, err := x.session(pstr(p, "id"))
	if err != nil {
		return nil, err
	}
	if !finished(s) {
		return nil, fmt.Errorf("%s is running already: open_session opens it", label(s))
	}
	if err := x.run(ctx, "session.open", registry.Args{"id": string(s.ID)}); err != nil {
		return nil, err
	}
	return registry.Acted{Result: "resumed; it opens when it's up", Line: "resumed " + label(s), Undo: func(ctx registry.Ctx) error {
		return x.run(ctx, "session.stop", registry.Args{"id": string(s.ID)})
	}}, nil
}

func (x *assistantPlugin) toolNewSession(ctx registry.Ctx, p map[string]any) (any, error) {
	args := registry.Args{}
	for _, k := range []string{"kind", "prompt", "repos"} {
		if v := pstr(p, k); v != "" {
			args[k] = v
		}
	}
	if err := x.run(ctx, "session.new", args); err != nil {
		return nil, err
	}
	return registry.Acted{Result: "the launcher is open for the user to finish", Line: "opened the launcher"}, nil
}

func (x *assistantPlugin) toolSettings(ctx registry.Ctx, p map[string]any) (any, error) {
	if err := x.run(ctx, "settings.open", registry.Args{"section": pstr(p, "section"), "query": pstr(p, "query")}); err != nil {
		return nil, err
	}
	return registry.Acted{Result: "opened", Line: "opened settings"}, nil
}

// repoLine is a registered repo as the assistant reads it.
type repoLine struct {
	ID     core.RepoID `json:"id"`
	Name   string      `json:"name"`
	Path   string      `json:"path"`
	Remote string      `json:"remote,omitempty"`
}

func (x *assistantPlugin) repos(context.Context) []repoLine {
	w, err := x.sh.sessions.host()
	if err != nil {
		return nil
	}
	var out []repoLine
	for _, r := range w.Repos() {
		out = append(out, repoLine{ID: r.ID, Name: filepath.Base(r.Path), Path: r.Path, Remote: r.Remote})
	}
	slices.SortFunc(out, func(a, b repoLine) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Path, b.Path))
	})
	return out
}

func (x *assistantPlugin) toolRepos(ctx registry.Ctx, _ map[string]any) (any, error) {
	out := x.repos(ctx)
	if out == nil {
		out = []repoLine{}
	}
	return out, nil
}

// repo finds a registered repo by name or path.
func (x *assistantPlugin) repo(ctx context.Context, name string) (repoLine, error) {
	var hits []repoLine
	for _, r := range x.repos(ctx) {
		if strings.EqualFold(r.Name, name) || r.Path == name {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return repoLine{}, fmt.Errorf("no repository called %s: read repos", name)
	case 1:
		return hits[0], nil
	}
	var where []string
	for _, h := range hits {
		where = append(where, h.Path)
	}
	return repoLine{}, fmt.Errorf("%s is ambiguous: %s. Ask which", name, strings.Join(where, "; "))
}

var startNotes = map[string]string{
	"plan": "A plan session drafts a plan first; no work starts until you press Start work.",
	"work": "A work session starts at once, on a fresh branch from the updated base.",
}

type startReq struct {
	kind, prompt string
	repo         repoLine
}

func (x *assistantPlugin) startReq(ctx context.Context, p map[string]any) (startReq, error) {
	r := startReq{kind: cmpOr(pstr(p, "kind"), "plan"), prompt: pstr(p, "prompt")}
	if startNotes[r.kind] == "" {
		return r, fmt.Errorf("kind is plan or work, not %q", r.kind)
	}
	if r.prompt == "" {
		return r, errors.New("what should the session do? prompt is empty")
	}
	var err error
	r.repo, err = x.repo(ctx, pstr(p, "repo"))
	return r, err
}

func (x *assistantPlugin) confirmStart(ctx registry.Ctx, p map[string]any) (registry.Confirmation, error) {
	r, err := x.startReq(ctx, p)
	if err != nil {
		return registry.Confirmation{}, err
	}
	return registry.Confirmation{Title: "Start a " + r.kind + " session",
		Lead:  fmt.Sprintf("The assistant wants to start a %s session in %s.", r.kind, r.repo.Name),
		Rows:  []registry.ConfirmRow{{Key: "repository", Value: r.repo.Name + " — " + r.repo.Path}},
		Block: &registry.ConfirmBlock{Label: "The prompt the agent will be given", Text: r.prompt}, Note: startNotes[r.kind],
		Line: "start a " + r.kind + " session in " + r.repo.Name}, nil
}

func (x *assistantPlugin) toolStart(ctx registry.Ctx, p map[string]any) (any, error) {
	r, err := x.startReq(ctx, p)
	if err != nil {
		return nil, err
	}
	id, err := x.sh.sessions.start(ctx, registry.Args{"kind": r.kind, "prompt": r.prompt, "repos": []string{string(r.repo.ID)},
		"background": true})
	if err != nil {
		return nil, err
	}
	return x.started(id, "started a "+r.kind+" session in "+r.repo.Name, r.repo.Name+" · "+r.kind), nil
}

// started is a session an act made: it opens from the card, and undoing it deletes it.
func (x *assistantPlugin) started(id core.SessionID, line, detail string) registry.Acted {
	return registry.Acted{Result: map[string]any{"session": id, "done": line}, Line: line, Detail: detail,
		Open: "session.open", OpenArgs: registry.Args{"id": string(id)},
		Undo: func(ctx registry.Ctx) error { return x.run(ctx, "session.delete", registry.Args{"id": string(id)}) }}
}

func (x *assistantPlugin) confirmStop(_ registry.Ctx, p map[string]any) (registry.Confirmation, error) {
	s, err := x.session(pstr(p, "id"))
	if err != nil {
		return registry.Confirmation{}, err
	}
	l := x.lineOf(x.h.State(), s, time.Now())
	c := registry.Confirmation{Title: "Stop a session", Lead: "The assistant wants to stop this session and its agent:",
		Rows: []registry.ConfirmRow{{Key: "session", Value: label(s)}, {Key: "repositories", Value: strings.Join(l.Repos, ", ")},
			{Key: "ticket", Value: s.Ticket}},
		Block: &registry.ConfirmBlock{Label: "Its unfinished tasks", Items: l.Tasks},
		Note:  "Its workspace stays, and the session can be resumed later.", Line: "stop " + label(s)}
	if len(l.Tasks) == 0 {
		c.Block.Text = "none"
	}
	return c, nil
}

func (x *assistantPlugin) toolStop(ctx registry.Ctx, p map[string]any) (any, error) {
	s, err := x.session(pstr(p, "id"))
	if err != nil {
		return nil, err
	}
	if err := x.run(ctx, "session.stop", registry.Args{"id": string(s.ID)}); err != nil {
		return nil, err
	}
	return registry.Acted{Result: "stopped", Line: "stopped " + label(s), Undo: func(ctx registry.Ctx) error {
		c, err := x.sh.sessions.host()
		if err != nil {
			return err
		}
		return c.Resume(ctx, s.ID)
	}}, nil
}

// pullRequest is a PR named by URL or owner/name#N, as its URL and owner/name#N.
func pullRequest(ref string) (url, key string, err error) {
	r, n, err := gh.ParsePR(ref)
	if err != nil {
		return "", "", err
	}
	return gh.PRURL(r, n), fmt.Sprintf("%s#%d", r, n), nil
}

// reviewOf is the open session already on a pull request.
func (x *assistantPlugin) reviewOf(url string) (core.SessionState, bool) {
	for _, s := range x.h.State().Sessions {
		if !s.Archived && strings.EqualFold(strings.TrimSuffix(s.PR, "/"), url) {
			return s, true
		}
	}
	return core.SessionState{}, false
}

func (x *assistantPlugin) confirmReview(_ registry.Ctx, p map[string]any) (registry.Confirmation, error) {
	url, key, err := pullRequest(pstr(p, "pr"))
	if err != nil {
		return registry.Confirmation{}, err
	}
	repo, num, _ := strings.Cut(key, "#")
	c := registry.Confirmation{Title: "Start a review", Lead: "The assistant wants to start a review of this pull request:",
		Rows: []registry.ConfirmRow{{Key: "repository", Value: repo}, {Key: "number", Value: "#" + num}, {Key: "link", Value: url, URL: url}},
		Note: "A review session checks out its head read-only, and an AI review starts as it opens.", Line: "start a review of " + key}
	if s, ok := x.reviewOf(url); ok {
		c.Title, c.Lead = "Open the review", "This pull request has a session already ("+label(s)+"): the assistant wants to open it."
		c.Note, c.Line = "", "open the review of "+key
	}
	return c, nil
}

func (x *assistantPlugin) toolReview(ctx registry.Ctx, p map[string]any) (any, error) {
	url, key, err := pullRequest(pstr(p, "pr"))
	if err != nil {
		return nil, err
	}
	if s, ok := x.reviewOf(url); ok {
		if err := x.run(ctx, "session.open", registry.Args{"id": string(s.ID)}); err != nil {
			return nil, err
		}
		return registry.Acted{Result: "it had a session already: opened " + label(s), Line: "opened the review of " + key}, nil
	}
	id, err := x.sh.sessions.start(ctx, registry.Args{"kind": string(core.KindReview), "pr": url, "background": true})
	if err != nil {
		return nil, err
	}
	return x.started(id, "started a review of "+key, "review · AI review starting"), nil
}

// jobLine is a saved job as the assistant reads it.
type jobLine struct {
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	Running  bool   `json:"running,omitempty"`
	LastRun  string `json:"lastRun,omitempty"` // done, blocked, stopped
	Summary  string `json:"summary,omitempty"`
	LastAt   string `json:"lastAt,omitempty"`

	at time.Time
}

func (x *assistantPlugin) jobs(context.Context) []jobLine {
	w, err := x.sh.sessions.host()
	if err != nil {
		return nil
	}
	st := x.h.State()
	var out []jobLine
	for _, j := range w.Jobs() {
		l := jobLine{Name: j.Name, Title: j.Title}
		if j.Schedule.Every != "" {
			l.Schedule = j.Schedule.String()
		}
		for _, s := range st.Sessions {
			if s.Job != j.Name || s.Created.Before(l.at) {
				continue
			}
			r := host.RunOf(s)
			l.at, l.LastRun, l.Summary, l.Running = s.Created, r.Status, clipLine(r.Summary, 300), r.Status == host.RunRunning
			l.LastAt = s.Created.Format(time.RFC3339)
		}
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b jobLine) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (x *assistantPlugin) toolJobs(ctx registry.Ctx, _ map[string]any) (any, error) {
	out := x.jobs(ctx)
	if out == nil {
		out = []jobLine{}
	}
	return out, nil
}

func (x *assistantPlugin) job(ctx context.Context, p map[string]any) (jobLine, error) {
	name := pstr(p, "name")
	var hits []jobLine
	for _, j := range x.jobs(ctx) {
		if j.Name == name || strings.EqualFold(j.Title, name) {
			hits = append(hits, j)
		}
	}
	switch len(hits) {
	case 0:
		return jobLine{}, fmt.Errorf("no job called %s: read jobs", name)
	case 1:
		return hits[0], nil
	}
	return jobLine{}, fmt.Errorf("more than one job is called %s: ask which", name)
}

func (x *assistantPlugin) confirmRunJob(ctx registry.Ctx, p map[string]any) (registry.Confirmation, error) {
	j, err := x.job(ctx, p)
	if err != nil {
		return registry.Confirmation{}, err
	}
	title := cmpOr(j.Title, j.Name)
	last := "never run"
	if j.LastRun != "" {
		last = j.LastRun + ", " + ago(time.Now(), j.at) + " ago"
	}
	c := registry.Confirmation{Title: "Run a job", Lead: "The assistant wants to run this saved job now, in a fresh workspace:",
		Rows: []registry.ConfirmRow{{Key: "job", Value: title}, {Key: "last run", Value: last}},
		Line: "run " + title}
	if j.Summary != "" {
		c.Block = &registry.ConfirmBlock{Label: "What the last run said", Text: j.Summary}
	}
	if j.Running {
		c.Note = "A run of this job is still going."
	}
	return c, nil
}

func (x *assistantPlugin) toolRunJob(ctx registry.Ctx, p map[string]any) (any, error) {
	j, err := x.job(ctx, p)
	if err != nil {
		return nil, err
	}
	c, err := x.sh.sessions.host()
	if err != nil {
		return nil, err
	}
	id, err := c.RunJob(ctx, j.Name)
	if err != nil {
		return nil, err
	}
	return x.started(id, "running "+cmpOr(j.Title, j.Name), "job"), nil
}

// prLine is a pull request as the assistant reads it.
type prLine struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Repo    string `json:"repo"`
	URL     string `json:"url"`
	Author  string `json:"author,omitempty"`
	Draft   bool   `json:"draft,omitempty"`
	Updated string `json:"updated,omitempty"`
	Session string `json:"session,omitempty"` // the open session on it
}

func (x *assistantPlugin) toolPRs(query string) func(registry.Ctx, map[string]any) (any, error) {
	return func(ctx registry.Ctx, _ map[string]any) (any, error) {
		c, err := x.sh.sessions.host()
		if err != nil {
			return nil, err
		}
		found, err := c.SearchPRs(ctx, host.PRsReq{Query: query})
		if err != nil {
			return nil, err
		}
		out := []prLine{}
		for _, f := range found {
			l := prLine{Number: f.Number, Title: f.Title, Repo: f.Repo, URL: f.URL, Author: f.Author, Draft: f.Draft}
			if !f.Updated.IsZero() {
				l.Updated = f.Updated.Format(time.RFC3339)
			}
			if s, ok := x.reviewOf(f.URL); ok {
				l.Session = string(s.ID)
			}
			out = append(out, l)
		}
		return out, nil
	}
}

func (x *assistantPlugin) toolUsage(registry.Ctx, map[string]any) (any, error) {
	type window struct {
		Name   string `json:"name"`
		Used   string `json:"used"`
		Resets string `json:"resets,omitempty"`
	}
	type plan struct {
		Provider string   `json:"provider"`
		Account  string   `json:"account,omitempty"`
		Plan     string   `json:"plan,omitempty"`
		Windows  []window `json:"windows"`
		Read     string   `json:"read,omitempty"`
	}
	out := []plan{}
	for _, u := range x.h.State().Usage {
		p := plan{Provider: u.Provider, Account: u.AccountName, Plan: u.Plan, Windows: []window{}}
		if !u.Sampled.IsZero() {
			p.Read = u.Sampled.Format(time.RFC3339)
		}
		for _, w := range u.Windows {
			ww := window{Name: w.Name, Used: fmt.Sprintf("%.0f%%", w.Used*100)}
			if !w.Resets.IsZero() {
				ww.Resets = w.Resets.Format(time.RFC3339)
			}
			p.Windows = append(p.Windows, ww)
		}
		out = append(out, p)
	}
	return map[string]any{"now": time.Now().Format(time.RFC3339), "plans": out}, nil
}

func pint(p map[string]any, k string) int {
	switch v := p[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// whereLine is what's in front of the user, as the assistant reads it.
type whereLine struct {
	Session  string        `json:"session,omitempty"`
	Name     string        `json:"name,omitempty"`
	Project  string        `json:"project,omitempty"` // the open one
	Projects []projectLine `json:"projects,omitempty"`
	File     string        `json:"file,omitempty"` // relative to its project, else absolute
	FileIn   string        `json:"file_project,omitempty"`
	Line     int           `json:"line,omitempty"`
}

type projectLine struct {
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
	Dir    string `json:"dir"`
}

func (x *assistantPlugin) toolWhere(ctx registry.Ctx, _ map[string]any) (any, error) {
	var at registry.At
	var w here
	if err := onLoop(ctx, x.h, func() { at, w = x.h.Here(), projectsHere(x.h) }); err != nil {
		return nil, err
	}
	out := whereLine{Project: w.open, File: at.File, Line: at.Line}
	if w.session != nil {
		out.Session, out.Name = string(w.session.ID), w.session.Name
	}
	for _, p := range w.projects {
		out.Projects = append(out.Projects, projectLine{Name: p.Name, Branch: p.Branch, Dir: p.Dir})
		if at.File != "" && inside(at.File, p.Dir) {
			out.FileIn = p.Name
			if rel, err := filepath.Rel(p.Dir, at.File); err == nil {
				out.File = filepath.ToSlash(rel)
			}
		}
	}
	return out, nil
}

// sessionOr is the session ref names, else the one in front.
func (x *assistantPlugin) sessionOr(ref string) (core.SessionState, error) {
	if ref == "" {
		ref = string(x.h.State().Focus)
		if ref == "" {
			return core.SessionState{}, errors.New("no session is in front: name one from sessions")
		}
	}
	return x.session(ref)
}

// projectOf is a session's project by name, else its open one.
func projectOf(s core.SessionState, name string) (core.Project, error) {
	if name == "" {
		name = s.OpenProject()
	}
	var names []string
	for _, p := range s.Projects {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
		names = append(names, p.Name)
	}
	return core.Project{}, fmt.Errorf("%s has no project %q: it has %s", label(s), name, strings.Join(names, ", "))
}

// asstFile is a changed file as the assistant reads it.
type asstFile struct {
	Project  string `json:"project"`
	Path     string `json:"path"`
	Area     string `json:"area,omitempty"`
	Add      int    `json:"added,omitempty"`
	Del      int    `json:"removed,omitempty"`
	Ago      string `json:"modified,omitempty"`
	Test     bool   `json:"is_test,omitempty"`
	Deleted  bool   `json:"deleted,omitempty"`
	modified time.Time
}

func (x *assistantPlugin) toolChangedFiles(ctx registry.Ctx, p map[string]any) (any, error) {
	s, err := x.sessionOr(pstr(p, "session"))
	if err != nil {
		return nil, err
	}
	projects := s.Projects
	if name := pstr(p, "project"); name != "" {
		pr, err := projectOf(s, name)
		if err != nil {
			return nil, err
		}
		projects = []core.Project{pr}
	}
	tests := testrun.Builtin()
	if x.sh.tests != nil && x.sh.tests.set != nil {
		tests = x.sh.tests.set
	}
	only, _ := p["tests"].(bool)
	now := time.Now()
	out := []asstFile{}
	for _, pr := range projects {
		st, err := gitx.Repo{Dir: pr.Dir}.Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("couldn't read %s: %w", pr.Name, err)
		}
		files := st.Files(gitx.Changed)
		for _, f := range st.Files(gitx.Uncommitted) {
			if f.Area == gitx.Untracked {
				files = append(files, f)
			}
		}
		for _, f := range files {
			abs := filepath.Join(pr.Dir, filepath.FromSlash(f.Path))
			l := asstFile{Project: pr.Name, Path: f.Path, Area: f.Area.String(), Add: f.Add, Del: f.Del, Test: tests.TestFile(abs), Deleted: f.Code == 'D'}
			if only && !l.Test {
				continue
			}
			if fi, err := os.Stat(abs); err == nil {
				l.modified, l.Ago = fi.ModTime(), ago(now, fi.ModTime())
			}
			out = append(out, l)
		}
	}
	slices.SortStableFunc(out, func(a, b asstFile) int { return b.modified.Compare(a.modified) })
	if n := pint(p, "limit"); n > 0 && n < len(out) {
		out = out[:n]
	} else if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

// bringUp brings a session's project to the front, then runs then there: in place when it's the session in front,
// else once the window is on it.
func (x *assistantPlugin) bringUp(ctx registry.Ctx, s core.SessionState, p core.Project, then ...map[string]any) error {
	calls := append([]map[string]any{{"command": "project.open", "args": map[string]any{"project": p.Name}}}, then...)
	if s.ID != x.h.State().Focus {
		list := make([]any, len(calls))
		for i, c := range calls {
			list[i] = c
		}
		return x.run(ctx, "session.open", registry.Args{"id": string(s.ID), "then": list})
	}
	for _, c := range calls {
		args, _ := c["args"].(map[string]any)
		if err := x.run(ctx, c["command"].(string), registry.Args(args)); err != nil {
			return err
		}
	}
	return nil
}

func (x *assistantPlugin) toolOpenProject(ctx registry.Ctx, p map[string]any) (any, error) {
	s, err := x.sessionOr(pstr(p, "session"))
	if err != nil {
		return nil, err
	}
	pr, err := projectOf(s, pstr(p, "project"))
	if err != nil {
		return nil, err
	}
	if err := x.bringUp(ctx, s, pr); err != nil {
		return nil, err
	}
	return registry.Acted{Result: "opened", Line: "opened " + pr.Name + " in " + label(s)}, nil
}

func (x *assistantPlugin) toolFocusFile(ctx registry.Ctx, p map[string]any) (any, error) {
	s, err := x.sessionOr(pstr(p, "session"))
	if err != nil {
		return nil, err
	}
	path := pstr(p, "path")
	if path == "" {
		return nil, errors.New("which file?")
	}
	name := pstr(p, "project")
	if name == "" && filepath.IsAbs(path) {
		for _, pr := range s.Projects {
			if inside(path, pr.Dir) {
				name = pr.Name
			}
		}
	}
	pr, err := projectOf(s, name)
	if err != nil {
		return nil, err
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(pr.Dir, filepath.FromSlash(path))
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("there's no %s in %s: read changed_files again", path, pr.Name)
	}
	open := map[string]any{"path": abs}
	line := pint(p, "line")
	if line > 0 {
		open["line"] = line
	}
	if err := x.bringUp(ctx, s, pr, map[string]any{"command": "editor.open", "args": open}); err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(pr.Dir, abs)
	where := filepath.ToSlash(rel)
	if line > 0 {
		where += fmt.Sprintf(":%d", line)
	}
	return registry.Acted{Result: "it's in front", Line: "showed " + where, Detail: pr.Name + " · " + label(s)}, nil
}
