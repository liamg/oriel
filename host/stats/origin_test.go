package stats

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/liamg/oriel/agent/codex"
)

// rollout is a line of a Codex rollout: {timestamp, type, payload}.
//
// ref:   codex/codex-rs/history/src/rollout_payload.rs
// quote: "#[serde(tag = \"type\", rename_all = \"snake_case\")] pub(super) enum RolloutItemWire<'a> { SessionMeta { payload: Cow<'a, SessionMetaLine>, },"
func rollout(at, typ, payload string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":%q,"payload":%s}`, at, typ, payload)
}

// tokenCount is a token_count event: the thread's running total, and the last response's.
//
// ref:   codex/codex-rs/protocol/src/protocol.rs
// quote: "pub struct TokenUsageInfo { pub total_token_usage: TokenUsage, pub last_token_usage: TokenUsage,"
func tokenCount(at string, in, cached, out int64) string {
	u := fmt.Sprintf(`{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"reasoning_output_tokens":0,"total_tokens":%d}`,
		in, cached, out, in+out)
	return rollout(at, "event_msg", `{"type":"token_count","info":{"total_token_usage":`+u+`,"last_token_usage":`+u+
		`,"model_context_window":272000},"rate_limits":null}`)
}

// codexLog is a rollout of two requests in /w/api: a turn, a shell call and its output, and a token_count after each
// response, one of them written twice.
func codexLog(t *testing.T) string {
	return write(t,
		rollout("2026-10-01T09:00:00.000Z", "session_meta", `{"session_id":"T1","id":"T1","timestamp":"2026-10-01T09:00:00.000Z","cwd":"/w/api","originator":"codex_cli_rs","cli_version":"0.50.0"}`),
		rollout("2026-10-01T09:00:01.000Z", "turn_context", `{"cwd":"/w/api","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"model":"gpt-5-codex","effort":"high","summary":"auto"}`),
		rollout("2026-10-01T09:00:01.000Z", "event_msg", `{"type":"user_message","message":"fix the flaky test","images":[]}`),
		rollout("2026-10-01T09:00:05.000Z", "response_item", `{"type":"function_call","name":"shell","arguments":"{\"command\":[\"go\",\"test\"]}","call_id":"call_1"}`),
		tokenCount("2026-10-01T09:00:05.000Z", 1_000_000, 400_000, 100_000),
		tokenCount("2026-10-01T09:00:05.500Z", 1_000_000, 400_000, 100_000),
		rollout("2026-10-01T09:00:09.000Z", "response_item", `{"type":"function_call_output","call_id":"call_1","output":"ok"}`),
		tokenCount("2026-10-01T09:00:12.000Z", 1_500_000, 600_000, 150_000),
	)
}

// A Codex rollout reads as the other logs do: each token_count is a response, its tokens the rise in the thread's
// total (a total repeated isn't a request), cached input priced as a cache read.
//
// ref:   codex/codex-rs/protocol/src/protocol.rs
// quote: "pub fn non_cached_input(&self) -> i64 { (self.input_tokens - self.cached_input()).max(0) }"
// ref:   cost-dev/models.json
// quote: pb_openai_gpt-5_direct_realtime_global_usd_20260814","verified_at":"2026-08-14T12:42:53Z","source_id":"source:openai-pricing"},"prices":{"cached_input_tokens_per_1000000":"0.125","input_tokens_per_1000000":"1.25","output_tokens_per_1000000":"10"}
func TestCodexRollout(t *testing.T) {
	s := New(codexLog(t), codex.UsageLog)
	s.Read()
	if s.Requests != 2 || s.Turns != 1 || s.ToolCalls != 1 || s.Cwd != "/w/api" || s.Title != "fix the flaky test" {
		t.Errorf("scan = %d requests, %d turns, %d tool calls, cwd %q, title %q", s.Requests, s.Turns, s.ToolCalls, s.Cwd, s.Title)
	}
	want := Tokens{Input: 900_000, CacheRead: 600_000, Output: 150_000}
	if s.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", s.Tokens, want)
	}
	// gpt-5: $1.25/Mtok in, $10 out, a cache read $0.125.
	if wantCost := (900_000*1.25 + 600_000*0.125 + 150_000*10) / 1e6; !near(s.Cost(), wantCost) {
		t.Errorf("cost = %v, want %v", s.Cost(), wantCost)
	}
	if s.Window != 272_000 || len(s.Slots) == 0 {
		t.Errorf("window = %d", s.Window)
	}
	for _, sl := range s.Slots {
		if sl.Requests > 0 && (sl.Model != "gpt-5-codex" || sl.Effort != "high") {
			t.Errorf("slot = %+v, want gpt-5-codex at high", sl)
		}
	}
}

// A rollout read in two goes counts each response once.
func TestCodexRolloutResumes(t *testing.T) {
	p := write(t,
		rollout("2026-10-01T09:00:01.000Z", "turn_context", `{"cwd":"/w/api","model":"gpt-5-codex","effort":"low"}`),
		tokenCount("2026-10-01T09:00:05.000Z", 1000, 0, 100),
	)
	s := New(p, codex.UsageLog)
	s.Read()
	appendTo(t, p, tokenCount("2026-10-01T09:00:09.000Z", 3000, 1000, 300))
	s.Read()
	if s.Requests != 2 || s.Tokens != (Tokens{Input: 2000, CacheRead: 1000, Output: 300}) {
		t.Errorf("after a resumed read: %d requests, %+v", s.Requests, s.Tokens)
	}
}

// A Claude log's title is its first prompt, not what the system wrapped in tags.
func TestTitleIsTheFirstPrompt(t *testing.T) {
	s := readNew(write(t,
		user("2026-09-01T10:00:00Z", `{"type":"text","text":"<command-name>/clear</command-name>"}`),
		user("2026-09-01T10:00:01Z", `{"type":"text","text":"rename the pricing helpers"}`),
		user("2026-09-01T10:00:02Z", `{"type":"text","text":"and their tests"}`),
	))
	if s.Title != "rename the pricing helpers" {
		t.Errorf("title = %q", s.Title)
	}
}

// Sessions from outside Oriel are counted with Oriel's, and the origin filters and groups them.
func TestOrigin(t *testing.T) {
	in, out := fixture(t, "a", "/ws/a"), fixture(t, "claude:b", "/ws/b")
	in.Origin, out.Origin = OriginOriel, OriginOutside
	all := Run(october, []Session{in, out})
	if all.Totals.Sessions != 2 || !near(all.Totals.Cost, 16) {
		t.Errorf("all = %+v", all.Totals)
	}
	if len(all.Origins) != 2 || all.Origins[0].Name != OriginOriel || !near(all.Origins[0].Cost, 8) || all.Origins[1].Sessions != 1 ||
		all.Origins[1].Tokens.Total() != 320_000 {
		t.Errorf("origins = %+v", all.Origins)
	}
	q := october
	q.Filters = map[string][]string{"origin": {OriginOutside}}
	if r := Run(q, []Session{in, out}); r.Totals.Sessions != 1 || !near(r.Totals.Cost, 8) {
		t.Errorf("outside only = %+v", r.Totals)
	}
	q = october
	q.Group = ByOrigin
	r := Run(q, []Session{in, out})
	if len(r.Groups) != 2 || r.Groups[0].Key != OriginOriel || r.Groups[0].Label != "Oriel" || r.Groups[1].Label != "outside Oriel" ||
		!near(r.Groups[1].Series[0], 5) {
		t.Errorf("by origin = %+v", r.Groups)
	}
	q.Group = BySession
	r = Run(q, []Session{in, out})
	got := map[string]Group{}
	for _, g := range r.Groups {
		got[g.Key] = g
	}
	if got["claude:b"].Origin != OriginOutside || got["a"].Origin != OriginOriel {
		t.Errorf("by session = %+v", r.Groups)
	}
	if d := Detailed(out, true, 0); d.Origin != OriginOutside || d.Name != "claude:b" {
		t.Errorf("detail = %q %q", d.Origin, d.Name)
	}
}

// A Codex apply_patch says where the session worked: each file its patch adds, updates or deletes.
//
// ref:   codex/codex-rs/apply-patch/src/parser.rs
// quote: "pub(crate) const ADD_FILE_MARKER: &str = \"*** Add File: \"; pub(crate) const DELETE_FILE_MARKER: &str = \"*** Delete File: \"; pub(crate) const UPDATE_FILE_MARKER: &str = \"*** Update File: \";"
func TestCodexPatchTouchesFiles(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: web/a.go\n@@\n-x\n+y\n*** Add File: /w/api/b.go\n+z\n*** End Patch\n"
	in, _ := json.Marshal(patch)
	args, _ := json.Marshal(map[string]string{"input": patch})
	argsIn, _ := json.Marshal(string(args))
	s := New(write(t,
		rollout("2026-10-01T09:00:00.000Z", "session_meta", `{"id":"T1","timestamp":"2026-10-01T09:00:00.000Z","cwd":"/w/api"}`),
		rollout("2026-10-01T09:00:01.000Z", "turn_context", `{"cwd":"/w/api","model":"gpt-5-codex","effort":"high"}`),
		rollout("2026-10-01T09:00:05.000Z", "response_item", `{"type":"custom_tool_call","name":"apply_patch","input":`+string(in)+`,"call_id":"c1"}`),
		rollout("2026-10-01T09:00:06.000Z", "response_item", `{"type":"function_call","name":"apply_patch","arguments":`+string(argsIn)+`,"call_id":"c2"}`),
	), codex.UsageLog)
	s.Read()
	if s.Paths["/w/api/web/a.go"] != 2 || s.Paths["/w/api/b.go"] != 2 || len(s.Paths) != 2 {
		t.Errorf("paths = %v", s.Paths)
	}
}
