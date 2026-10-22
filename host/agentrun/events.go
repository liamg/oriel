package agentrun

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/host/stats"
)

func (h *session) handle(sess agent.Session, ev agent.Event) {
	x := h.r
	now := x.env.Now().UTC()
	switch ev := ev.(type) {
	case agent.Ready:
		h.setAgent(func(a *core.AgentState) { a.Session = ev.ID })
	case agent.TurnStart:
		h.r.deps.Record(h.snapshot(), stats.Event{At: now, Kind: "turn"})
		h.mu.Lock()
		h.working, h.turnAt = true, now
		h.mu.Unlock()
		h.setAgent(func(a *core.AgentState) { a.Working, a.TurnStarted, a.Error = true, now, "" })
	case agent.Message:
		kind := ev.Role
		if kind == "" {
			kind = core.ItemAssistant
		}
		id := ev.ID
		if id == "" {
			id = hostcore.NewID(6)
		}
		h.appendItem(core.Item{ID: id, Kind: kind, Text: ev.Text, Partial: ev.Partial, Agent: ev.Agent, Time: now})
	case agent.ToolCall:
		se := h.snapshot()
		h.r.deps.Record(se, stats.Event{At: now, Kind: "tool", ID: ev.ID, Name: ev.Tool, Input: ev.Input, Agent: ev.Agent, Model: se.Agent.Model,
			Effort: se.Agent.Effort, Cwd: hostcore.AgentDir(se)})
		h.mu.Lock()
		h.calls[ev.ID] = ev
		h.mu.Unlock()
		it := core.Item{ID: ev.ID, Kind: core.ItemTool, Tool: ev.Tool, Input: string(ev.Input), Title: toolTitle(ev.Tool, ev.Input),
			Agent: ev.Agent, Time: now}
		h.appendItem(it)
		h.setAgent(func(a *core.AgentState) { a.Stats.ToolCalls++ })
		if ev.Agent != "" {
			h.subagentTool(ev.Agent)
		}
	case agent.ToolResult:
		h.r.deps.Record(h.snapshot(), stats.Event{At: now, Kind: "result", ID: ev.ID, Error: ev.IsError})
		h.toolResult(ev, now)
	case agent.Permission:
		x.env.Go("a permission", func() { h.permission(sess, ev) })
	case agent.Question:
		x.env.Go("a question", func() { h.question(sess, ev) })
	case agent.Subagent:
		h.subagent(ev, now)
	case agent.TurnEnd:
		se := h.snapshot()
		u := ev.Usage
		h.r.deps.Record(se, stats.Event{At: now, Kind: "request", Model: se.Agent.Model, Effort: se.Agent.Effort, Window: int64(u.ContextMax),
			Tokens: stats.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite5m: u.CacheWrite}})
		h.turnEnd(ev, now)
	case agent.Failure:
		h.appendItem(core.Item{ID: hostcore.NewID(6), Kind: core.ItemError, Text: ev.Err, Time: now})
		h.setAgent(func(a *core.AgentState) { a.Error = ev.Err })
	case agent.UsageUpdate:
		x.deps.UsageSampled(ev.Usage)
		if acc := h.snapshot().Agent.Accounts; ev.Usage.Account != "" && (len(acc) == 0 || acc[len(acc)-1].Account != ev.Usage.Account) {
			h.setAgent(func(a *core.AgentState) { a.RanOn(now, ev.Usage.Account, ev.Usage.AccountName) })
		}
	case agent.AuthExpired:
		h.mu.Lock()
		h.relogin = h.relogin || h.working
		h.mu.Unlock()
		h.appendItem(core.Item{ID: hostcore.NewID(6), Kind: core.ItemError, Text: "Signed out: " + ev.Detail, Time: now})
		x.LoginLost(h.id, ev)
	case agent.AuthRestored:
		if x.SignedOut(ev.Provider) {
			x.env.Go("a login check", func() { x.CheckLogin(ev.Provider, true) })
		}
	case agent.Exited:
		if ev.Err != "" {
			h.appendItem(core.Item{ID: hostcore.NewID(6), Kind: core.ItemSystem, Text: "The agent exited: " + ev.Err, Time: now})
		}
	}
}

// snapshot is the session's state now, or its zero value once it's gone.
func (h *session) snapshot() core.SessionState {
	se, _ := h.r.st.Session(h.id)
	return se
}

func (h *session) appendItem(it core.Item) {
	h.mu.Lock()
	h.items[it.ID] = it
	h.mu.Unlock()
	_ = h.r.st.Emit(core.TranscriptAppended{Session: h.id, Items: []core.Item{it}})
}

func (h *session) toolResult(ev agent.ToolResult, now time.Time) {
	h.mu.Lock()
	call := h.calls[ev.ID]
	it, ok := h.items[ev.ID]
	h.mu.Unlock()
	if !ok {
		it = core.Item{ID: ev.ID, Kind: core.ItemTool, Tool: call.Tool, Time: now}
	}
	it.Done, it.Output, it.IsError, it.Diff = true, clipOutput(ev.Output), ev.IsError, ev.Diff
	if !it.Time.IsZero() {
		it.Duration = now.Sub(it.Time).Milliseconds()
	}
	h.appendItem(it)
	if strings.HasSuffix(call.Tool, "test_run") {
		h.linkRun(ev)
	}
	if run, ok := testRunFrom(call, ev, now); ok {
		run.ID, run.By, run.Item = hostcore.NewID(4), "agent", ev.ID
		h.mu.Lock()
		if len(h.delivered) > 0 {
			run.Thread = h.delivered[0]
		}
		h.runs = append(h.runs, run.ID)
		h.mu.Unlock()
		_ = h.r.st.Emit(core.TestRunRecorded{Session: h.id, Run: run})
	}
	path, added, removed, ok := editOf(call, ev)
	if !ok {
		return
	}
	h.mu.Lock()
	threads := slices.Clone(h.delivered)
	h.mu.Unlock()
	for _, tid := range threads {
		_, _ = h.r.mutateThread(h.id, tid, func(t *core.Thread) error {
			t.Touched = touch(t.Touched, h.r.projectOf(h.id, path), path, added, removed)
			return nil
		})
	}
}

// linkRun ties the run a test_run call recorded to the call's transcript item, so the console can link it.
func (h *session) linkRun(ev agent.ToolResult) {
	m := citesRun.FindStringSubmatch(ev.Output)
	if m == nil {
		return
	}
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return
	}
	if r, ok := se.Run(m[1]); ok && r.Item == "" {
		r.Item = ev.ID
		_ = h.r.st.Emit(core.TestRunRecorded{Session: h.id, Run: r})
	}
}

// projectOf is the project a path belongs to.
func (x *Runner) projectOf(id core.SessionID, path string) string {
	se, err := x.st.Session(id)
	if err != nil {
		return ""
	}
	for _, p := range se.Projects {
		if rel, err := filepath.Rel(p.Dir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return p.Name
		}
	}
	return ""
}

func touch(ts []core.Touched, project, path string, added, removed int) []core.Touched {
	for i, t := range ts {
		if t.Path == path {
			ts[i].Added += added
			ts[i].Removed += removed
			return ts
		}
	}
	return append(ts, core.Touched{Project: project, Path: path, Added: added, Removed: removed})
}

func (h *session) subagent(ev agent.Subagent, now time.Time) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return
	}
	a := core.Subagent{ID: ev.ID, Description: ev.Description, Type: ev.Type, Prompt: ev.Prompt, Running: !ev.Done, Started: now}
	for _, old := range se.Subagents {
		if old.ID == ev.ID {
			a.Started, a.Tools, a.Threads = old.Started, old.Tools, old.Threads
			if a.Description == "" {
				a.Description, a.Type, a.Prompt = old.Description, old.Type, old.Prompt
			}
		}
	}
	if ev.Done {
		a.Ended, a.Result = now, ev.Result
	}
	if a.Threads == nil {
		h.mu.Lock()
		a.Threads = slices.Clone(h.delivered)
		h.mu.Unlock()
	}
	_ = h.r.st.Emit(core.SubagentChanged{Session: h.id, Subagent: a})
	hostcore.Restatus(h.r.st, h.id)
}

func (h *session) subagentTool(id string) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return
	}
	for _, a := range se.Subagents {
		if a.ID == id {
			a.Tools++
			_ = h.r.st.Emit(core.SubagentChanged{Session: h.id, Subagent: a})
		}
	}
}

func (h *session) turnEnd(ev agent.TurnEnd, now time.Time) {
	h.mu.Lock()
	h.working, h.interrupting = false, false
	started := h.turnAt
	delivered := append(h.held, h.delivered...)
	h.held, h.delivered = nil, nil
	if len(ev.Background) > 0 {
		h.held, delivered = delivered, nil
	}
	h.mu.Unlock()
	for _, tid := range delivered {
		_ = h.r.st.Mutate(h.id, func(core.SessionState) error { return h.r.st.Emit(core.ThreadBusy{Session: h.id, Thread: tid}) })
	}
	h.setAgent(func(a *core.AgentState) {
		a.Working = false
		a.Background = ev.Background
		a.Stats.Turns++
		u := ev.Usage
		a.Stats.Input += u.Input
		a.Stats.Output += u.Output
		a.Stats.CacheRead += u.CacheRead
		a.Stats.CacheWrite += u.CacheWrite
		a.Stats.Cost += u.Cost
		if u.Context > 0 {
			a.Stats.Context = u.Context
		}
		if u.ContextMax > 0 {
			a.Stats.ContextMax = u.ContextMax
		}
		if !started.IsZero() {
			a.Stats.Running += int64(now.Sub(started).Seconds())
		}
	})
	h.kick()
	if len(ev.Background) == 0 {
		h.idle()
	}
}

func clipOutput(s string) string {
	const max = 8000
	if len(s) <= max {
		return s
	}
	return s[:max/2] + "\n…\n" + s[len(s)-max/2:]
}

// toolTitle is a tool call in one line, as the console shows it: "Read internal/ratelimit/limiter.go".
func toolTitle(tool string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	name := tool
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		name = tool[i+2:]
	}
	switch tool {
	case "Bash", "commandExecution", "shell":
		if c := str("command"); c != "" {
			return "Bash " + firstLine(c)
		}
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		return tool + " " + str("file_path")
	case "Grep":
		return fmt.Sprintf("Search %q", str("pattern"))
	case "Glob":
		return "Find " + str("pattern")
	case "Task", "Agent":
		return "Agent " + str("description")
	case "WebFetch":
		return "Fetch " + str("url")
	case "fileChange":
		return "Edit " + str("path")
	}
	return name
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
