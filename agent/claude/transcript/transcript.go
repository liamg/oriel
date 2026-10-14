// Package transcript reads Claude Code's own session log, <config>/projects/<slug(cwd)>/<id>.jsonl, and its
// subagents' logs beside it: what the agent said and did, and what it used. It only ever reads.
package transcript

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// ConfigDir is Claude Code's config directory: CLAUDE_CONFIG_DIR from env (KEY=value), then from the process, else
// ~/.claude.
func ConfigDir(env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	if v := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".claude")
}

// Slug is Claude Code's name for a project's folder: the absolute path with every character that isn't a letter or
// a digit turned into '-' (/Users/x/.oriel/a → -Users-x--oriel-a).
func Slug(dir string) string {
	var sb strings.Builder
	for _, r := range dir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	return sb.String()
}

// Path is where session id's log is, or will be, for an agent started in cwd. A log already written under another
// project folder (a cwd that was a symlink, a path Claude shortened) is found by its id.
func Path(config, cwd, id string) string {
	want := filepath.Join(config, "projects", Slug(cwd), id+".jsonl")
	if _, err := os.Stat(want); err == nil {
		return want
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil && real != cwd {
		p := filepath.Join(config, "projects", Slug(real), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if m, _ := filepath.Glob(filepath.Join(config, "projects", "*", id+".jsonl")); len(m) > 0 {
		return m[0]
	}
	return want
}

// HasSession reports whether session id has a log that isn't empty under cwd's project: what `claude --resume <id>`
// started in cwd needs.
func HasSession(config, cwd, id string) bool {
	fi, err := os.Stat(filepath.Join(config, "projects", Slug(cwd), id+".jsonl"))
	return err == nil && fi.Size() > 0
}

// SessionIDForCwd is the most recent session whose project folder is cwd's own, or "" when there is none.
func SessionIDForCwd(config, cwd string) string {
	paths, _ := filepath.Glob(filepath.Join(config, "projects", Slug(cwd), "*.jsonl"))
	best, bestMod := "", time.Time{}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			continue
		}
		if fi.ModTime().After(bestMod) {
			best, bestMod = p, fi.ModTime()
		}
	}
	return strings.TrimSuffix(filepath.Base(best), ".jsonl")
}

// Subagents are the logs of the subagents a session started: <id>/subagents/agent-*.jsonl beside its own.
func Subagents(path string) []string {
	m, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-*.jsonl"))
	return m
}

// Kind is what an Entry is.
type Kind string

const (
	Text       Kind = "text"
	Thinking   Kind = "thinking"
	ToolUse    Kind = "tool_use"
	ToolResult Kind = "tool_result"
	Plugin     Kind = "plugin" // a prompt a plugin submitted
)

// Entry is one block of the log.
type Entry struct {
	Kind    Kind
	Role    string          // "assistant" or "user"
	ID      string          // "<record uuid>:<block>" for prose; the tool_use id for a call and its result
	Text    string          // prose, a result's text, or a plugin's prompt
	Tool    string          // the tool called, or the plugin that sent a prompt
	Input   json.RawMessage // a call's input
	IsError bool            // a result that is a failure
	Diff    *core.FileDiff  // what an edit's result did to its file
	Agent   string          // the subagent it belongs to; "" for the main loop
	At      time.Time
}

// record is the part of a log line read here.
type record struct {
	Type        string    `json:"type"`
	UUID        string    `json:"uuid"`
	IsMeta      bool      `json:"isMeta"`
	IsSidechain bool      `json:"isSidechain"`
	AgentID     string    `json:"agentId"`
	Timestamp   time.Time `json:"timestamp"`
	Effort      string    `json:"effort"`
	// Origin says who raised a user message: a plugin's prompt, not the user.
	Origin struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		AsUser bool   `json:"asUser"`
	} `json:"origin"`
	Message struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *usage          `json:"usage"`
	} `json:"message"`
	// ToolUseResult is what the tool itself returned, beside the text the agent was given: an edit's patch, the
	// answers to a question.
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type usage struct {
	Input         int64 `json:"input_tokens"`
	CacheCreate   int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheCreation struct {
		Hour int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	Speed string `json:"speed"`
}

// block is one content block of a message.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ID        string          `json:"id"`          // tool_use
	ToolUseID string          `json:"tool_use_id"` // tool_result
	Content   json.RawMessage `json:"content"`     // tool_result
	IsError   bool            `json:"is_error"`    // tool_result
}

// Usage is the token tally of a log.
type Usage struct {
	Input       int64 `json:"input"` // uncached input
	CacheCreate int64 `json:"cacheCreate"`
	CacheHour   int64 `json:"cacheHour,omitempty"` // the part of CacheCreate cached for an hour
	CacheRead   int64 `json:"cacheRead"`
	Output      int64 `json:"output"`
	Requests    int64 `json:"requests"`
	// Context is the prompt size of the latest request: how full the context window is.
	Context int64 `json:"context"`
	// Window is the model's context window, inferred (ContextWindow).
	Window int64   `json:"window,omitempty"`
	Cost   float64 `json:"cost"` // dollars, at list price: an estimate
	// Model and Effort are what the latest request ran as.
	Model  string    `json:"model,omitempty"`
	Effort string    `json:"effort,omitempty"`
	Tools  int64     `json:"tools"`
	First  time.Time `json:"first,omitzero"`
	Last   time.Time `json:"last,omitzero"`
}

// Context window tiers. The log names the model but not the window it was called with, and the million-token
// variants share their name with the 200k ones, so ContextWindow starts at the common tier and promotes.
const (
	windowStd  = 200_000
	windowLong = 1_000_000
)

// ContextWindow is the window a model with this much context in flight must have: over the standard tier it can
// only be the long one. ORIEL_CONTEXT_WINDOW ("1m", "200k" or a count) settles it.
func ContextWindow(model string, context int64) int64 {
	if model == "" {
		return 0
	}
	if n := envWindow(); n > 0 {
		return n
	}
	if context > windowStd {
		return windowLong
	}
	return windowStd
}

func envWindow() int64 {
	s := strings.ToLower(strings.TrimSpace(os.Getenv("ORIEL_CONTEXT_WINDOW")))
	if s == "" {
		return 0
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "m"):
		mult, s = 1_000_000, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "k"):
		mult, s = 1_000, strings.TrimSuffix(s, "k")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n * mult
}

// Reader reads a log as it grows: each Read returns what was appended since the last, and adds its usage to Usage.
type Reader struct {
	Path   string
	Offset int64 // always at a line boundary
	Usage  Usage

	calls   map[string]call // tool_use id → the call, for its result's diff
	lastMsg string          // the message whose usage was counted last
	lastUse usage
}

type call struct {
	name  string
	input json.RawMessage
}

// Read parses the complete lines appended since the last Read. A line still being written is left for the next.
func (r *Reader) Read() []Entry {
	f, err := os.Open(r.Path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	if fi.Size() < r.Offset {
		r.Offset = 0 // rewritten from scratch
	}
	if fi.Size() == r.Offset {
		return nil
	}
	if _, err := f.Seek(r.Offset, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil
	}
	r.Offset += int64(end) + 1
	var out []Entry
	for line := range bytes.SplitSeq(data[:end], []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		r.tally(rec)
		out = append(out, r.entries(rec)...)
	}
	return out
}

// tally adds a record's usage. Claude Code writes one record per content block, each carrying its message's usage,
// so a message is counted once, with the usage its latest record says.
func (r *Reader) tally(rec record) {
	u := &r.Usage
	if !rec.Timestamp.IsZero() {
		if u.First.IsZero() {
			u.First = rec.Timestamp
		}
		u.Last = rec.Timestamp
	}
	if rec.Type != "assistant" {
		return
	}
	var blocks []block
	if json.Unmarshal(rec.Message.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "tool_use" {
				u.Tools++
			}
		}
	}
	g := rec.Message.Usage
	if g == nil {
		return
	}
	if rec.Message.Model != "" {
		u.Model = rec.Message.Model
	}
	if rec.Effort != "" {
		u.Effort = rec.Effort
	}
	if rec.Message.ID != "" && rec.Message.ID == r.lastMsg {
		r.add(r.lastUse, -1)
	} else {
		u.Requests++
	}
	r.lastMsg, r.lastUse = rec.Message.ID, *g
	r.add(*g, 1)
	u.Context = g.Input + g.CacheCreate + g.CacheRead
	// Sticky: a compaction drops the prompt under the standard tier, but not the window it was called with.
	if n := envWindow(); n > 0 {
		u.Window = n
	} else {
		u.Window = max(u.Window, ContextWindow(u.Model, u.Context))
	}
}

func (r *Reader) add(g usage, sign int64) {
	u := &r.Usage
	u.Input += sign * g.Input
	u.CacheCreate += sign * g.CacheCreate
	u.CacheHour += sign * g.CacheCreation.Hour
	u.CacheRead += sign * g.CacheRead
	u.Output += sign * g.Output
	u.Cost += float64(sign) * Tokens{
		Input:        g.Input,
		CacheWrite5m: g.CacheCreate - g.CacheCreation.Hour,
		CacheWrite1h: g.CacheCreation.Hour,
		CacheRead:    g.CacheRead,
		Output:       g.Output,
	}.Cost(RateFor(u.Model, g.Speed == "fast"))
}

// entries turns one record into its blocks.
func (r *Reader) entries(rec record) []Entry {
	if rec.IsMeta || (rec.Type != "assistant" && rec.Type != "user") {
		return nil
	}
	agent := ""
	if rec.IsSidechain {
		agent = rec.AgentID
	}
	at := rec.Timestamp
	if name, text, ok := pluginOf(rec); ok {
		return []Entry{{Kind: Plugin, Role: "user", ID: ref(rec.UUID, 0), Text: text, Tool: name, Agent: agent, At: at}}
	}
	var s string
	if json.Unmarshal(rec.Message.Content, &s) == nil {
		if t := strings.TrimSpace(s); t != "" && !strings.HasPrefix(t, "<") {
			return []Entry{{Kind: Text, Role: rec.Type, ID: ref(rec.UUID, 0), Text: t, Agent: agent, At: at}}
		}
		return nil
	}
	var blocks []block
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return nil
	}
	var out []Entry
	for i, b := range blocks {
		switch b.Type {
		case "text":
			t := strings.TrimSpace(b.Text)
			// What the system injects into a user message (reminders, hook output) is wrapped in tags.
			if t == "" || (rec.Type == "user" && strings.HasPrefix(t, "<")) {
				continue
			}
			out = append(out, Entry{Kind: Text, Role: rec.Type, ID: ref(rec.UUID, i), Text: t, Agent: agent, At: at})
		case "thinking":
			if t := strings.TrimSpace(b.Thinking); t != "" {
				out = append(out, Entry{Kind: Thinking, Role: "assistant", ID: ref(rec.UUID, i), Text: t, Agent: agent, At: at})
			}
		case "tool_use":
			if r.calls == nil {
				r.calls = map[string]call{}
			}
			r.calls[b.ID] = call{name: b.Name, input: b.Input}
			out = append(out, Entry{Kind: ToolUse, Role: "assistant", ID: b.ID, Tool: b.Name, Input: b.Input, Agent: agent, At: at})
		case "tool_result":
			c := r.calls[b.ToolUseID]
			delete(r.calls, b.ToolUseID)
			e := Entry{Kind: ToolResult, Role: "user", ID: b.ToolUseID, Tool: c.name, Text: resultText(b.Content), IsError: b.IsError, Agent: agent, At: at}
			if !b.IsError {
				e.Diff = Diff(c.name, c.input, rec.ToolUseResult)
			}
			out = append(out, e)
		}
	}
	return out
}

func ref(uuid string, i int) string { return uuid + ":" + strconv.Itoa(i) }

// pluginSays is how Claude Code opens a prompt a plugin submits.
var pluginSays = regexp.MustCompile(`^The (\S+) plugin sent a message:\s*`)

// pluginOf says whether rec is a plugin's prompt, and names the plugin and what it said. Not every log marks one
// with its origin, so the wording it opens with counts too; one submitted as the user's own words is the user's.
func pluginOf(rec record) (name, text string, ok bool) {
	if rec.Type != "user" {
		return "", "", false
	}
	if json.Unmarshal(rec.Message.Content, &text) != nil {
		var blocks []block
		if json.Unmarshal(rec.Message.Content, &blocks) != nil || len(blocks) == 0 || blocks[0].Type != "text" {
			return "", "", false
		}
		text = blocks[0].Text
	}
	text = strings.TrimSpace(text)
	if m := pluginSays.FindStringSubmatch(text); m != nil {
		return m[1], strings.TrimPrefix(text, m[0]), true
	}
	if rec.Origin.Kind == "plugin" && !rec.Origin.AsUser {
		return rec.Origin.Name, text, true
	}
	return "", "", false
}

// resultText renders a tool_result body: a bare string, or a list of blocks (text, and the odd image).
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var inner []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &inner) == nil {
		var parts []string
		for _, p := range inner {
			if p.Text != "" {
				parts = append(parts, p.Text)
			} else if p.Type != "" {
				parts = append(parts, "["+p.Type+"]")
			}
		}
		if len(parts) > 0 {
			return strings.TrimSpace(strings.Join(parts, "\n"))
		}
	}
	return strings.TrimSpace(string(raw))
}

// Summary is the most readable part of a tool call's input: its description, command, path or query.
func Summary(input json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return string(input)
	}
	for _, k := range []string{"description", "command", "file_path", "notebook_path", "prompt", "query", "pattern", "url"} {
		if v, ok := in[k].(string); ok && v != "" {
			return v
		}
	}
	return string(input)
}
