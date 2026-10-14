package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

func schema(props string, required ...string) json.RawMessage {
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(req) + `}`)
}

const (
	str = `{"type":"string"}`
	bl  = `{"type":"boolean"}`
	num = `{"type":"integer"}`
)

// tools are what the agent is offered as mcp__oriel__*: the worker's own, then the app's (LSP lookups, say).
func (w *Worker) tools(h *host) []agent.Tool {
	own := []agent.Tool{
		{Name: "reply", Description: "Reply in an Oriel thread. working: a progress note that keeps the thread marked in progress. resolve: resolve the thread after replying.",
			Schema: schema(`"thread":`+str+`,"body":`+str+`,"working":`+bl+`,"resolve":`+bl, "thread", "body"), Call: h.toolReply},
		{Name: "thread", Description: "Read one Oriel thread in full: every comment, with nothing clipped.", ReadOnly: true,
			Schema: schema(`"thread":`+str, "thread"), Call: h.toolThread},
		{Name: "resolve", Description: "Resolve an Oriel thread.", Schema: schema(`"thread":`+str+`,"summary":`+str, "thread"), Call: h.toolResolve},
		{Name: "comment", Description: `Start a thread of your own: on a line of a file (file, line, optional end_line and side "old"/"new", project in a session with more than one), or a general one with no file. severity rates a review finding: nit, low, medium, high or critical.`,
			Schema: schema(`"body":`+str+`,"file":`+str+`,"line":`+num+`,"end_line":`+num+`,"side":`+str+`,"project":`+str+`,"severity":{"type":"string","enum":["nit","low","medium","high","critical"]}`, "body"),
			Call:   h.toolComment},
		{Name: "task_add", Description: "Queue a task on the Tasks rail the user watches, and get its id. thread: the thread that asked for it, which links the two and completes the task when the thread is resolved.",
			Schema: schema(`"subject":`+str+`,"thread":`+str, "subject"), Call: h.toolTaskAdd},
		{Name: "task_update", Description: "Change a task: status is pending, in_progress, completed or deleted.",
			Schema: schema(`"id":`+str+`,"status":`+str+`,"subject":`+str, "id"), Call: h.toolTaskUpdate},
		{Name: "tasks", Description: "List the tasks on the Tasks rail, as JSON.", ReadOnly: true, Schema: schema(""), Call: h.toolTasks},
		{Name: "projects", Description: "List the session's projects: name, directory, branch, and whether it has uncommitted changes.", ReadOnly: true,
			Schema: schema(""), Call: h.toolProjects},
		{Name: "commit_draft", Description: "Save the commit message shown on a project's Ship page.",
			Schema: schema(`"message":`+str+`,"project":`+str, "message"), Call: h.toolDraft("commit")},
		{Name: "pr_draft", Description: "Save the pull request draft shown on a project's Ship page.",
			Schema: schema(`"title":`+str+`,"body":`+str+`,"project":`+str, "title", "body"), Call: h.toolDraft("pr")},
		{Name: "test_run", Description: "Run tests and record the run as evidence. Returns the run's id: cite it (run:<id>) when you say tests pass. dir: a directory in a project (the primary project if empty); filter: a test name pattern.",
			Schema: schema(`"dir":` + str + `,"filter":` + str + `,"project":` + str), Call: h.toolTestRun},
	}
	for _, app := range w.appConns() {
		var list []agent.Tool
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := app.Call(ctx, MethodAppTools, SessionRef{h.id}, &list)
		cancel()
		if err != nil {
			continue
		}
		for _, t := range list {
			if slices.ContainsFunc(own, func(o agent.Tool) bool { return o.Name == t.Name }) {
				continue
			}
			t.Call = h.appTool(t.Name)
			own = append(own, t)
		}
		break
	}
	return own
}

func (h *host) appTool(name string) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		for _, app := range h.w.appConns() {
			var out string
			if err := app.Call(ctx, MethodAppTool, ToolCall{Session: h.id, Tool: name, Args: args}, &out); err != nil {
				return "", err
			}
			return out, nil
		}
		return "", errors.New("Oriel's window isn't open, and this tool runs there")
	}
}

type toolArgs map[string]any

func parse(raw json.RawMessage) toolArgs {
	var a toolArgs
	_ = json.Unmarshal(raw, &a)
	return a
}

func (a toolArgs) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func (a toolArgs) int(k string) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func (a toolArgs) bool(k string) bool { b, _ := a[k].(bool); return b }

// toolReply is the agent answering a thread: its reply carries what it did since its last one, and the test runs
// that back what it claims.
func (h *host) toolReply(_ context.Context, raw json.RawMessage) (string, error) {
	a := parse(raw)
	body := strings.TrimSpace(a.str("body"))
	if body == "" {
		return "", errors.New("the reply is empty")
	}
	se, err := h.w.session(h.id)
	if err != nil {
		return "", err
	}
	t, err := findThread(se, a.str("thread"))
	if err != nil {
		return "", err
	}
	working, resolve := a.bool("working"), a.bool("resolve")
	h.mu.Lock()
	activity, runs := h.activity, h.runs
	if !working {
		h.activity, h.runs = nil, nil
	}
	h.mu.Unlock()
	c := core.Comment{ID: newID(6), Author: core.AuthorAgent, Name: se.Agent.Provider, Body: body, Time: h.w.opts.Now().UTC(),
		Progress: working, Model: modelLabel(se.Agent)}
	if !working {
		c.Activity = activity
		c.Evidence, c.Unbacked = evidence(se, body, runs)
	}
	_, err = h.w.mutateThread(h.id, t.ID, func(t *core.Thread) error {
		t.Comments = append(t.Comments, c)
		t.Busy = working
		if resolve {
			t.Status, t.Busy = core.ThreadResolved, false
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if resolve {
		h.w.completeTasks(h.id, t.ID)
	}
	h.w.restatus(h.id)
	if c.Unbacked != "" {
		return "replied; note: " + c.Unbacked + ". Run the tests with test_run and cite the run.", nil
	}
	return "replied", nil
}

var (
	claimsPass = regexp.MustCompile(`(?i)\b(tests?|suite|specs?)\b[^.\n]{0,40}\b(pass(es|ing|ed)?|green|succeed(s|ed)?)\b|\ball (tests )?pass(es|ing)?\b`)
	citesRun   = regexp.MustCompile(`run:([0-9a-f]{4,})`)
)

// evidence links a reply's test claims to the runs that back them; a claim with none is noted, not refused.
func evidence(se core.SessionState, body string, runs []string) ([]core.Evidence, string) {
	for _, m := range citesRun.FindAllStringSubmatch(body, -1) {
		if !slices.Contains(runs, m[1]) {
			runs = append(runs, m[1])
		}
	}
	var out []core.Evidence
	for _, id := range runs {
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
	if !slices.ContainsFunc(out, func(e core.Evidence) bool { return e.OK }) {
		return out, "says tests pass, but no passing test run backs it"
	}
	return out, ""
}

func (h *host) toolThread(_ context.Context, raw json.RawMessage) (string, error) {
	se, err := h.w.session(h.id)
	if err != nil {
		return "", err
	}
	t, err := findThread(se, parse(raw).str("thread"))
	if err != nil {
		return "", err
	}
	return renderThreadFull(se, t), nil
}

func (h *host) toolResolve(ctx context.Context, raw json.RawMessage) (string, error) {
	a := parse(raw)
	if s := strings.TrimSpace(a.str("summary")); s != "" {
		b, _ := json.Marshal(map[string]any{"thread": a.str("thread"), "body": s, "resolve": true})
		return h.toolReply(ctx, b)
	}
	se, err := h.w.session(h.id)
	if err != nil {
		return "", err
	}
	t, err := findThread(se, a.str("thread"))
	if err != nil {
		return "", err
	}
	return "resolved", h.w.SetThread(ThreadReq{Session: h.id, Thread: t.ID, Status: core.ThreadResolved})
}

func (h *host) toolComment(_ context.Context, raw json.RawMessage) (string, error) {
	a := parse(raw)
	body := strings.TrimSpace(a.str("body"))
	if body == "" {
		return "", errors.New("the comment is empty")
	}
	se, err := h.w.session(h.id)
	if err != nil {
		return "", err
	}
	now := h.w.opts.Now().UTC()
	t := core.Thread{ID: core.ThreadID(newID(8)), Status: core.ThreadOpen, Created: now, Updated: now,
		Comments: []core.Comment{{ID: newID(6), Author: core.AuthorAgent, Name: se.Agent.Provider, Body: body, Time: now,
			Severity: a.str("severity"), Model: modelLabel(se.Agent), Delivered: true}}}
	if f := a.str("file"); f != "" {
		line := max(1, a.int("line"))
		t.Anchor = &core.Anchor{Project: a.str("project"), File: f, Line: line, EndLine: a.int("end_line"), Side: a.str("side")}
		t.Anchor.Text = anchorText(se, t.Anchor)
	}
	if err := h.w.emit(core.ThreadUpdated{Session: h.id, Thread: t}); err != nil {
		return "", err
	}
	h.w.restatus(h.id)
	return string(t.ID), nil
}

// anchorText is the anchored lines as they read now, so the thread can follow later edits.
func anchorText(se core.SessionState, a *core.Anchor) string {
	dir := ""
	if a.Project == core.DocsProject {
		dir = se.Dir
	}
	for _, p := range se.Projects {
		if p.Name == a.Project || (a.Project == "" && p.Primary) {
			dir = p.Dir
		}
	}
	if dir == "" {
		if p, ok := se.Primary(); ok {
			dir = p.Dir
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, a.File))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	end := max(a.Line, a.EndLine)
	if a.Line < 1 || a.Line > len(lines) {
		return ""
	}
	return strings.Join(lines[a.Line-1:min(end, len(lines))], "\n")
}

func (h *host) toolTaskAdd(_ context.Context, raw json.RawMessage) (string, error) {
	a := parse(raw)
	if strings.TrimSpace(a.str("subject")) == "" {
		return "", errors.New("the task has no subject")
	}
	t := core.Task{ID: h.w.nextTaskID(h.id), Subject: a.str("subject"), Status: core.TaskPending, Created: h.w.opts.Now().UTC()}
	if ref := a.str("thread"); ref != "" {
		se, _ := h.w.session(h.id)
		th, err := findThread(se, ref)
		if err != nil {
			return "", err
		}
		t.Thread = th.ID
	}
	if err := h.w.emit(core.TaskChanged{Session: h.id, Task: t}); err != nil {
		return "", err
	}
	return "task " + t.ID + " added", nil
}

func (h *host) toolTaskUpdate(_ context.Context, raw json.RawMessage) (string, error) {
	a := parse(raw)
	se, err := h.w.session(h.id)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(se.Tasks, func(t core.Task) bool { return t.ID == a.str("id") })
	if i < 0 {
		return "", fmt.Errorf("no task %q", a.str("id"))
	}
	t := se.Tasks[i]
	if s := a.str("status"); s != "" {
		switch core.TaskStatus(s) {
		case core.TaskPending, core.TaskInProgress, core.TaskCompleted, core.TaskDeleted:
			t.Status = core.TaskStatus(s)
		default:
			return "", fmt.Errorf("status is pending, in_progress, completed or deleted, not %q", s)
		}
	}
	if s := a.str("subject"); s != "" {
		t.Subject = s
	}
	if err := h.w.emit(core.TaskChanged{Session: h.id, Task: t}); err != nil {
		return "", err
	}
	return "task " + t.ID + " " + string(t.Status), nil
}

func (h *host) toolTasks(context.Context, json.RawMessage) (string, error) {
	se, err := h.w.session(h.id)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(se.Tasks, "", "  ")
	if len(se.Tasks) == 0 {
		return "[]", nil
	}
	return string(b), err
}

func (h *host) toolProjects(ctx context.Context, _ json.RawMessage) (string, error) {
	se, err := h.w.session(h.id)
	if err != nil {
		return "", err
	}
	type row struct {
		Name     string `json:"name"`
		Dir      string `json:"dir"`
		Branch   string `json:"branch,omitempty"`
		Base     string `json:"base,omitempty"`
		Primary  bool   `json:"primary,omitempty"`
		ReadOnly bool   `json:"readOnly,omitempty"`
		Dirty    bool   `json:"uncommitted,omitempty"`
	}
	var out []row
	for _, p := range se.Projects {
		out = append(out, row{Name: p.Name, Dir: p.Dir, Branch: p.Branch, Base: p.Base, Primary: p.Primary, ReadOnly: p.ReadOnly,
			Dirty: !p.ReadOnly && gitx.Repo{Dir: p.Dir}.Dirty(ctx)})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	return string(b), err
}

// toolDraft keeps a commit message or PR draft for the Ship page, per project.
func (h *host) toolDraft(kind string) func(context.Context, json.RawMessage) (string, error) {
	return func(_ context.Context, raw json.RawMessage) (string, error) {
		a := parse(raw)
		se, err := h.w.session(h.id)
		if err != nil {
			return "", err
		}
		project := a.str("project")
		if project == "" {
			if p, ok := se.Primary(); ok {
				project = p.Name
			}
		}
		dir := filepath.Join(h.w.sessionDir(h.id), "drafts")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		var body string
		if kind == "commit" {
			body = a.str("message")
		} else {
			body = "# " + a.str("title") + "\n\n" + a.str("body")
		}
		if strings.TrimSpace(body) == "" {
			return "", errors.New("the draft is empty")
		}
		return "saved", os.WriteFile(filepath.Join(dir, Sanitise(project)+"."+kind+".md"), []byte(body), 0o600)
	}
}

// RuleFor is the rule "Always" makes of a tool call: the command's first two words for a shell, the tool for
// the rest.
func RuleFor(tool string, input json.RawMessage) string {
	a := parse(input)
	if cmd := a.str("command"); cmd != "" {
		f := strings.Fields(cmd)
		if len(f) > 2 {
			f = f[:2]
		}
		return tool + "(" + strings.Join(f, " ") + ":*)"
	}
	return tool
}

// matchRule reports whether rule lets the ask through.
func matchRule(rule string, ask core.Ask) bool {
	if ask.Kind == core.AskNetwork {
		pat, ok := strings.CutPrefix(rule, "net(")
		if !ok {
			return false
		}
		return hostMatch(strings.TrimSuffix(pat, ")"), ask.Host)
	}
	tool, arg, hasArg := strings.Cut(rule, "(")
	if tool != ask.Tool {
		return false
	}
	if !hasArg {
		return true
	}
	arg = strings.TrimSuffix(arg, ")")
	cmd := parse(json.RawMessage(ask.Input)).str("command")
	if prefix, ok := strings.CutSuffix(arg, ":*"); ok {
		return cmd == prefix || strings.HasPrefix(cmd, prefix+" ")
	}
	return cmd == arg
}

func hostMatch(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		h, _, _ := strings.Cut(host, ":")
		p, port, hasPort := strings.Cut(rest, ":")
		if hasPort && !strings.HasSuffix(host, ":"+port) {
			return false
		}
		return h == p || strings.HasSuffix(h, "."+p)
	}
	return false
}

func (w *Worker) addRule(id core.SessionID, rule string) error {
	if rule == "" {
		return nil
	}
	return w.emit(core.RuleAdded{Session: id, Rule: rule})
}

// policy is layer 2: the session's rules, then the app's hooks. ok is false when neither answers.
func (w *Worker) policy(id core.SessionID, ask core.Ask) (Verdict, bool) {
	se, err := w.session(id)
	if err != nil {
		return Verdict{}, false
	}
	for _, r := range se.Rules {
		if matchRule(r, ask) {
			return Verdict{Decision: "allow", By: "rule " + r}, true
		}
	}
	for _, app := range w.appConns() {
		var v Verdict
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := app.Call(ctx, MethodAppDecide, Decide{Session: id, Ask: ask}, &v)
		cancel()
		if err == nil && (v.Decision == "allow" || v.Decision == "deny") {
			if v.By == "" {
				v.By = "policy"
			}
			return v, true
		}
	}
	return Verdict{}, false
}
