package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

const (
	str = hostcore.StrType
	bl  = hostcore.BoolType
	num = hostcore.IntType
)

// tools are what the agent is offered as mcp__oriel__*: the host's own, then the app's (LSP lookups, say).
func (x *Runner) tools(h *session) []agent.Tool {
	own := []agent.Tool{
		{Name: "reply", Description: "Reply in an Oriel thread. working: a progress note that keeps the thread marked in progress. resolve: resolve the thread after replying. " +
			"On a thread with origin=\"github\" your reply is a draft: the user reads it and forwards it to GitHub themselves. You never post to GitHub.",
			Schema: hostcore.Schema(`"thread":`+str+`,"body":`+str+`,"working":`+bl+`,"resolve":`+bl, "thread", "body"), Call: h.toolReply},
		{Name: "thread", Description: "Read one Oriel thread in full: every comment, with nothing clipped.", ReadOnly: true,
			Schema: hostcore.Schema(`"thread":`+str, "thread"), Call: h.toolThread},
		{Name: "threads", Description: "List the session's Oriel threads, open and closed, each with where it is, where it came from, who started it and its first line: the thread tool reads one in full. Those mirrored from a code host include other people's comments that weren't handed to you.", ReadOnly: true,
			Schema: hostcore.Schema(""), Call: h.toolThreads},
		{Name: "resolve", Description: "Resolve an Oriel thread.", Schema: hostcore.Schema(`"thread":`+str+`,"summary":`+str, "thread"), Call: h.toolResolve},
		{Name: "ask", Description: "Ask the user something in an Oriel thread and wait for the answer: the thread you're working on, where the user sees the question with its options as buttons and a field for an answer of their own. Each question has its options (header: a short label; multi: several may be picked). Returns each question with the user's answer.",
			Schema: hostcore.Schema(`"thread":`+str+`,"questions":`+questionsType, "thread", "questions"), Call: h.toolAsk},
		{Name: "comment", Description: `Start a thread of your own: on a line of a file (file, line, optional end_line and side "old"/"new", project in a session with more than one), or a general one with no file. severity rates a review finding: nit, low, medium, high or critical.`,
			Schema: hostcore.Schema(`"body":`+str+`,"file":`+str+`,"line":`+num+`,"end_line":`+num+`,"side":`+str+`,"project":`+str+`,"severity":{"type":"string","enum":["nit","low","medium","high","critical"]}`, "body"),
			Call:   h.toolComment},
		{Name: "task_add", Description: "Queue a task on the Tasks rail the user watches, and get its id. thread: the thread that asked for it, which links the two and completes the task when the thread is resolved.",
			Schema: hostcore.Schema(`"subject":`+str+`,"thread":`+str, "subject"), Call: h.toolTaskAdd},
		{Name: "task_update", Description: "Change a task: status is pending, in_progress, completed or deleted.",
			Schema: hostcore.Schema(`"id":`+str+`,"status":`+str+`,"subject":`+str, "id"), Call: h.toolTaskUpdate},
		{Name: "tasks", Description: "List the tasks on the Tasks rail, as JSON.", ReadOnly: true, Schema: hostcore.Schema(""), Call: h.toolTasks},
		{Name: "projects", Description: "List the session's projects: name, directory, branch, and whether it has uncommitted changes.", ReadOnly: true,
			Schema: hostcore.Schema(""), Call: h.toolProjects},
		{Name: "commit_draft", Description: "Save the commit message shown on a project's Ship page.",
			Schema: hostcore.Schema(`"message":`+str+`,"project":`+str, "message"), Call: h.toolDraft("commit")},
		{Name: "pr_draft", Description: "Save the pull request draft shown on a project's Ship page.",
			Schema: hostcore.Schema(`"title":`+str+`,"body":`+str+`,"project":`+str, "title", "body"), Call: h.toolDraft("pr")},
		{Name: "test_run", Description: "Run tests and record the run as evidence. Returns the run's id: cite it (run:<id>) when you say tests pass. dir: a directory in a project (the primary project if empty); filter: a test name pattern.",
			Schema: hostcore.Schema(`"dir":` + str + `,"filter":` + str + `,"project":` + str), Call: h.toolTestRun},
	}
	kind := ""
	if se, err := x.st.Session(h.id); err == nil {
		kind = string(se.Kind)
		// A sandbox's gateway offers its own: the agent restarts in a sandbox that can see the new project.
		if x.deps.AddRepo != nil && x.deps.Kinds.Spec(se).Can(core.AddProjects) && !se.Agent.Sandboxed {
			own = append(own,
				agent.Tool{Name: "repos", Description: "List the user's repositories that could join this session as projects: name and path.", ReadOnly: true,
					Schema: hostcore.Schema(""), Call: h.toolRepos},
				agent.Tool{Name: "add_project", Description: "Bring one of the user's repositories into this session as a project, a worktree on the session's branch, and answer where it is. repo is a name or path from repos.",
					Schema: hostcore.Schema(`"repo":`+str, "repo"), Call: h.toolAddProject})
		}
		if k := x.deps.Kinds.Spec(se); x.deps.NewBranch != nil && k.Can(core.OwnsBranch) && k.Can(core.Ships) {
			own = append(own, branchTools(h)...)
		}
	}
	for _, t := range x.AppTools(hostcore.ToolsReq{Session: h.id, Agent: kind}) {
		if !slices.ContainsFunc(own, func(o agent.Tool) bool { return o.Name == t.Name }) {
			own = append(own, t)
		}
	}
	return own
}

// toolReply is the agent answering a thread: its reply carries the test runs that back what it claims.
func (h *session) toolReply(_ context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	body := strings.TrimSpace(a.Str("body"))
	if body == "" {
		return "", errors.New("the reply is empty")
	}
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	t, err := hostcore.FindThread(se, a.Str("thread"))
	if err != nil {
		return "", err
	}
	working, resolve := a.Bool("working"), a.Bool("resolve")
	if resolve || !working {
		if err := h.refuseUnseen(t.ID); err != nil {
			return "", err
		}
	}
	h.mu.Lock()
	runs := slices.Clone(h.runs)
	h.mu.Unlock()
	c := core.Comment{ID: hostcore.NewID(6), Author: core.AuthorAgent, Name: se.Agent.Provider, Body: body, Time: h.r.env.Now().UTC(),
		Progress: working, Model: modelLabel(se.Agent, h.ranModel()), Draft: t.Hosted != nil && !working}
	if !working {
		c.Evidence, c.Unbacked = evidence(se, body, runs)
	}
	_, err = h.r.mutateThread(h.id, t.ID, func(t *core.Thread) error {
		t.Comments = append(t.Comments, c)
		t.Busy, t.Active = working, working
		if resolve {
			t.Status, t.Busy, t.Active = core.ThreadResolved, false, false
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if resolve {
		h.r.completeTasks(h.id, t.ID)
	}
	hostcore.Restatus(h.r.st, h.id)
	said := "replied"
	if c.Draft {
		said = "drafted: the user reads your reply and forwards it to GitHub; nothing was posted"
	}
	if c.Unbacked != "" {
		return said + "; note: " + c.Unbacked + ". Run the tests with test_run and cite the run.", nil
	}
	return said, nil
}

var (
	claimsPass = regexp.MustCompile(`(?i)\b(tests?|suite|specs?)\b[^.\n]{0,40}\b(pass(es|ing|ed)?|green|succeed(s|ed)?)\b|\ball (tests )?pass(es|ing)?\b`)
	citesRun   = regexp.MustCompile(`run:([0-9a-f]{4,})`)
)

// evidence links a reply's test claims to the runs that back them: those it cites, else the turn's last. A claim with
// none is noted, not refused.
func evidence(se core.SessionState, body string, runs []string) ([]core.Evidence, string) {
	var cited []string
	for _, m := range citesRun.FindAllStringSubmatch(body, -1) {
		if !slices.Contains(cited, m[1]) {
			cited = append(cited, m[1])
		}
	}
	// The reply shows the runs it cites, else the turn's last; a claim is backed by any of them, else any of the turn's
	// that passed tests, so a reply after another one, or after a run whose other packages failed, isn't flagged.
	shown, backing := runs, runs
	if len(cited) > 0 {
		shown, backing = cited, cited
	} else if len(runs) > 1 {
		shown = runs[len(runs)-1:]
	}
	var out []core.Evidence
	for _, id := range shown {
		r, ok := se.Run(id)
		if !ok {
			continue
		}
		label := fmt.Sprintf("%d passed", r.Passed)
		if r.Failed > 0 {
			label = fmt.Sprintf("%d failed, %d passed", r.Failed, r.Passed)
		}
		out = append(out, core.Evidence{Kind: "test-run", Ref: r.ID, Label: label, OK: r.OK})
	}
	if !claimsPass.MatchString(body) {
		return out, ""
	}
	if !slices.ContainsFunc(backing, func(id string) bool { r, ok := se.Run(id); return ok && (r.OK || r.Passed > 0) }) {
		return out, "says tests pass, but no passing test run backs it"
	}
	return out, ""
}

func (h *session) toolThread(_ context.Context, raw json.RawMessage) (string, error) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	t, err := hostcore.FindThread(se, hostcore.ParseArgs(raw).Str("thread"))
	if err != nil {
		return "", err
	}
	return renderThreadFull(se, t), nil
}

func (h *session) toolThreads(context.Context, json.RawMessage) (string, error) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	return renderThreadList(se), nil
}

func (h *session) toolResolve(ctx context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	if s := strings.TrimSpace(a.Str("summary")); s != "" {
		b, _ := json.Marshal(map[string]any{"thread": a.Str("thread"), "body": s, "resolve": true})
		return h.toolReply(ctx, b)
	}
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	t, err := hostcore.FindThread(se, a.Str("thread"))
	if err != nil {
		return "", err
	}
	if err := h.refuseUnseen(t.ID); err != nil {
		return "", err
	}
	return "resolved", h.r.SetThread(ThreadReq{Session: h.id, Thread: t.ID, Status: core.ThreadResolved})
}

// questionsType is the ask tool's questions: each a question, with its options.
const questionsType = `{"type":"array","minItems":1,"items":{"type":"object","properties":{"question":` + str + `,"header":` + str +
	`,"options":{"type":"array","items":` + str + `},"multi":` + bl + `},"required":["question"]}}`

// toolAsk is the agent asking the user in a thread: it waits for the answer and returns it.
func (h *session) toolAsk(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Thread    string          `json:"thread"`
		Questions []core.Question `json:"questions"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	in.Questions = slices.DeleteFunc(in.Questions, func(q core.Question) bool { return strings.TrimSpace(q.Question) == "" })
	if len(in.Questions) == 0 {
		return "", errors.New("ask has no questions")
	}
	if strings.TrimSpace(in.Thread) == "" {
		return "", errors.New("say which thread you're asking in: thread")
	}
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	t, err := hostcore.FindThread(se, in.Thread)
	if err != nil {
		return "", err
	}
	a, ok := h.ask(ctx, hostcore.NewID(6), t.ID, in.Questions)
	if !ok {
		return "", errors.New("the question went unanswered")
	}
	if len(a.Answers) == 0 {
		return "The user chose not to answer. " + strings.TrimSpace(a.Note), nil
	}
	return answerText(in.Questions, a), nil
}

// refuseUnseen refuses an answer to a thread, or resolving it, while it holds comments the agent hasn't been given:
// those of the first turn count as given while its start hands them over. The error hands them over, so they count as
// given and the thread stays the agent's.
func (h *session) refuseUnseen(tid core.ThreadID) error {
	// Read with h.mu held: the first turn's comments are marked given while it's held, as handing is cleared.
	h.mu.Lock()
	handing := h.handing
	se, err := h.r.st.Session(h.id)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	unseenIn := func(t core.Thread) []core.Comment {
		return slices.DeleteFunc(t.Undelivered(), func(c core.Comment) bool { return handing[c.ID] })
	}
	if t, ok := se.Thread(tid); !ok || len(unseenIn(t)) == 0 {
		return nil
	}
	t, err := h.r.mutateThread(h.id, tid, func(t *core.Thread) error {
		unseen := unseenIn(*t)
		for i, c := range t.Comments {
			if slices.ContainsFunc(unseen, func(u core.Comment) bool { return u.ID == c.ID }) {
				t.Comments[i].Delivered = true
			}
		}
		t.Busy, t.Active = true, true
		return nil
	})
	if err != nil {
		return err
	}
	return errors.New("refused: the user added comments you hadn't seen. Handle them, then reply or resolve again.\n\n" + renderThreadFull(se, t))
}

func (h *session) toolComment(ctx context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	body := strings.TrimSpace(a.Str("body"))
	if body == "" {
		return "", errors.New("the comment is empty")
	}
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	now := h.r.env.Now().UTC()
	t := core.Thread{ID: core.ThreadID(hostcore.NewID(8)), Status: core.ThreadOpen, Created: now, Updated: now,
		Comments: []core.Comment{{ID: hostcore.NewID(6), Author: core.AuthorAgent, Name: se.Agent.Provider, Body: body, Time: now,
			Severity: a.Str("severity"), Model: modelLabel(se.Agent, h.ranModel()), Delivered: true}}}
	if f := a.Str("file"); f != "" {
		line := max(1, a.Int("line"))
		t.Anchor = &core.Anchor{Project: a.Str("project"), File: f, Line: line, EndLine: a.Int("end_line"), Side: a.Str("side")}
		t.Anchor.Text = hostcore.AnchorText(se, t.Anchor)
		if h.r.deps.Kinds.Spec(se).Can(core.CommentsInDiff) {
			if err := h.inDiff(ctx, se, t.Anchor); err != nil {
				return "", err
			}
		}
	}
	if err := h.r.st.Emit(core.ThreadUpdated{Session: h.id, Thread: t}); err != nil {
		return "", err
	}
	hostcore.Restatus(h.r.st, h.id)
	return string(t.ID), nil
}

// inDiff refuses a line comment on lines the pull request's diff doesn't show, which its review can't hold.
func (h *session) inDiff(ctx context.Context, se core.SessionState, a *core.Anchor) error {
	project := a.Project
	if p, ok := se.Primary(); ok && project == "" {
		project = p.Name
	}
	_, p, repo, err := hostcore.ProjectRepo(ctx, h.r.st, h.r.deps.Kinds, h.id, project)
	if err != nil {
		return err
	}
	base := p.Base
	if p.Detached {
		base = "" // the base the review's worktree was given
	}
	files, err := repo.ReviewLines(ctx, base)
	if err != nil {
		return nil // nothing to check against: the comment stands
	}
	const instead = "Comment only on lines the diff shows (changed lines and the 3 lines around them), or put this in a general comment with no file."
	lines, ok := files[strings.TrimPrefix(a.File, "./")]
	if !ok {
		return fmt.Errorf("%s isn't in the pull request's diff. %s", a.File, instead)
	}
	side := "RIGHT"
	if a.Side == "old" {
		side = "LEFT"
	}
	for n := a.Line; n <= max(a.Line, a.EndLine); n++ {
		if !lines[side][n] {
			return fmt.Errorf("line %d of %s isn't in the pull request's diff, which shows %s there. %s", n, a.File, lineRanges(lines[side]), instead)
		}
	}
	return nil
}

// lineRanges writes a set of line numbers as runs: "lines 3-9, 40-52".
func lineRanges(lines map[int]bool) string {
	ns := slices.Sorted(maps.Keys(lines))
	if len(ns) == 0 {
		return "no lines on that side"
	}
	var runs []string
	for i := 0; i < len(ns); {
		j := i
		for j+1 < len(ns) && ns[j+1] == ns[j]+1 {
			j++
		}
		run := strconv.Itoa(ns[i])
		if j > i {
			run += "-" + strconv.Itoa(ns[j])
		}
		runs = append(runs, run)
		i = j + 1
	}
	return "lines " + strings.Join(runs, ", ")
}

func (h *session) toolTaskAdd(_ context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	if strings.TrimSpace(a.Str("subject")) == "" {
		return "", errors.New("the task has no subject")
	}
	t := core.Task{ID: h.r.nextTaskID(h.id), Subject: a.Str("subject"), Status: core.TaskPending, Created: h.r.env.Now().UTC()}
	if ref := a.Str("thread"); ref != "" {
		se, _ := h.r.st.Session(h.id)
		th, err := hostcore.FindThread(se, ref)
		if err != nil {
			return "", err
		}
		t.Thread = th.ID
	}
	if err := h.r.st.Emit(core.TaskChanged{Session: h.id, Task: t}); err != nil {
		return "", err
	}
	return "task " + t.ID + " added", nil
}

func (h *session) toolTaskUpdate(_ context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(se.Tasks, func(t core.Task) bool { return t.ID == a.Str("id") })
	if i < 0 {
		return "", fmt.Errorf("no task %q", a.Str("id"))
	}
	t := se.Tasks[i]
	if s := a.Str("status"); s != "" {
		switch core.TaskStatus(s) {
		case core.TaskPending, core.TaskInProgress, core.TaskCompleted, core.TaskDeleted:
			t.Status = core.TaskStatus(s)
		default:
			return "", fmt.Errorf("status is pending, in_progress, completed or deleted, not %q", s)
		}
	}
	if s := a.Str("subject"); s != "" {
		t.Subject = s
	}
	if err := h.r.st.Emit(core.TaskChanged{Session: h.id, Task: t}); err != nil {
		return "", err
	}
	if t.Status == core.TaskInProgress && t.Thread != "" {
		_, _ = h.r.mutateThread(h.id, t.Thread, func(th *core.Thread) error {
			th.Active = th.Busy
			return nil
		})
	}
	return "task " + t.ID + " " + string(t.Status), nil
}

func (h *session) toolTasks(context.Context, json.RawMessage) (string, error) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(se.Tasks, "", "  ")
	if len(se.Tasks) == 0 {
		return "[]", nil
	}
	return string(b), err
}

func (h *session) toolProjects(ctx context.Context, _ json.RawMessage) (string, error) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	return hostcore.ProjectsTool(ctx, se)
}

// toolRepos are the registered repos not in the session yet.
func (h *session) toolRepos(context.Context, json.RawMessage) (string, error) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, r := range h.r.deps.Repos() {
		if slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Repo == r.Path || p.Dir == r.Path }) {
			continue
		}
		fmt.Fprintf(&sb, "%s\t%s\n", r.ID, r.Path)
	}
	if sb.Len() == 0 {
		return "No repositories to add: none registered that isn't in this session already.", nil
	}
	return sb.String(), nil
}

// toolAddProject brings a registered repo into the session, and says where its worktree is.
func (h *session) toolAddProject(ctx context.Context, raw json.RawMessage) (string, error) {
	ref := strings.TrimSpace(hostcore.ParseArgs(raw).Str("repo"))
	if ref == "" {
		return "", errors.New("say which repo: a name or path from repos")
	}
	before, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	if err := h.r.deps.AddRepo(ctx, h.id, core.RepoID(ref)); err != nil {
		return "", err
	}
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	for _, p := range se.Projects {
		if !slices.ContainsFunc(before.Projects, func(o core.Project) bool { return o.Name == p.Name }) {
			return fmt.Sprintf("Added %s at %s, on branch %s. Work in it there.", p.Name, p.Dir, p.Branch), nil
		}
	}
	return "Added " + ref + ".", nil
}

// toolDraft keeps a commit message or PR draft for the Ship page, per project, with the code it was drafted
// against. One the user wrote stays.
func (h *session) toolDraft(kind string) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		a := hostcore.ParseArgs(raw)
		se, err := h.r.st.Session(h.id)
		if err != nil {
			return "", err
		}
		project := a.Str("project")
		if project == "" {
			if p, ok := se.Primary(); ok {
				project = p.Name
			}
		}
		_, p, repo, err := hostcore.ProjectRepo(ctx, h.r.st, h.r.deps.Kinds, h.id, project)
		if err != nil {
			return "", err
		}
		if h.r.env.ReadDraft(h.id, p.Name, kind).By == hostcore.DraftByYou {
			return fmt.Sprintf("Not saved: the user wrote %s's %s themselves. Leave it.", p.Name, hostcore.DraftNoun[kind]), nil
		}
		code := hostcore.DraftCode(ctx, repo, repo.FlightBase(ctx, p.Base), kind)
		title, body := a.Str("title"), a.Str("body")
		if kind == "commit" {
			title, body = "", a.Str("message")
		}
		if err := h.r.env.WriteDraft(h.id, p.Name, kind, title, body, se.Agent.Provider, code); err != nil {
			return "", err
		}
		return "saved", h.r.st.Emit(core.DraftSaved{Session: h.id, Project: p.Name, Kind: kind})
	}
}
