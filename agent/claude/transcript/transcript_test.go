package transcript

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeLog puts a log in the project folder Claude Code names for cwd, dated so newest-first is under the test's
// control.
func writeLog(t *testing.T, config, cwd, id, body string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(config, "projects", Slug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
	return p
}

func logLine(cwd, text string) string {
	return `{"type":"assistant","cwd":"` + cwd + `","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

func read(t *testing.T, body string) (*Reader, []Entry) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Reader{Path: p}
	return r, r.Read()
}

func appendTo(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

func TestConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := ConfigDir(nil); got != filepath.Join(home, ".claude") {
		t.Errorf("ConfigDir = %q, want ~/.claude", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "/box/claude")
	if got := ConfigDir(nil); got != "/box/claude" {
		t.Errorf("ConfigDir = %q, want the environment's", got)
	}
	if got := ConfigDir([]string{"CLAUDE_CONFIG_DIR=/spec/claude"}); got != "/spec/claude" {
		t.Errorf("ConfigDir = %q, want the session's own", got)
	}
}

func TestSlug(t *testing.T) {
	if got := Slug("/Users/x/.oriel/a_b"); got != "-Users-x--oriel-a-b" {
		t.Errorf("Slug = %q", got)
	}
}

func TestSessionIDForCwdTakesTheNewest(t *testing.T) {
	config := t.TempDir()
	writeLog(t, config, "/w/a", "old", logLine("/w/a", "x"), time.Hour)
	writeLog(t, config, "/w/a", "new", logLine("/w/a", "y"), time.Minute)
	writeLog(t, config, "/w/a", "empty", "", 0)
	writeLog(t, config, "/w/ab", "neighbour", logLine("/w/ab", "z"), 0)
	if got := SessionIDForCwd(config, "/w/a"); got != "new" {
		t.Errorf("SessionIDForCwd = %q, want new", got)
	}
	if !HasSession(config, "/w/a", "old") || HasSession(config, "/w/a", "empty") || HasSession(config, "/w/a", "neighbour") {
		t.Error("HasSession wants a non-empty log in cwd's own project")
	}
}

func TestPathFindsALogByIDElsewhere(t *testing.T) {
	config := t.TempDir()
	want := writeLog(t, config, "/real/w", "s1", logLine("/real/w", "x"), 0)
	if got := Path(config, "/link/w", "s1"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if got := Path(config, "/w/new", "s2"); got != filepath.Join(config, "projects", "-w-new", "s2.jsonl") {
		t.Errorf("Path of a log not yet written = %q", got)
	}
}

// toolLog is a tool call and its result, as Claude Code writes them: two records, joined by the tool_use id.
const toolLog = `{"type":"assistant","uuid":"u1","timestamp":"2026-09-01T20:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"deciding"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -a","description":"List"}}]}}
{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"a\nb","is_error":false}]}}
`

func TestReadJoinsACallAndItsResult(t *testing.T) {
	_, got := read(t, logLine("/w", "hello")+toolLog)
	if len(got) != 4 {
		t.Fatalf("entries = %+v, want the text, the thinking, the call and its result", got)
	}
	if got[0].Kind != Text || got[0].Role != "assistant" || got[0].Text != "hello" {
		t.Errorf("text = %+v", got[0])
	}
	if got[1].Kind != Thinking || got[1].Text != "deciding" || got[1].ID != "u1:0" {
		t.Errorf("thinking = %+v", got[1])
	}
	c := got[2]
	if c.Kind != ToolUse || c.ID != "t1" || c.Tool != "Bash" || Summary(c.Input) != "List" {
		t.Errorf("call = %+v", c)
	}
	var in map[string]string
	if err := json.Unmarshal(c.Input, &in); err != nil || in["command"] != "ls -a" {
		t.Errorf("call input = %s", c.Input)
	}
	if r := got[3]; r.Kind != ToolResult || r.ID != "t1" || r.Tool != "Bash" || r.Text != "a\nb" || r.IsError {
		t.Errorf("result = %+v", r)
	}
}

func TestReadLeavesAPartialLineForLater(t *testing.T) {
	whole, half, _ := strings.Cut(toolLog, "\n")
	r, got := read(t, whole+"\n"+half[:20])
	if len(got) != 2 {
		t.Fatalf("entries = %+v, want the first record's two", got)
	}
	appendTo(t, r.Path, half[20:]+"\n")
	if got := r.Read(); len(got) != 1 || got[0].Kind != ToolResult {
		t.Fatalf("after the line was finished: %+v", got)
	}
	if got := r.Read(); got != nil {
		t.Fatalf("nothing new, read %+v", got)
	}
}

// usageLog is two requests: a tool call, then the reply after it. The second prompt is over the standard window,
// which is the only way a log says which window the model was called with.
const usageLog = `{"type":"assistant","effort":"high","timestamp":"2026-09-01T20:00:00Z","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}],"usage":{"input_tokens":10,"cache_creation_input_tokens":100,"cache_read_input_tokens":900,"output_tokens":50}}}
{"type":"assistant","effort":"high","timestamp":"2026-09-01T20:10:00Z","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":5,"cache_creation_input_tokens":1000,"cache_read_input_tokens":300000,"output_tokens":20}}}
`

func TestReadRecordsTheSessionFacts(t *testing.T) {
	r, _ := read(t, usageLog)
	u := r.Usage
	if r.Offset != int64(len(usageLog)) {
		t.Fatalf("offset = %d, want %d", r.Offset, len(usageLog))
	}
	if u.Requests != 2 || u.Tools != 1 {
		t.Errorf("requests/tools = %d/%d, want 2/1", u.Requests, u.Tools)
	}
	if u.Model != "claude-opus-5" || u.Effort != "high" {
		t.Errorf("model/effort = %q/%q", u.Model, u.Effort)
	}
	if u.Context != 301005 {
		t.Errorf("context = %d, want 301005", u.Context)
	}
	if u.Window != 1_000_000 {
		t.Errorf("window = %d, want 1000000: a prompt this big can't be a 200k call", u.Window)
	}
	if u.First.IsZero() || u.Last.Sub(u.First) != 10*time.Minute {
		t.Errorf("span = %v, want 10m", u.Last.Sub(u.First))
	}
	// Opus at $5 in, $25 out: 15 in, 1100 written for five minutes at 1.25×, 300900 read at 0.1×, 70 out.
	want := (15*5 + 1100*5*1.25 + 300900*5*0.1 + 70*25) / 1e6
	if math.Abs(u.Cost-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", u.Cost, want)
	}
}

func TestReadCountsAMessageOnce(t *testing.T) {
	// One response, written as a record per block, each carrying the message's usage.
	line := func(block string) string {
		return `{"type":"assistant","message":{"id":"m1","role":"assistant","model":"claude-sonnet-5","content":[` + block +
			`],"usage":{"input_tokens":2,"cache_creation_input_tokens":10,"cache_read_input_tokens":100,"output_tokens":7,"cache_creation":{"ephemeral_1h_input_tokens":10}}}}` + "\n"
	}
	r, got := read(t, line(`{"type":"thinking","thinking":"hm"}`)+line(`{"type":"text","text":"ok"}`))
	if len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}
	u := r.Usage
	if u.Requests != 1 || u.Input != 2 || u.Output != 7 || u.CacheCreate != 10 || u.CacheHour != 10 || u.CacheRead != 100 {
		t.Errorf("usage = %+v, want one request's", u)
	}
	want := (2*3 + 10*3*2.0 + 100*3*0.1 + 7*15) / 1e6
	if math.Abs(u.Cost-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", u.Cost, want)
	}
}

func TestReadKeepsTheLongWindowAfterACompaction(t *testing.T) {
	r, _ := read(t, usageLog)
	appendTo(t, r.Path, `{"type":"assistant","timestamp":"2026-09-01T20:20:00Z","message":{"role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":5,"cache_creation_input_tokens":20000,"cache_read_input_tokens":0,"output_tokens":10}}}`+"\n")
	r.Read()
	if r.Usage.Window != 1_000_000 {
		t.Errorf("window = %d, want 1000000: the window doesn't shrink with the prompt", r.Usage.Window)
	}
}

func TestContextWindowTiers(t *testing.T) {
	t.Setenv("ORIEL_CONTEXT_WINDOW", "")
	if got := ContextWindow("", 0); got != 0 {
		t.Errorf("no model = %d, want 0", got)
	}
	if got := ContextWindow("claude-opus-5", 50_000); got != 200_000 {
		t.Errorf("small prompt = %d, want 200000", got)
	}
	if got := ContextWindow("claude-opus-5", 400_000); got != 1_000_000 {
		t.Errorf("big prompt = %d, want 1000000", got)
	}
}

func TestContextWindowOverride(t *testing.T) {
	for in, want := range map[string]int64{"1m": 1_000_000, "500k": 500_000, "300000": 300_000, "lots": 200_000} {
		t.Setenv("ORIEL_CONTEXT_WINDOW", in)
		if got := ContextWindow("claude-opus-5", 1000); got != want {
			t.Errorf("%s = %d, want %d", in, got, want)
		}
	}
	t.Setenv("ORIEL_CONTEXT_WINDOW", "1m")
	if got := ContextWindow("", 1000); got != 0 {
		t.Errorf("no model = %d, want 0", got)
	}
}

func TestReadLetsTheOverrideBeatTheGuess(t *testing.T) {
	t.Setenv("ORIEL_CONTEXT_WINDOW", "200k")
	r, _ := read(t, usageLog)
	if r.Usage.Window != 200_000 {
		t.Errorf("window = %d, want the overridden 200000", r.Usage.Window)
	}
}

func TestReadTellsPluginPromptsFromTheUsers(t *testing.T) {
	_, got := read(t, `{"type":"user","uuid":"p1","origin":{"kind":"plugin","name":"crit"},"message":{"role":"user","content":"The crit plugin sent a message:\nNew crit comments."}}`+"\n"+
		`{"type":"user","uuid":"p2","message":{"role":"user","content":"typed by hand"}}`+"\n"+
		`{"type":"user","uuid":"p3","message":{"role":"user","content":[{"type":"text","text":"The crit plugin sent a message:\nNo origin on this one."}]}}`+"\n"+
		`{"type":"user","uuid":"p4","origin":{"kind":"plugin","name":"oriel","asUser":true},"message":{"role":"user","content":"say hello"}}`+"\n"+
		`{"type":"user","uuid":"p5","message":{"role":"user","content":"<command-name>/oriel:plan</command-name>"}}`+"\n")
	if len(got) != 4 {
		t.Fatalf("entries = %+v", got)
	}
	if e := got[0]; e.Kind != Plugin || e.Tool != "crit" || e.Text != "New crit comments." || e.ID != "p1:0" {
		t.Errorf("plugin entry = %+v", e)
	}
	if e := got[1]; e.Kind != Text || e.Role != "user" || e.Text != "typed by hand" {
		t.Errorf("typed entry = %+v", e)
	}
	if e := got[2]; e.Kind != Plugin || e.Tool != "crit" || e.Text != "No origin on this one." {
		t.Errorf("unmarked plugin entry = %+v", e)
	}
	if e := got[3]; e.Kind != Text || e.Role != "user" || e.Text != "say hello" {
		t.Errorf("a prompt submitted as the user's = %+v", e)
	}
}

func TestReadReadsPastAnOversizedRecord(t *testing.T) {
	huge := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"` +
		strings.Repeat("a", 2<<20) + `"}]}}` + "\n"
	_, got := read(t, logLine("/w", "before")+huge+logLine("/w", "after"))
	if len(got) != 3 || got[0].Text != "before" || len(got[1].Text) != 2<<20 || got[2].Text != "after" {
		t.Fatalf("entries either side of the big record went missing: %d entries", len(got))
	}
}

func TestReadNamesASubagentsEntries(t *testing.T) {
	_, got := read(t, `{"type":"assistant","isSidechain":true,"agentId":"a1","message":{"role":"assistant","content":[{"type":"text","text":"found it"}]}}`+"\n")
	if len(got) != 1 || got[0].Agent != "a1" {
		t.Fatalf("entries = %+v, want the subagent's", got)
	}
}

func TestSubagents(t *testing.T) {
	config := t.TempDir()
	p := writeLog(t, config, "/w", "s1", logLine("/w", "x"), 0)
	dir := filepath.Join(filepath.Dir(p), "s1", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"agent-a1.jsonl", "agent-a1.meta.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := Subagents(p); len(got) != 1 || filepath.Base(got[0]) != "agent-a1.jsonl" {
		t.Errorf("Subagents = %v", got)
	}
}

func TestReadDiffsAnEditFromItsPatch(t *testing.T) {
	_, got := read(t, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"/w/a.go","old_string":"b","new_string":"B"}}]}}`+"\n"+
		`{"type":"user","toolUseResult":{"filePath":"/w/a.go","oldString":"b","newString":"B","structuredPatch":[{"oldStart":1,"oldLines":3,"newStart":1,"newLines":3,"lines":[" a","-b","+B"," c"]}]},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"e1","content":"ok"}]}}`+"\n")
	d := got[1].Diff
	if d == nil || d.Path != "/w/a.go" || d.Added != 1 || d.Removed != 1 || len(d.Lines) != 4 {
		t.Fatalf("diff = %+v", d)
	}
	if l := d.Lines[1]; l.Op != "-" || l.Old != 2 || l.Text != "b" {
		t.Errorf("removed line = %+v", l)
	}
	if l := d.Lines[2]; l.Op != "+" || l.New != 2 || l.Text != "B" {
		t.Errorf("added line = %+v", l)
	}
}

func TestDiffFromTheInput(t *testing.T) {
	d := Diff("MultiEdit", json.RawMessage(`{"file_path":"/w/a.go","edits":[{"old_string":"a\nb","new_string":"c"}]}`), nil)
	if d == nil || d.Removed != 2 || d.Added != 1 {
		t.Fatalf("MultiEdit diff = %+v", d)
	}
	d = Diff("Write", json.RawMessage(`{"file_path":"/w/n.go","content":"x\ny\n"}`), json.RawMessage(`{"type":"create","filePath":"/w/n.go","content":"x\ny\n","structuredPatch":[]}`))
	if d == nil || d.Path != "/w/n.go" || d.Added != 2 || d.Removed != 0 {
		t.Fatalf("Write diff = %+v", d)
	}
	if Diff("Bash", json.RawMessage(`{"command":"ls"}`), nil) != nil {
		t.Error("a shell command has no diff")
	}
}

func TestQuestions(t *testing.T) {
	qs := Questions(json.RawMessage(`{"questions":[{"question":"Which colour?","header":"Colour","options":[{"label":"Red","description":"the warm one"},{"label":"Blue"}]},{"question":" "}]}`))
	if len(qs) != 1 {
		t.Fatalf("questions = %+v", qs)
	}
	if q := qs[0]; q.Question != "Which colour?" || q.Header != "Colour" || len(q.Options) != 2 || q.Options[0] != "Red" {
		t.Errorf("question = %+v", q)
	}
}

// AI-13: an API error Claude Code writes in the agent's place is marked with its type. The records are Claude Code
// 2.1.296's own (../testdata): a revoked login on a crit sandbox, and a send with no login at all.
func TestAPIErrors(t *testing.T) {
	for file, text := range map[string]string{
		"oauth-revoked.jsonl": "OAuth token revoked · Please run /login",
		"not-logged-in.jsonl": "Not logged in · Please run /login",
	} {
		b, err := os.ReadFile(filepath.Join("..", "testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		_, es := read(t, string(b))
		if len(es) != 1 || es[0].Kind != Text || es[0].Text != text || es[0].APIError != "authentication_failed" {
			t.Errorf("%s: entries = %+v", file, es)
		}
	}
	_, es := read(t, logLine("/w", "Please run /login"))
	if len(es) != 1 || es[0].APIError != "" {
		t.Errorf("the agent's own words: %+v", es)
	}
}
