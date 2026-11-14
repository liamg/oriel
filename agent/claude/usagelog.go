package claude

import (
	"cmp"
	"encoding/json"
	"time"

	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/agent/usage"
)

// UsageLog is Claude Code's own log of a session, <config>/projects/<slug>/<id>.jsonl and its subagents', as usage.
var UsageLog = usage.Format{Name: "claude", Reader: func(json.RawMessage) usage.Reader { return logReader{} }}

// UsageFormats are the formats of the logs Logs names.
func (*Provider) UsageFormats() []usage.Format { return []usage.Format{UsageLog} }

type logReader struct{}

func (logReader) State() json.RawMessage { return nil }

// logRecord is the part of a Claude Code log line read here.
type logRecord struct {
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	Cwd         string    `json:"cwd"`
	Effort      string    `json:"effort"`
	IsSidechain bool      `json:"isSidechain"`
	IsMeta      bool      `json:"isMeta"`
	AgentID     string    `json:"agentId"`
	Message     struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *logUsage       `json:"usage"`
	} `json:"message"`
}

type logUsage struct {
	Input       int64  `json:"input_tokens"`
	CacheCreate int64  `json:"cache_creation_input_tokens"`
	CacheRead   int64  `json:"cache_read_input_tokens"`
	Output      int64  `json:"output_tokens"`
	Speed       string `json:"speed"`
	Creation    *struct {
		FiveMin int64 `json:"ephemeral_5m_input_tokens"`
		OneHour int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u *logUsage) tokens() price.Tokens {
	t := price.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead}
	if u.Creation != nil && (u.Creation.FiveMin > 0 || u.Creation.OneHour > 0) {
		t.CacheWrite5m, t.CacheWrite1h = u.Creation.FiveMin, u.Creation.OneHour
		return t
	}
	t.CacheWrite5m = u.CacheCreate // older logs don't say for how long: five minutes is the default
	return t
}

type logBlock struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Text      string          `json:"text"`
}

// Line reads one record. Claude writes one a content block, each assistant one with its message's usage as it then
// stood, so each request names its message (Event.Msg).
func (logReader) Line(line []byte, emit func(usage.Event)) {
	var r logRecord
	if json.Unmarshal(line, &r) != nil {
		return
	}
	at := r.Timestamp
	emit(usage.Event{At: at, Cwd: r.Cwd})
	if r.Type != "assistant" && r.Type != "user" {
		return
	}
	agent := ""
	if r.IsSidechain {
		agent = cmp.Or(r.AgentID, "subagent")
	}
	prompted := r.Type == "user" && !r.IsMeta && agent == ""
	var blocks []logBlock
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		var text string
		if prompted && json.Unmarshal(r.Message.Content, &text) == nil && usage.Prompt(text) {
			emit(usage.Event{At: at, Kind: usage.Turn, Text: text})
		}
		return
	}
	if prompted && len(blocks) > 0 && blocks[0].Type == "text" && usage.Prompt(blocks[0].Text) {
		emit(usage.Event{At: at, Kind: usage.Turn, Text: blocks[0].Text})
	}
	for _, b := range blocks {
		if b.Type == "tool_use" {
			emit(usage.Event{At: at, Kind: usage.Place, Name: b.Name, Input: b.Input})
		}
	}
	model := usage.ModelName(r.Message.Model)
	if g := r.Message.Usage; r.Type == "assistant" && g != nil {
		t := g.tokens()
		emit(usage.Event{At: at, Kind: usage.Request, Model: model, Effort: r.Effort, Agent: agent, Tokens: t, Fast: g.Speed == "fast",
			Msg: r.Message.ID, Window: transcript.ContextWindow(model, t.Prompt())})
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			emit(usage.Event{At: at, Kind: usage.Tool, ID: b.ID, Name: b.Name, Input: b.Input, Model: model, Effort: r.Effort})
		case "tool_result":
			emit(usage.Event{At: at, Kind: usage.Result, ID: b.ToolUseID, Error: b.IsError})
		}
	}
}
