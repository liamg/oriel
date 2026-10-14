package codex

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
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
	Tool      string          `json:"tool"`
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
		RequestID  json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &n); err != nil {
		return
	}
	if method == "account/rateLimits/updated" && n.RateLimits != nil {
		s.mu.Lock()
		s.limits = s.limits.merge(*n.RateLimits)
		u := s.limits.usage(time.Now())
		u.Account = s.acct.identity()
		u.AccountName = u.Account
		s.mu.Unlock()
		s.emit(agent.UsageUpdate{Usage: u})
		return
	}
	if n.ThreadID != "" && n.ThreadID != s.ID() {
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
		s.itemStarted(n.Item)
	case "item/completed":
		s.itemCompleted(n.Item)
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

type tokenUsage struct {
	Total              tokens `json:"total"`
	Last               tokens `json:"last"`
	ModelContextWindow *int64 `json:"modelContextWindow"`
}

func (s *session) turnDone(t turnInfo) {
	s.mu.Lock()
	s.turn = ""
	s.history = true
	failed := s.failed
	u := agent.Usage{
		Input:      s.total.InputTokens - s.base.InputTokens,
		Output:     s.total.OutputTokens - s.base.OutputTokens,
		CacheRead:  s.total.CachedInputTokens - s.base.CachedInputTokens,
		CacheWrite: s.total.CacheWriteInputTokens - s.base.CacheWriteInputTokens,
		Context:    int(s.last.TotalTokens),
		ContextMax: int(s.window),
	}
	clear(s.partial)
	clear(s.started)
	clear(s.changes)
	s.mu.Unlock()
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
	s.emit(agent.TurnEnd{Usage: u})
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

// call is the ToolCall an item makes, if it is one.
func call(it item) (agent.ToolCall, bool) {
	c := agent.ToolCall{ID: it.ID}
	switch it.Type {
	case "commandExecution":
		c.Tool, c.Input = "Bash", jsonOf(map[string]any{"command": it.Command, "cwd": it.Cwd})
	case "fileChange":
		var files []map[string]string
		for _, ch := range it.Changes {
			files = append(files, map[string]string{"path": ch.Path, "kind": ch.Kind.Type})
		}
		c.Tool, c.Input = "Edit", jsonOf(map[string]any{"files": files})
	case "mcpToolCall":
		c.Tool, c.Input = "mcp__"+it.Server+"__"+it.Tool, it.Arguments
	case "dynamicToolCall":
		c.Tool, c.Input = it.Tool, it.Arguments
	case "webSearch":
		c.Tool, c.Input = "WebSearch", jsonOf(map[string]any{"query": it.Query})
	default:
		return c, false
	}
	return c, true
}

func (s *session) itemStarted(it item) {
	c, ok := call(it)
	if !ok {
		return
	}
	s.mu.Lock()
	s.started[it.ID] = true
	if it.Type == "fileChange" {
		s.changes[it.ID] = it.Changes
	}
	s.mu.Unlock()
	s.emit(c)
}

func (s *session) itemCompleted(it item) {
	switch it.Type {
	case "agentMessage":
		s.emit(agent.Message{ID: it.ID, Role: core.ItemAssistant, Text: it.Text})
		return
	case "reasoning":
		text := strings.Join(it.Summary, "\n")
		if text == "" {
			var parts []string
			json.Unmarshal(it.Content, &parts)
			text = strings.Join(parts, "\n")
		}
		if text != "" {
			s.emit(agent.Message{ID: it.ID, Role: core.ItemThinking, Text: text})
		}
		return
	case "userMessage":
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
	c, ok := call(it)
	if !ok {
		return
	}
	s.mu.Lock()
	started := s.started[it.ID]
	delete(s.started, it.ID)
	delete(s.changes, it.ID)
	s.mu.Unlock()
	if !started {
		s.emit(c)
	}
	s.emit(result(it))
}

// result is a finished item's ToolResult.
func result(it item) agent.ToolResult {
	r := agent.ToolResult{ID: it.ID, IsError: it.Status == "failed" || it.Status == "declined"}
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
		var paths []string
		for _, ch := range it.Changes {
			paths = append(paths, ch.Kind.Type+" "+ch.Path)
		}
		r.Output = strings.Join(paths, "\n")
		if len(it.Changes) > 0 {
			ch := it.Changes[0]
			r.Diff = parseDiff(ch.Path, ch.Kind.Type, ch.Diff)
		}
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
	}
	return r
}

// parseDiff reads a file change's diff: a unified diff for an update, the file's text for an add or delete.
func parseDiff(path, kind, diff string) *core.FileDiff {
	d := &core.FileDiff{Path: path}
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	if diff == "" {
		return d
	}
	if kind == "add" || kind == "delete" {
		op := "+"
		if kind == "delete" {
			op = "-"
		}
		for i, l := range lines {
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
	oldN, newN := 0, 0
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "@@"):
			oldN, newN = hunkStart(l)
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"), strings.HasPrefix(l, `\`):
		case strings.HasPrefix(l, "+"):
			d.Lines = append(d.Lines, core.DiffLine{Op: "+", New: newN, Text: l[1:]})
			d.Added++
			newN++
		case strings.HasPrefix(l, "-"):
			d.Lines = append(d.Lines, core.DiffLine{Op: "-", Old: oldN, Text: l[1:]})
			d.Removed++
			oldN++
		default:
			d.Lines = append(d.Lines, core.DiffLine{Op: " ", Old: oldN, New: newN, Text: strings.TrimPrefix(l, " ")})
			oldN++
			newN++
		}
	}
	return d
}

// hunkStart reads "@@ -a,b +c,d @@" as a and c.
func hunkStart(h string) (int, int) {
	var oldN, newN int
	for _, f := range strings.Fields(h) {
		n, _, _ := strings.Cut(f[1:], ",")
		v, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		switch f[0] {
		case '-':
			oldN = v
		case '+':
			newN = v
		}
	}
	return oldN, newN
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
