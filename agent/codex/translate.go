package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/core"
)

// item is a ThreadItem, with the fields Oriel reads from each type.
type item struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	Text    string          `json:"text"`    // agentMessage
	Summary []string        `json:"summary"` // reasoning
	Content json.RawMessage `json:"content"` // reasoning: strings; userMessage: inputs

	Command          string  `json:"command"` // commandExecution
	Cwd              string  `json:"cwd"`
	Status           string  `json:"status"`
	AggregatedOutput *string `json:"aggregatedOutput"`
	ExitCode         *int    `json:"exitCode"`

	Changes []fileChange `json:"changes"` // fileChange

	Server    string          `json:"server"` // mcpToolCall, dynamicToolCall
	Tool      json.RawMessage `json:"tool"`   // a string; collabAgentToolCall's is its tool's name too
	Arguments json.RawMessage `json:"arguments"`
	Result    *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	ContentItems []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"contentItems"`
	Success *bool `json:"success"`

	Query string `json:"query"` // webSearch

	Receivers []string               `json:"receiverThreadIds"` // collabAgentToolCall
	Prompt    *string                `json:"prompt"`
	States    map[string]collabState `json:"agentsStates"`
	Kind      string                 `json:"kind"` // subAgentActivity
	AgentID   string                 `json:"agentThreadId"`
	AgentPath string                 `json:"agentPath"`
}

// collabState is a subagent's last known status, as a collab call reports it.
type collabState struct {
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

type fileChange struct {
	Path string `json:"path"`
	Kind struct {
		Type string `json:"type"` // add, delete, update
	} `json:"kind"`
	Diff string `json:"diff"`
}

type turnInfo struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message        string          `json:"message"`
		CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
	} `json:"error"`
	DurationMs *int64 `json:"durationMs"`
}

func (s *session) onNotify(method string, params json.RawMessage) {
	var n struct {
		ThreadID   string          `json:"threadId"`
		TurnID     string          `json:"turnId"`
		ItemID     string          `json:"itemId"`
		Delta      string          `json:"delta"`
		Item       item            `json:"item"`
		Turn       turnInfo        `json:"turn"`
		RateLimits *rateLimits     `json:"rateLimits"`
		TokenUsage *tokenUsage     `json:"tokenUsage"`
		Error      json.RawMessage `json:"error"`
		WillRetry  bool            `json:"willRetry"`
		AuthMode   *string         `json:"authMode"`
		Success    bool            `json:"success"`
	}
	if err := json.Unmarshal(params, &n); err != nil {
		return
	}
	switch method {
	case "account/rateLimits/updated":
		if n.RateLimits != nil {
			s.mu.Lock()
			s.limits = s.limits.merge(*n.RateLimits)
			u := s.limits.usage(time.Now())
			u.Account = s.acct.identity()
			u.AccountName = u.Account
			s.mu.Unlock()
			s.emit(agent.UsageUpdate{Usage: u})
		}
		return
	case "account/updated":
		s.mu.Lock()
		out := s.out
		s.mu.Unlock()
		if n.AuthMode != nil && out {
			s.signedIn()
		}
		return
	case "account/login/completed":
		if n.Success {
			s.signedIn()
		}
		return
	}
	if n.ThreadID != "" && n.ThreadID != s.ID() {
		s.mu.Lock()
		child := s.children[n.ThreadID]
		s.mu.Unlock()
		if child {
			s.onChild(method, n.ThreadID, n.Item)
			return
		}
		s.onFork(method, n.ThreadID, n.Item, n.Turn, n.Error, n.WillRetry)
		return
	}
	switch method {
	case "turn/started":
		s.mu.Lock()
		s.turn, s.failed, s.base = n.Turn.ID, false, s.total
		s.mu.Unlock()
		s.emit(agent.TurnStart{})
	case "turn/completed":
		s.turnDone(n.Turn)
	case "thread/tokenUsage/updated":
		if n.TokenUsage != nil {
			s.mu.Lock()
			s.total, s.last = n.TokenUsage.Total, n.TokenUsage.Last
			if n.TokenUsage.ModelContextWindow != nil {
				s.window = *n.TokenUsage.ModelContextWindow
			}
			s.mu.Unlock()
		}
	case "item/agentMessage/delta":
		s.stream(n.ItemID, core.ItemAssistant, n.Delta)
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		s.stream(n.ItemID, core.ItemThinking, n.Delta)
	case "item/started":
		s.itemStarted(n.Item, "")
	case "item/completed":
		s.itemCompleted(n.Item, "")
	case "error":
		if n.WillRetry {
			return
		}
		s.mu.Lock()
		s.failed = true
		s.mu.Unlock()
		s.failure(n.Error)
	}
}

// onChild passes on what a subagent the session spawned does, as its.
func (s *session) onChild(method, thread string, it item) {
	switch method {
	case "item/started":
		s.itemStarted(it, thread)
	case "item/completed":
		s.itemCompleted(it, thread)
	}
}

// signedIn is codex saying it's signed in: a login that had gone is seen back.
func (s *session) signedIn() {
	s.mu.Lock()
	s.out = false
	acct := s.acct.identity()
	s.mu.Unlock()
	s.emit(agent.AuthRestored{Provider: "codex", Account: acct})
}

type tokenUsage struct {
	Total              tokens `json:"total"`
	Last               tokens `json:"last"`
	ModelContextWindow *int64 `json:"modelContextWindow"`
}

func (s *session) turnDone(t turnInfo) {
	s.mu.Lock()
	s.turn, s.ended = "", t.ID
	s.history = true
	failed, out, model, prompt := s.failed, s.out, s.model, s.last.InputTokens
	cached := s.total.CachedInputTokens - s.base.CachedInputTokens
	u := agent.Usage{
		Input:      max(s.total.InputTokens-s.base.InputTokens-cached, 0), // Codex's input includes the cached part
		Output:     s.total.OutputTokens - s.base.OutputTokens,
		CacheRead:  cached,
		CacheWrite: s.total.CacheWriteInputTokens - s.base.CacheWriteInputTokens,
		Context:    int(s.last.TotalTokens),
		ContextMax: int(s.window),
	}
	clear(s.partial)
	clear(s.started)
	clear(s.changes)
	clear(s.calls)
	s.mu.Unlock()
	u.Cost = price.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite5m: u.CacheWrite}.
		Cost(s.prices.Book().RateFor(model, false, prompt))
	if t.DurationMs != nil {
		u.Duration = time.Duration(*t.DurationMs) * time.Millisecond
	}
	if t.Status == "failed" && !failed {
		if t.Error != nil {
			s.failure(jsonOf(t.Error))
		} else {
			s.emit(agent.Failure{Err: "the turn failed"})
		}
	}
	if out && t.Status == "completed" {
		s.signedIn()
	}
	s.emit(agent.TurnEnd{Usage: u, Running: s.terminals()})
}

// retryable are the errors worth trying the turn again for.
var retryable = map[string]bool{
	"serverOverloaded": true, "internalServerError": true, "httpConnectionFailed": true,
	"responseStreamConnectionFailed": true, "responseStreamDisconnected": true, "rateLimitExceeded": true,
}

// failure says a TurnError, and a 401's login gone with it.
func (s *session) failure(raw json.RawMessage) {
	msg, name := turnError(raw)
	s.emit(agent.Failure{Err: msg, Retryable: retryable[name]})
	if name == "unauthorized" {
		s.mu.Lock()
		s.out = true
		acct := s.acct.identity()
		s.mu.Unlock()
		s.emit(signedOut(acct, msg))
	}
}

// signedOut is codex's login gone: it signs in again from a shell, as it has no terminal of its own here.
func signedOut(account, detail string) agent.AuthExpired {
	return agent.AuthExpired{Provider: "codex", Account: account, Detail: detail, Shell: "codex login"}
}

// turnError reads a TurnError: its message, and its codexErrorInfo's name.
func turnError(raw json.RawMessage) (string, string) {
	var e struct {
		Message        string          `json:"message"`
		CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
	}
	json.Unmarshal(raw, &e)
	var name string
	if json.Unmarshal(e.CodexErrorInfo, &name) != nil {
		var obj map[string]json.RawMessage
		json.Unmarshal(e.CodexErrorInfo, &obj)
		for k := range obj {
			name = k
		}
	}
	if e.Message == "" {
		e.Message = "codex: the turn failed"
	}
	return e.Message, name
}

// stream passes on a message so far, whole each time.
func (s *session) stream(id string, role core.ItemKind, delta string) {
	s.mu.Lock()
	s.partial[id] += delta
	text := s.partial[id]
	s.mu.Unlock()
	s.emit(agent.Message{ID: id, Role: role, Text: text, Partial: true})
}

// str is a JSON string's value; "" for anything else.
func str(raw json.RawMessage) string {
	var v string
	json.Unmarshal(raw, &v)
	return v
}

// part is the id of a file change's i-th file: each file is a call of its own, so each keeps its diff.
func part(id string, i, n int) string {
	if n == 1 {
		return id
	}
	return fmt.Sprintf("%s#%d", id, i+1)
}

// calls are the ToolCalls an item makes: one for each file a change touches, else one or none.
func calls(it item, agentID string) []agent.ToolCall {
	c := agent.ToolCall{ID: it.ID, Agent: agentID}
	switch it.Type {
	case "commandExecution":
		c.Tool, c.Input = "Bash", jsonOf(map[string]any{"command": it.Command, "cwd": it.Cwd})
	case "fileChange":
		var out []agent.ToolCall
		for i, ch := range it.Changes {
			out = append(out, agent.ToolCall{ID: part(it.ID, i, len(it.Changes)), Tool: "Edit", Agent: agentID,
				Input: jsonOf(map[string]any{"file_path": ch.Path, "kind": ch.Kind.Type})})
		}
		return out
	case "mcpToolCall":
		c.Tool, c.Input = "mcp__"+it.Server+"__"+str(it.Tool), it.Arguments
	case "dynamicToolCall":
		c.Tool, c.Input = str(it.Tool), it.Arguments
	case "webSearch":
		c.Tool, c.Input = "WebSearch", jsonOf(map[string]any{"query": it.Query})
	case "collabAgentToolCall":
		if str(it.Tool) != "spawnAgent" {
			return nil
		}
		prompt := ""
		if it.Prompt != nil {
			prompt = *it.Prompt
		}
		c.Tool, c.Input = "Agent", jsonOf(map[string]any{"description": firstLine(prompt), "prompt": prompt})
	default:
		return nil
	}
	return []agent.ToolCall{c}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func (s *session) itemStarted(it item, agentID string) {
	cs := calls(it, agentID)
	s.mu.Lock()
	for _, c := range cs {
		s.started[c.ID] = true
	}
	switch it.Type {
	case "fileChange":
		s.changes[it.ID] = it.Changes
	case "mcpToolCall":
		s.calls[it.ID] = it
	}
	s.mu.Unlock()
	for _, c := range cs {
		s.emit(c)
	}
	s.subagents(it)
}

func (s *session) itemCompleted(it item, agentID string) {
	switch it.Type {
	case "agentMessage":
		s.emit(agent.Message{ID: it.ID, Role: core.ItemAssistant, Text: it.Text, Agent: agentID})
		return
	case "reasoning":
		text := strings.Join(it.Summary, "\n")
		if text == "" {
			var parts []string
			json.Unmarshal(it.Content, &parts)
			text = strings.Join(parts, "\n")
		}
		if text != "" {
			s.emit(agent.Message{ID: it.ID, Role: core.ItemThinking, Text: text, Agent: agentID})
		}
		return
	case "userMessage":
		if agentID != "" {
			return
		}
		var in []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		json.Unmarshal(it.Content, &in)
		var parts []string
		for _, i := range in {
			if i.Type == "text" {
				parts = append(parts, i.Text)
			}
		}
		s.emit(agent.Message{ID: it.ID, Role: core.ItemUser, Text: strings.Join(parts, "\n")})
		return
	}
	s.subagents(it)
	cs := calls(it, agentID)
	results := results(it)
	s.mu.Lock()
	var fresh []bool
	for _, c := range cs {
		fresh = append(fresh, !s.started[c.ID])
		delete(s.started, c.ID)
	}
	delete(s.changes, it.ID)
	delete(s.calls, it.ID)
	s.mu.Unlock()
	for i, c := range cs {
		if fresh[i] {
			s.emit(c)
		}
		if i < len(results) {
			s.emit(results[i])
		}
	}
}

// subagents are the Subagent events an item says: a spawn starts one, and a collab call's states finish them.
func (s *session) subagents(it item) {
	switch it.Type {
	case "collabAgentToolCall":
		prompt := ""
		if it.Prompt != nil {
			prompt = *it.Prompt
		}
		for _, id := range it.Receivers {
			s.mu.Lock()
			known := s.children[id]
			spawn := str(it.Tool) == "spawnAgent"
			if spawn {
				s.children[id] = true
			}
			s.mu.Unlock()
			if spawn && !known {
				s.emit(agent.Subagent{ID: id, Description: firstLine(prompt), Prompt: prompt})
			}
		}
		for id, st := range it.States {
			switch st.Status {
			case "completed", "errored", "interrupted", "shutdown", "notFound":
				res := st.Status
				if st.Message != nil && *st.Message != "" {
					res = *st.Message
				}
				s.emit(agent.Subagent{ID: id, Done: true, Result: res})
			}
		}
	case "subAgentActivity":
		if it.AgentID == "" {
			return
		}
		s.mu.Lock()
		s.children[it.AgentID] = true
		s.mu.Unlock()
		switch it.Kind {
		case "started":
			s.emit(agent.Subagent{ID: it.AgentID, Description: it.AgentPath})
		case "completed", "interrupted":
			s.emit(agent.Subagent{ID: it.AgentID, Done: true, Result: it.Kind})
		}
	}
}

// results are a finished item's ToolResults, one for each of its calls.
func results(it item) []agent.ToolResult {
	failed := it.Status == "failed" || it.Status == "declined"
	r := agent.ToolResult{ID: it.ID, IsError: failed}
	switch it.Type {
	case "commandExecution":
		if it.AggregatedOutput != nil {
			r.Output = *it.AggregatedOutput
		}
		if it.ExitCode != nil && *it.ExitCode != 0 {
			r.IsError = true
		}
		if it.Status == "declined" && r.Output == "" {
			r.Output = "declined"
		}
	case "fileChange":
		var out []agent.ToolResult
		for i, ch := range it.Changes {
			out = append(out, agent.ToolResult{ID: part(it.ID, i, len(it.Changes)), IsError: failed,
				Output: ch.Kind.Type + " " + ch.Path, Diff: parseDiff(ch.Path, ch.Kind.Type, ch.Diff)})
		}
		return out
	case "mcpToolCall":
		if it.Result != nil {
			var parts []string
			for _, c := range it.Result.Content {
				if c.Type == "text" {
					parts = append(parts, c.Text)
				}
			}
			r.Output = strings.Join(parts, "\n")
		}
		if it.Error != nil {
			r.Output, r.IsError = it.Error.Message, true
		}
	case "dynamicToolCall":
		var parts []string
		for _, c := range it.ContentItems {
			if c.Type == "inputText" {
				parts = append(parts, c.Text)
			}
		}
		r.Output = strings.Join(parts, "\n")
		if it.Success != nil && !*it.Success {
			r.IsError = true
		}
	case "webSearch":
		r.Output = it.Query
	case "collabAgentToolCall":
		var said []string
		for _, st := range it.States {
			if st.Message != nil {
				said = append(said, *st.Message)
			}
		}
		r.Output = strings.Join(said, "\n")
	}
	return []agent.ToolResult{r}
}

// parseDiff reads a file change's diff: a unified diff for an update, the file's text for an add or delete.
func parseDiff(path, kind, diff string) *core.FileDiff {
	if kind != "add" && kind != "delete" {
		return core.PatchDiff(path, diff)
	}
	d := &core.FileDiff{Path: path}
	if diff == "" {
		return d
	}
	op := "+"
	if kind == "delete" {
		op = "-"
	}
	for i, l := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		dl := core.DiffLine{Op: op, Text: l}
		if op == "+" {
			dl.New, d.Added = i+1, d.Added+1
		} else {
			dl.Old, d.Removed = i+1, d.Removed+1
		}
		d.Lines = append(d.Lines, dl)
	}
	return d
}

// onFork collects a fork's answer.
func (s *session) onFork(method, thread string, it item, t turnInfo, errRaw json.RawMessage, willRetry bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ft := s.forks[thread]
	if ft == nil {
		return
	}
	switch method {
	case "item/completed":
		if it.Type == "agentMessage" {
			ft.said = append(ft.said, it.Text)
		}
	case "error":
		if !willRetry {
			ft.err, _ = turnError(errRaw)
		}
	case "turn/completed":
		if t.Status == "failed" && ft.err == "" {
			ft.err = "codex: the side question failed"
			if t.Error != nil {
				ft.err = t.Error.Message
			}
		}
		select {
		case <-ft.done:
		default:
			close(ft.done)
		}
	}
}
