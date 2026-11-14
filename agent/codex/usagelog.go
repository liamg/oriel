package codex

import (
	"cmp"
	"encoding/json"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/agent/usage"
)

// UsageLog is a Codex rollout, $CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl, as usage: one {timestamp, type,
// payload} a line. Read here: the session's cwd (session_meta), each turn's model and effort (turn_context), the
// user's messages, tool calls and their outputs, and a token_count after each response with the thread's running
// total.
var UsageLog = usage.Format{Name: "codex", Reader: func(state json.RawMessage) usage.Reader {
	r := &logReader{}
	if state != nil {
		_ = json.Unmarshal(state, &r.st)
	}
	return r
}}

// UsageFormats are the formats of the logs Logs names.
func (*Provider) UsageFormats() []usage.Format { return []usage.Format{UsageLog} }

type rolloutLine struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type rolloutPayload struct {
	Type    string `json:"type"`
	Cwd     string `json:"cwd"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Message string `json:"message"`
	Name    string `json:"name"`
	CallID  string `json:"call_id"`
	Args    string `json:"arguments"`
	Input   string `json:"input"`
	Info    *struct {
		Total  rolloutUsage `json:"total_token_usage"`
		Window int64        `json:"model_context_window"`
	} `json:"info"`
}

type rolloutUsage struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
}

// rolloutState is what a rollout's read carries to the next: the turn's model and effort, and the total so far.
type rolloutState struct {
	Model  string       `json:"model,omitempty"`
	Effort string       `json:"effort,omitempty"`
	Total  rolloutUsage `json:"total"`
}

type logReader struct{ st rolloutState }

func (r *logReader) State() json.RawMessage {
	b, _ := json.Marshal(r.st)
	return b
}

func (r *logReader) Line(line []byte, emit func(usage.Event)) {
	var l rolloutLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	var p rolloutPayload
	if json.Unmarshal(l.Payload, &p) != nil {
		return
	}
	st, at := &r.st, l.Timestamp
	switch l.Type {
	case "session_meta", "turn_context":
		emit(usage.Event{At: at, Cwd: p.Cwd})
		if l.Type == "turn_context" {
			st.Model, st.Effort = usage.ModelName(p.Model), p.Effort
		}
		return
	}
	emit(usage.Event{At: at})
	switch l.Type {
	case "event_msg":
		switch p.Type {
		case "user_message":
			if usage.Prompt(p.Message) {
				emit(usage.Event{At: at, Kind: usage.Turn, Text: p.Message})
			}
		case "token_count":
			if p.Info == nil {
				return
			}
			t, last := p.Info.Total, st.Total
			if t.Input < last.Input || t.Output < last.Output {
				last = rolloutUsage{} // a total that fell starts again
			}
			in, cached, out := t.Input-last.Input, t.Cached-last.Cached, t.Output-last.Output
			if in == 0 && out == 0 {
				return
			}
			st.Total = t
			tk := price.Tokens{Input: max(in-cached, 0), CacheRead: max(cached, 0), Output: out}
			emit(usage.Event{At: at, Kind: usage.Request, Model: cmp.Or(st.Model, "unknown"), Effort: st.Effort, Tokens: tk, Window: p.Info.Window})
		}
	case "response_item":
		switch p.Type {
		case "function_call", "custom_tool_call":
			in := p.Args
			if p.Type == "custom_tool_call" {
				in = p.Input
			}
			var raw json.RawMessage
			if json.Valid([]byte(in)) {
				raw = json.RawMessage(in)
			}
			if p.Name == "apply_patch" {
				raw = patchFiles(p.Input, raw)
			}
			emit(usage.Event{At: at, Kind: usage.Tool, ID: p.CallID, Name: p.Name, Input: raw, Model: st.Model, Effort: st.Effort})
		case "function_call_output", "custom_tool_call_output":
			emit(usage.Event{At: at, Kind: usage.Result, ID: p.CallID})
		}
	}
}

// patchFiles is an apply_patch call as the files it touches ({"files": [{"path": …}]}): its patch is the freeform
// call's input, or the input in a function call's arguments.
func patchFiles(input string, args json.RawMessage) json.RawMessage {
	if input == "" {
		var a struct {
			Input string `json:"input"`
		}
		json.Unmarshal(args, &a)
		input = a.Input
	}
	type file struct {
		Path string `json:"path"`
	}
	var files []file
	for line := range strings.Lines(input) {
		for _, marker := range []string{"*** Add File: ", "*** Delete File: ", "*** Update File: "} {
			if p, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), marker); ok && strings.TrimSpace(p) != "" {
				files = append(files, file{Path: strings.TrimSpace(p)})
			}
		}
	}
	b, _ := json.Marshal(map[string]any{"files": files})
	return b
}
