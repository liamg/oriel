package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
		{Name: "resolve", Description: "Resolve an Oriel thread.", Schema: hostcore.Schema(`"thread":`+str+`,"summary":`+str, "thread"), Call: h.toolResolve},
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
	return "resolved", h.r.SetThread(ThreadReq{Session: h.id, Thread: t.ID, Status: core.ThreadResolved})
}

func (h *session) toolComment(_ context.Context, raw json.RawMessage) (string, error) {
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
	}
	if err := h.r.st.Emit(core.ThreadUpdated{Session: h.id, Thread: t}); err != nil {
		return "", err
	}
	hostcore.Restatus(h.r.st, h.id)
	return string(t.ID), nil
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
		if kind == "commit" {
			return "saved", h.r.env.WriteDraft(h.id, p.Name, kind, "", a.Str("message"), se.Agent.Provider, code)
		}
		return "saved", h.r.env.WriteDraft(h.id, p.Name, kind, a.Str("title"), a.Str("body"), se.Agent.Provider, code)
	}
}
