package stats

import (
	"encoding/json"
	"time"

	"github.com/liamg/oriel/agent/price"
)

// A Codex rollout: one {timestamp, type, payload} a line. Read here: the session's cwd (session_meta), each turn's
// model and effort (turn_context), the user's messages, tool calls and their outputs, and a token_count after each
// response with the thread's running total.

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
		Total  codexUsage `json:"total_token_usage"`
		Window int64      `json:"model_context_window"`
	} `json:"info"`
}

type codexUsage struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Output int64 `json:"output_tokens"`
}

// codexState is what a rollout's read carries to the next: the turn's model and effort, and the total so far.
type codexState struct {
	Model  string     `json:"model,omitempty"`
	Effort string     `json:"effort,omitempty"`
	Total  codexUsage `json:"total"`
}

func (s *Scan) codex(r *rolloutLine) {
	var p rolloutPayload
	if json.Unmarshal(r.Payload, &p) != nil {
		return
	}
	if s.Codex == nil {
		s.Codex = &codexState{}
	}
	st := s.Codex
	s.clock(r.Timestamp)
	switch r.Type {
	case "session_meta":
		if s.Cwd == "" {
			s.Cwd = p.Cwd
		}
	case "turn_context":
		if p.Cwd != "" && s.Cwd == "" {
			s.Cwd = p.Cwd
		}
		st.Model, st.Effort = ModelName(p.Model), p.Effort
	case "event_msg":
		switch p.Type {
		case "user_message":
			if prompt(p.Message) {
				s.prompted(r.Timestamp, p.Message)
			}
		case "token_count":
			if p.Info == nil {
				return
			}
			t, last := p.Info.Total, st.Total
			if t.Input < last.Input || t.Output < last.Output {
				last = codexUsage{} // a total that fell starts again
			}
			in, cached, out := t.Input-last.Input, t.Cached-last.Cached, t.Output-last.Output
			if in == 0 && out == 0 {
				return
			}
			st.Total = t
			tk := Tokens{Input: max(in-cached, 0), CacheRead: max(cached, 0), Output: out}
			model := cmpOr(st.Model, "unknown")
			s.request("", request{At: r.Timestamp, Model: model, Effort: st.Effort, Tokens: tk, Costs: tk.Of(price.RateFor(model, false))},
				"", p.Info.Window)
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
			s.call(p.CallID, p.Name, raw, r.Timestamp, st.Model, st.Effort)
		case "function_call_output", "custom_tool_call_output":
			s.result(p.CallID, false, r.Timestamp)
		}
	}
}
