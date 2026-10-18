// Package stats turns agents' session logs into numbers worth charting: tokens and cost by kind, model and effort,
// tool calls with their errors and timings, when the work happened, and which files it touched. It's crit's
// internal/stats, read per session: Claude Code's own log (FormatClaude), Codex's rollout (FormatCodex), or the record
// Oriel keeps of a provider's events (FormatEvents).
//
// Reading is incremental: a log is append-only, so a scan resumes at the byte the last one stopped at.
package stats

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/agent/price"
)

// Log formats.
const (
	FormatClaude = "claude" // <config>/projects/<slug>/<id>.jsonl and its subagents'
	FormatEvents = "events" // Oriel's record of a provider's events (Event)
	FormatCodex  = "codex"  // $CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl
)

// idleGap is the silence that ends a working stretch: anything longer isn't counted as active time.
const idleGap = 5 * time.Minute

// bucketSpan is the session chart's resolution.
const bucketSpan = 5 * time.Minute

// maxSamples caps the call durations a tool keeps for its median; past it they're thinned by half.
const maxSamples = 512

// bucketEdges are the upper bounds, in milliseconds, of all but the last duration bucket.
var bucketEdges = [...]int64{100, 1_000, 10_000, 60_000, 600_000}

// Buckets is how many duration buckets a tool has.
const Buckets = len(bucketEdges) + 1

func bucketOf(ms int64) int {
	for i, e := range bucketEdges {
		if ms < e {
			return i
		}
	}
	return len(bucketEdges)
}

// FileTools are the tools whose calls say where a session worked: a multi-repo session's cost splits by them.
var FileTools = map[string]bool{"Read": true, "Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true, "Grep": true,
	"Glob": true}

type (
	Tokens = price.Tokens
	Costs  = price.Costs
)

// Slot is an hour's requests on one model at one effort, with the tool calls they made: what a range sums.
type Slot struct {
	Hour       int64  `json:"hour"` // unix hours
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	Requests   int64  `json:"requests,omitempty"`
	Sidechain  int64  `json:"sidechain,omitempty"`
	Tokens     Tokens `json:"tokens,omitzero"`
	Costs      Costs  `json:"costs,omitzero"`
	ToolCalls  int64  `json:"toolCalls,omitempty"`
	ToolErrors int64  `json:"toolErrors,omitempty"`
	Turns      int64  `json:"turns,omitempty"`
	ActiveS    int64  `json:"activeS,omitempty"`
	// First and Last are the hour's first and last record, in unix seconds: kept on the slot with no model.
	First int64 `json:"first,omitempty"`
	Last  int64 `json:"last,omitempty"`
}

// ToolSlot is an hour's calls of one tool on one model, with the errors and timings of their results: what a range's
// tool rows sum.
type ToolSlot struct {
	Hour    int64          `json:"hour"`
	Model   string         `json:"model,omitempty"`
	Name    string         `json:"name"`
	Calls   int64          `json:"calls"`
	Errors  int64          `json:"errors,omitempty"`
	Buckets [Buckets]int64 `json:"buckets"`
}

// Bucket is five minutes of a session, for its chart.
type Bucket struct {
	At       int64  `json:"at"` // unix seconds, a multiple of bucketSpan
	Requests int64  `json:"requests,omitempty"`
	Turns    int64  `json:"turns,omitempty"`
	Tokens   Tokens `json:"tokens,omitzero"`
	Costs    Costs  `json:"costs,omitzero"`
	Context  int64  `json:"context,omitempty"` // the largest prompt in it
}

// Tool is one tool's record.
type Tool struct {
	Name    string         `json:"name"`
	Calls   int64          `json:"calls"`
	Errors  int64          `json:"errors,omitempty"`
	Millis  int64          `json:"millis,omitempty"` // the summed time of the calls that came back
	Timed   int64          `json:"timed,omitempty"`
	Buckets [Buckets]int64 `json:"buckets"`
	Samples []int64        `json:"samples,omitempty"` // durations in ms, for the median
	Every   int            `json:"every,omitempty"`   // one sample kept in every Every
	seen    int
}

// Compaction is the context dropping: the agent summarised its conversation to make room.
type Compaction struct {
	At   time.Time `json:"at"`
	From int64     `json:"from"`
	To   int64     `json:"to"`
}

// Stats is what a log has said so far.
type Stats struct {
	Requests   int64  `json:"requests"`
	Turns      int64  `json:"turns"`
	ToolCalls  int64  `json:"toolCalls"`
	ToolErrors int64  `json:"toolErrors"`
	Sidechain  int64  `json:"sidechain"`
	Tokens     Tokens `json:"tokens"`
	Costs      Costs  `json:"costs"`
	// Peak is the largest prompt the main loop sent, and Window the context window it ran in.
	Peak    int64     `json:"peak"`
	Context int64     `json:"context"` // the latest prompt
	Window  int64     `json:"window,omitempty"`
	First   time.Time `json:"first,omitzero"`
	Last    time.Time `json:"last,omitzero"`
	ActiveS int64     `json:"activeS"`
	Agents  []string  `json:"agents,omitempty"` // subagents that made requests

	Slots       map[string]*Slot     `json:"slots,omitempty"`     // by slotKey
	ToolSlots   map[string]*ToolSlot `json:"toolSlots,omitempty"` // by slotKey(hour, model, tool)
	Buckets     map[int64]*Bucket    `json:"buckets,omitempty"`
	Tools       map[string]*Tool     `json:"tools,omitempty"`
	Paths       map[string]int64     `json:"paths,omitempty"` // file tool calls by absolute path
	Compactions []Compaction         `json:"compactions,omitempty"`
}

// Cost is the dollars spent, at list price.
func (s *Stats) Cost() float64 { return s.Costs.Total() }

func slotKey(hour int64, model, effort string) string {
	return strconv.FormatInt(hour, 10) + "|" + model + "|" + effort
}

// Scan is the incremental state of reading one log: keep it and Read again for what's been appended.
type Scan struct {
	Path   string `json:"path"`
	Format string `json:"format"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	Cwd    string `json:"cwd,omitempty"`
	Title  string `json:"title,omitempty"` // its first prompt
	Stats

	// What a read in progress carries to the next: calls awaiting their results, and the message whose usage was
	// counted last (Claude writes one record per block, each with the message's usage).
	Pending map[string]Pending `json:"pending,omitempty"`
	LastMsg string             `json:"lastMsg,omitempty"`
	LastReq *request           `json:"lastReq,omitempty"`
	Codex   *codexState        `json:"codex,omitempty"`
}

// Pending is a tool call waiting for its result.
type Pending struct {
	Name  string    `json:"name"`
	At    time.Time `json:"at"`
	Model string    `json:"model,omitempty"`
	Key   string    `json:"key"`            // its slot
	Tool  string    `json:"tool,omitempty"` // its tool slot
}

// request is one request as it was counted, so a later record of the same message can take it back.
type request struct {
	At        time.Time `json:"at"`
	Model     string    `json:"model"`
	Effort    string    `json:"effort,omitempty"`
	Tokens    Tokens    `json:"tokens"`
	Costs     Costs     `json:"costs"`
	Sidechain bool      `json:"sidechain,omitempty"`
}

// New starts a scan of path in format.
func New(path, format string) *Scan {
	return &Scan{Path: path, Format: format}
}

func (s *Scan) ensure() {
	if s.Slots == nil {
		s.Slots = map[string]*Slot{}
	}
	if s.Buckets == nil {
		s.Buckets = map[int64]*Bucket{}
	}
	if s.ToolSlots == nil {
		s.ToolSlots = map[string]*ToolSlot{}
	}
	if s.Tools == nil {
		s.Tools = map[string]*Tool{}
	}
	if s.Paths == nil {
		s.Paths = map[string]int64{}
	}
	if s.Pending == nil {
		s.Pending = map[string]Pending{}
	}
}

// Read takes in everything appended since the last read, and reports whether there was anything. A file that has
// shrunk is read again from the start.
func (s *Scan) Read() bool {
	s.ensure()
	f, err := os.Open(s.Path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	if fi.Size() < s.Offset {
		*s = Scan{Path: s.Path, Format: s.Format}
		s.ensure()
	}
	s.Size = fi.Size()
	if fi.Size() <= s.Offset {
		return false
	}
	if _, err := f.Seek(s.Offset, io.SeekStart); err != nil {
		return false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return false // a write in flight
	}
	for line := range bytes.SplitSeq(data[:end], []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		switch s.Format {
		case FormatEvents:
			var ev Event
			if json.Unmarshal(line, &ev) == nil {
				s.event(ev)
			}
			continue
		case FormatCodex:
			var r rolloutLine
			if json.Unmarshal(line, &r) == nil {
				s.codex(&r)
			}
			continue
		}
		var r record
		if json.Unmarshal(line, &r) == nil {
			s.claude(&r)
		}
	}
	s.Offset += int64(end) + 1
	return true
}

// record is the part of a Claude Code log line read here.
type record struct {
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
		Usage   *usage          `json:"usage"`
	} `json:"message"`
}

type usage struct {
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

func (u *usage) tokens() Tokens {
	t := Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead}
	if u.Creation != nil && (u.Creation.FiveMin > 0 || u.Creation.OneHour > 0) {
		t.CacheWrite5m, t.CacheWrite1h = u.Creation.FiveMin, u.Creation.OneHour
		return t
	}
	t.CacheWrite5m = u.CacheCreate // older logs don't say for how long: five minutes is the default
	return t
}

type block struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Text      string          `json:"text"`
}

func (s *Scan) claude(r *record) {
	if r.Cwd != "" && s.Cwd == "" {
		s.Cwd = r.Cwd
	}
	s.clock(r.Timestamp)
	if r.Type != "assistant" && r.Type != "user" {
		return
	}
	agent := ""
	if r.IsSidechain {
		agent = cmpOr(r.AgentID, "subagent")
	}
	model := ModelName(r.Message.Model)
	var blocks []block
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		var text string
		if r.Type == "user" && !r.IsMeta && agent == "" && json.Unmarshal(r.Message.Content, &text) == nil && prompt(text) {
			s.prompted(r.Timestamp, text)
		}
		return
	}
	if r.Type == "user" && !r.IsMeta && agent == "" && len(blocks) > 0 && blocks[0].Type == "text" && prompt(blocks[0].Text) {
		s.prompted(r.Timestamp, blocks[0].Text)
	}
	if r.Type == "assistant" && r.Message.Usage != nil {
		g := r.Message.Usage
		rate := price.RateFor(model, g.Speed == "fast")
		s.request(r.Message.ID, request{At: r.Timestamp, Model: model, Effort: r.Effort, Tokens: g.tokens(), Costs: g.tokens().Of(rate),
			Sidechain: agent != ""}, agent, transcript.ContextWindow(model, g.tokens().Prompt()))
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			s.call(b.ID, b.Name, b.Input, r.Timestamp, model, r.Effort)
		case "tool_result":
			s.result(b.ToolUseID, b.IsError, r.Timestamp)
		}
	}
}

// prompted is a turn the user started with text: the first is the log's title.
func (s *Scan) prompted(at time.Time, text string) {
	s.turn(at)
	if s.Title == "" {
		s.Title = titleOf(text)
	}
}

// titleOf is a prompt's first line, cut to a title's length.
func titleOf(text string) string {
	t, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if r := []rune(t); len(r) > 80 {
		t = string(r[:79]) + "…"
	}
	return strings.TrimSpace(t)
}

// prompt says whether a user message's text is something the user said, not what the system wrapped in tags.
func prompt(text string) bool {
	t := strings.TrimSpace(text)
	return t != "" && !strings.HasPrefix(t, "<")
}

// Event is a line of Oriel's record of a provider's events: a turn starting, a request with its tokens, a tool
// call, or its result.
type Event struct {
	At     time.Time       `json:"at"`
	Kind   string          `json:"kind"` // turn, request, tool, result
	Model  string          `json:"model,omitempty"`
	Effort string          `json:"effort,omitempty"`
	Agent  string          `json:"agent,omitempty"` // a subagent's id
	Window int64           `json:"window,omitempty"`
	Tokens Tokens          `json:"tokens,omitzero"`
	ID     string          `json:"id,omitempty"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Error  bool            `json:"error,omitempty"`
	Cwd    string          `json:"cwd,omitempty"`
}

func (s *Scan) event(ev Event) {
	if ev.Cwd != "" && s.Cwd == "" {
		s.Cwd = ev.Cwd
	}
	s.clock(ev.At)
	model := ""
	if ev.Model != "" {
		model = ModelName(ev.Model)
	}
	switch ev.Kind {
	case "turn":
		if ev.Agent == "" {
			s.turn(ev.At)
		}
	case "request":
		model = ModelName(ev.Model)
		s.request("", request{At: ev.At, Model: model, Effort: ev.Effort, Tokens: ev.Tokens, Costs: ev.Tokens.Of(price.RateFor(model, false)),
			Sidechain: ev.Agent != ""}, ev.Agent, ev.Window)
	case "tool":
		s.call(ev.ID, ev.Name, ev.Input, ev.At, model, ev.Effort)
	case "result":
		s.result(ev.ID, ev.Error, ev.At)
	}
}

// clock moves the log's time on: its span and each hour's, and the active time (gaps up to idleGap) of the hour a gap ends in.
func (s *Scan) clock(at time.Time) {
	if at.IsZero() {
		return
	}
	if s.First.IsZero() || at.Before(s.First) {
		s.First = at
	}
	sl := s.slot(hourOf(at), "", "")
	if u := at.Unix(); sl.First == 0 || u < sl.First {
		sl.First = u
	}
	sl.Last = max(sl.Last, at.Unix())
	if !at.After(s.Last) {
		return // a record from before the latest: a tool's result written late
	}
	if !s.Last.IsZero() {
		if gap := at.Sub(s.Last); gap <= idleGap {
			secs := int64(gap / time.Second)
			s.ActiveS += secs
			sl.ActiveS += secs
		}
	}
	s.Last = at
}

func hourOf(at time.Time) int64 { return at.Unix() / 3600 }

func (s *Scan) slot(hour int64, model, effort string) *Slot {
	k := slotKey(hour, model, effort)
	sl := s.Slots[k]
	if sl == nil {
		sl = &Slot{Hour: hour, Model: model, Effort: effort}
		s.Slots[k] = sl
	}
	return sl
}

func (s *Scan) bucket(at time.Time) *Bucket {
	k := at.Unix() / int64(bucketSpan/time.Second) * int64(bucketSpan/time.Second)
	b := s.Buckets[k]
	if b == nil {
		b = &Bucket{At: k}
		s.Buckets[k] = b
	}
	return b
}

func (s *Scan) turn(at time.Time) {
	if at.IsZero() {
		return
	}
	s.Turns++
	s.slot(hourOf(at), "", "").Turns++
	s.bucket(at).Turns++
}

// request counts a request. One with the id of the request counted last replaces it: a later record of the same
// message, with its usage as it then stood.
func (s *Scan) request(id string, q request, agent string, window int64) {
	if q.At.IsZero() {
		return
	}
	if id != "" && id == s.LastMsg && s.LastReq != nil {
		s.apply(*s.LastReq, -1)
	} else {
		s.Requests++
		s.slot(hourOf(q.At), q.Model, q.Effort).Requests++
		s.bucket(q.At).Requests++
		if q.Sidechain {
			s.Sidechain++
			s.slot(hourOf(q.At), q.Model, q.Effort).Sidechain++
		}
	}
	s.LastMsg, s.LastReq = id, &q
	s.apply(q, 1)
	if agent != "" {
		if !contains(s.Agents, agent) {
			s.Agents = append(s.Agents, agent)
		}
		return
	}
	p := q.Tokens.Prompt()
	if s.Context > 40_000 && p < s.Context/2 {
		s.Compactions = append(s.Compactions, Compaction{At: q.At, From: s.Context, To: p})
	}
	s.Context = p
	s.Peak = max(s.Peak, p)
	s.Window = max(s.Window, window)
	if b := s.bucket(q.At); p > b.Context {
		b.Context = p
	}
}

func (s *Scan) apply(q request, k int64) {
	s.Tokens = s.Tokens.Add(q.Tokens, k)
	s.Costs = s.Costs.Add(q.Costs, float64(k))
	sl := s.slot(hourOf(q.At), q.Model, q.Effort)
	sl.Tokens = sl.Tokens.Add(q.Tokens, k)
	sl.Costs = sl.Costs.Add(q.Costs, float64(k))
	b := s.bucket(q.At)
	b.Tokens = b.Tokens.Add(q.Tokens, k)
	b.Costs = b.Costs.Add(q.Costs, float64(k))
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *Scan) call(id, name string, input json.RawMessage, at time.Time, model, effort string) {
	if name == "" {
		name = "(unnamed)"
	}
	s.ToolCalls++
	t := s.tool(name)
	t.Calls++
	key, tkey := "", ""
	if !at.IsZero() {
		sl := s.slot(hourOf(at), model, effort)
		sl.ToolCalls++
		key = slotKey(sl.Hour, model, effort)
		tkey = slotKey(sl.Hour, model, name)
		ts := s.ToolSlots[tkey]
		if ts == nil {
			ts = &ToolSlot{Hour: sl.Hour, Model: model, Name: name}
			s.ToolSlots[tkey] = ts
		}
		ts.Calls++
	}
	if id != "" {
		s.Pending[id] = Pending{Name: name, At: at, Model: model, Key: key, Tool: tkey}
	}
	if FileTools[name] {
		for _, p := range paths(input, s.Cwd) {
			s.Paths[p]++
		}
	}
}

func (s *Scan) result(id string, isErr bool, at time.Time) {
	call, ok := s.Pending[id]
	if !ok {
		return
	}
	delete(s.Pending, id)
	t := s.tool(call.Name)
	ts := s.ToolSlots[call.Tool]
	if isErr {
		t.Errors++
		s.ToolErrors++
		if sl := s.Slots[call.Key]; sl != nil {
			sl.ToolErrors++
		}
		if ts != nil {
			ts.Errors++
		}
	}
	if call.At.IsZero() || at.IsZero() {
		return
	}
	if d := at.Sub(call.At); d > 0 {
		ms := d.Milliseconds()
		t.Millis += ms
		t.Timed++
		t.Buckets[bucketOf(ms)]++
		if ts != nil {
			ts.Buckets[bucketOf(ms)]++
		}
		t.sample(ms)
	}
}

func (t *Tool) sample(ms int64) {
	if t.Every < 1 {
		t.Every = 1
	}
	t.seen++
	if t.seen%t.Every != 0 {
		return
	}
	t.Samples = append(t.Samples, ms)
	if len(t.Samples) < maxSamples {
		return
	}
	kept := t.Samples[:0]
	for i, v := range t.Samples {
		if i%2 == 0 {
			kept = append(kept, v)
		}
	}
	t.Samples = kept
	t.Every *= 2
}

func (s *Scan) tool(name string) *Tool {
	t := s.Tools[name]
	if t == nil {
		t = &Tool{Name: name}
		s.Tools[name] = t
	}
	return t
}

// paths are the files a tool call's input names, made absolute against cwd.
func paths(input json.RawMessage, cwd string) []string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Path         string `json:"path"`
		Files        []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		if !filepath.IsAbs(p) {
			if cwd == "" {
				return
			}
			p = filepath.Join(cwd, p)
		}
		out = append(out, filepath.Clean(p))
	}
	add(in.FilePath)
	add(in.NotebookPath)
	add(in.Path)
	for _, f := range in.Files {
		add(f.Path)
	}
	if len(out) == 0 && cwd != "" {
		out = append(out, filepath.Clean(cwd)) // a search with no path is of where the agent works
	}
	return out
}

// ModelName is the model id with any deployment suffix cut off: the million-token variant is the same model.
func ModelName(id string) string {
	if id == "" {
		return "unknown"
	}
	if i := strings.IndexByte(id, '['); i > 0 {
		id = id[:i]
	}
	return strings.TrimSpace(id)
}

func cmpOr(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
