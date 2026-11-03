package stats

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/price"
)

// asst is an assistant record of Claude Code's log: a message with its usage and content blocks.
func asst(at, model, effort, body string, in, out, read, w5, w1 int64) string {
	return asstID("", at, model, effort, body, in, out, read, w5, w1)
}

func asstID(id, at, model, effort, body string, in, out, read, w5, w1 int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"effort":%q,"cwd":"/w/demo","message":{"id":%q,"role":"assistant","model":%q,`+
		`"content":[%s],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,`+
		`"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`, at, effort, id, model, body, in, out, read,
		w5+w1, w5, w1)
}

func user(at, body string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":"/w/demo","message":{"role":"user","content":[%s]}}`, at, body)
}

func write(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sess.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func appendTo(t *testing.T, p string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScanCountsTokensToolsAndCost(t *testing.T) {
	p := write(t,
		user("2026-09-01T10:00:00Z", `{"type":"text","text":"do the thing"}`),
		asst("2026-09-01T10:00:02Z", "claude-opus-5", "high",
			`{"type":"thinking","thinking":"hmm"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}`,
			100, 200, 1_000_000, 0, 50_000),
		user("2026-09-01T10:00:05Z", `{"type":"tool_result","tool_use_id":"t1"}`),
		asst("2026-09-01T10:00:07Z", "claude-opus-5", "high", `{"type":"text","text":"done"}`, 10, 20, 2_000_000, 1000, 0),
	)
	s := New(p, FormatClaude)
	s.Read()
	if s.Requests != 2 || s.Turns != 1 {
		t.Errorf("requests %d, turns %d: want 2 and 1", s.Requests, s.Turns)
	}
	if s.ToolCalls != 1 || s.ToolErrors != 0 {
		t.Errorf("tools = %d calls / %d errors, want 1/0", s.ToolCalls, s.ToolErrors)
	}
	want := Tokens{Input: 110, Output: 220, CacheRead: 3_000_000, CacheWrite5m: 1000, CacheWrite1h: 50_000}
	if s.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", s.Tokens, want)
	}
	// Opus 5 at its list prices.
	// ref:   cost-dev/models.json
	// quote: pb_anthropic_claude-opus-5_direct_realtime_global_usd_20260724","verified_at":"2026-07-24T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"10","cache_write_5m_tokens_per_1000000":"6.25","cached_input_tokens_per_1000000":"0.5","input_tokens_per_1000000":"5","output_tokens_per_1000000":"25"}
	wantCost := (110*5 + 1000*6.25 + 50_000*10 + 3_000_000*0.5 + 220*25) / 1e6
	if !near(s.Cost(), wantCost) {
		t.Errorf("cost = %v, want %v", s.Cost(), wantCost)
	}
	if s.Peak != 10+2_000_000+1000 {
		t.Errorf("peak = %d", s.Peak)
	}
	if s.Window != 1_000_000 {
		t.Errorf("window = %d, want the long tier for a 2M prompt", s.Window)
	}
	if s.Cwd != "/w/demo" {
		t.Errorf("cwd = %q", s.Cwd)
	}
	if len(s.Buckets) != 1 {
		t.Errorf("buckets = %d, want one five-minute step", len(s.Buckets))
	}
}

// Claude Code writes one record per content block, each with its message's usage: the message counts once.
func TestOneMessageOverSeveralRecordsCountsOnce(t *testing.T) {
	p := write(t,
		asstID("m1", "2026-09-01T10:00:00Z", "claude-opus-5", "high", `{"type":"thinking","thinking":"x"}`, 10, 1, 0, 0, 0),
		asstID("m1", "2026-09-01T10:00:01Z", "claude-opus-5", "high", `{"type":"text","text":"y"}`, 10, 5, 0, 0, 0),
		asstID("m2", "2026-09-01T10:00:09Z", "claude-opus-5", "high", `{"type":"text","text":"z"}`, 20, 2, 0, 0, 0),
	)
	s := New(p, FormatClaude)
	s.Read()
	if s.Requests != 2 || s.Tokens.Input != 30 || s.Tokens.Output != 7 {
		t.Errorf("requests %d, tokens %+v: want 2 requests of 30 in and 7 out", s.Requests, s.Tokens)
	}
}

func TestToolTimingsErrorsAndMedian(t *testing.T) {
	p := write(t,
		asst("2026-09-01T10:00:00Z", "claude-opus-5", "high",
			`{"type":"tool_use","id":"a","name":"Bash"},{"type":"tool_use","id":"b","name":"Read","input":{"file_path":"/w/demo/a.go"}}`, 1, 1, 0, 0, 0),
		user("2026-09-01T10:00:00.050Z", `{"type":"tool_result","tool_use_id":"b"}`),
		user("2026-09-01T10:00:30Z", `{"type":"tool_result","tool_use_id":"a","is_error":true}`),
	)
	s := New(p, FormatClaude)
	s.Read()
	bash, read := s.Tools["Bash"], s.Tools["Read"]
	if s.ToolErrors != 1 || bash.Errors != 1 || read.Errors != 0 {
		t.Fatalf("errors: %d, Bash %d, Read %d", s.ToolErrors, bash.Errors, read.Errors)
	}
	if read.Buckets[0] != 1 || bash.Buckets[3] != 1 {
		t.Errorf("buckets: Read %v, Bash %v", read.Buckets, bash.Buckets)
	}
	if s.Paths["/w/demo/a.go"] != 1 || len(s.Paths) != 1 {
		t.Errorf("paths = %v, want the Read's file", s.Paths)
	}
	r := Run(Query{From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
		[]Session{{ID: "s", Scans: []*Scan{s}}})
	for _, tr := range r.Tools {
		if tr.Name == "Bash" && tr.P50 != 30_000 || tr.Name == "Read" && tr.P50 != 50 {
			t.Errorf("%s p50 = %d", tr.Name, tr.P50)
		}
	}
	if r.Totals.ToolErrors != 1 {
		t.Errorf("errors in range = %d", r.Totals.ToolErrors)
	}
}

func TestReadResumesFromTheOffset(t *testing.T) {
	p := write(t, asst("2026-09-01T10:00:00Z", "claude-sonnet-5", "medium", `{"type":"text","text":"a"}`, 5, 5, 0, 0, 0))
	s := New(p, FormatClaude)
	s.Read()
	off := s.Offset
	appendTo(t, p, asst("2026-09-01T10:01:00Z", "claude-sonnet-5", "medium", `{"type":"text","text":"b"}`, 7, 7, 0, 0, 0))
	s.Read()
	if s.Requests != 2 || s.Offset <= off {
		t.Errorf("after append: %d requests, offset %d then %d", s.Requests, off, s.Offset)
	}
	if s.Read() || s.Requests != 2 {
		t.Errorf("a read with nothing new counted again: %d", s.Requests)
	}
}

func TestPartialTrailingLineIsLeftForNextTime(t *testing.T) {
	full := asst("2026-09-01T10:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 1, 1, 0, 0, 0)
	p := filepath.Join(t.TempDir(), "s.jsonl")
	os.WriteFile(p, []byte(full+"\n"+full[:len(full)/2]), 0o644)
	s := New(p, FormatClaude)
	s.Read()
	if s.Requests != 1 {
		t.Fatalf("requests = %d, want 1: the half line can't count", s.Requests)
	}
	os.WriteFile(p, []byte(full+"\n"+full+"\n"), 0o644)
	s.Read()
	if s.Requests != 2 {
		t.Errorf("requests = %d, want 2 once the line is whole", s.Requests)
	}
}

func TestShrunkFileIsReadFromTheStart(t *testing.T) {
	long := asst("2026-09-01T10:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"aaaaaaaaaaaaaaaaaaaa"}`, 9, 9, 0, 0, 0)
	p := write(t, long, long)
	s := New(p, FormatClaude)
	s.Read()
	os.WriteFile(p, []byte(asst("2026-09-02T10:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"b"}`, 1, 1, 0, 0, 0)+"\n"), 0o644)
	s.Read()
	if s.Requests != 1 {
		t.Errorf("requests = %d, want 1: the tally starts over", s.Requests)
	}
}

func TestActiveTimeIgnoresLongGaps(t *testing.T) {
	p := write(t,
		asst("2026-09-01T10:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 1, 1, 0, 0, 0),
		asst("2026-09-01T10:00:30Z", "claude-opus-5", "high", `{"type":"text","text":"b"}`, 1, 1, 0, 0, 0),
		asst("2026-09-01T12:00:30Z", "claude-opus-5", "high", `{"type":"text","text":"c"}`, 1, 1, 0, 0, 0),
		asst("2026-09-01T12:01:00Z", "claude-opus-5", "high", `{"type":"text","text":"d"}`, 1, 1, 0, 0, 0),
	)
	s := New(p, FormatClaude)
	s.Read()
	if s.ActiveS != 60 {
		t.Errorf("active = %ds, want 60", s.ActiveS)
	}
}

func TestCompactionIsTheContextDropping(t *testing.T) {
	p := write(t,
		asst("2026-09-01T10:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 1, 1, 168_000, 0, 0),
		asst("2026-09-01T10:01:00Z", "claude-opus-5", "high", `{"type":"text","text":"b"}`, 1, 1, 41_000, 0, 0),
		asst("2026-09-01T10:02:00Z", "claude-opus-5", "high", `{"type":"text","text":"c"}`, 1, 1, 30_000, 0, 0),
	)
	s := New(p, FormatClaude)
	s.Read()
	if len(s.Compactions) != 1 || s.Compactions[0].From != 168_001 || s.Compactions[0].To != 41_001 {
		t.Errorf("compactions = %+v, want one from 168k to 41k", s.Compactions)
	}
}

// The host's record of a provider's events reads as Claude's log does.
func TestEventsFormat(t *testing.T) {
	p := write(t,
		`{"at":"2026-09-01T10:00:00Z","kind":"turn"}`,
		`{"at":"2026-09-01T10:00:01Z","kind":"tool","id":"c1","name":"Edit","input":{"files":[{"path":"/w/api/x.go"}]}}`,
		`{"at":"2026-09-01T10:00:03Z","kind":"result","id":"c1","error":true}`,
		`{"at":"2026-09-01T10:00:09Z","kind":"request","model":"gpt-5-codex","effort":"high","tokens":{"input":1000000,"output":100000}}`,
	)
	s := New(p, FormatEvents)
	s.Read()
	if s.Turns != 1 || s.Requests != 1 || s.ToolErrors != 1 || s.Paths["/w/api/x.go"] != 1 {
		t.Errorf("scan = %d turns, %d requests, %d errors, paths %v", s.Turns, s.Requests, s.ToolErrors, s.Paths)
	}
	if !near(s.Cost(), 1.25+1.0) {
		t.Errorf("cost = %v, want $1.25 in and $1.00 out at gpt-5's price", s.Cost())
	}
}

// fixture is a multi-repo session over pricing and billing-ui: 3 file calls in pricing, 1 in billing-ui. The request
// working in pricing is $5 of opus output on Oct 1; on Oct 2 the one writing in billing-ui is $1, and the one after it,
// touching no file, $2.
func fixture(t *testing.T, id, workspace string) Session {
	t.Helper()
	pr, bu := filepath.Join(workspace, "pricing"), filepath.Join(workspace, "billing-ui")
	p := write(t,
		user("2026-10-01T09:00:00Z", `{"type":"text","text":"round it"}`),
		asst("2026-10-01T09:00:10Z", "claude-opus-5", "high",
			fmt.Sprintf(`{"type":"tool_use","id":"1","name":"Read","input":{"file_path":%q}},`+
				`{"type":"tool_use","id":"2","name":"Edit","input":{"file_path":%q}},`+
				`{"type":"tool_use","id":"3","name":"Grep","input":{"path":%q}},`+
				`{"type":"tool_use","id":"4","name":"Bash","input":{"command":"go test"}}`,
				pr+"/round.go", pr+"/round.go", pr),
			0, 200_000, 0, 0, 0),
		asst("2026-10-02T09:00:00Z", "claude-opus-5", "high",
			fmt.Sprintf(`{"type":"tool_use","id":"5","name":"Write","input":{"file_path":%q}}`, bu+"/Invoice.tsx"), 0, 40_000, 0, 0, 0),
		asst("2026-10-02T09:01:00Z", "claude-opus-5", "high", `{"type":"text","text":"done"}`, 0, 80_000, 0, 0, 0),
	)
	return Session{ID: id, Name: id, Kind: "work", Provider: "claude", Repos: []Repo{{Name: "pricing", Dir: pr}, {Name: "billing-ui", Dir: bu}},
		Scans: []*Scan{readNew(p)}}
}

func readNew(p string) *Scan {
	s := New(p, FormatClaude)
	s.Read()
	return s
}

var october = Query{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), Split: true}

// A multi-repo session charges each request to the repo it worked in: that of its own last file call (Read, Edit,
// Write, Grep, Glob), else of the latest before it. The total stays the real one.
func TestGroupByRepoSplitsMultiRepoSessions(t *testing.T) {
	s := fixture(t, "pricing-rounding", "/ws/a")
	r := Run(october, []Session{s})
	if len(r.Groups) != 2 || r.Groups[0].Key != "pricing" || r.Groups[1].Key != "billing-ui" {
		t.Fatalf("groups = %+v", r.Groups)
	}
	if !near(r.Groups[0].Cost, 5) || !near(r.Groups[1].Cost, 3) {
		t.Errorf("costs = %v and %v, want $5 in pricing and $3 in billing-ui", r.Groups[0].Cost, r.Groups[1].Cost)
	}
	if !near(r.Totals.Cost, 8) || r.Totals.Sessions != 1 || r.Totals.MultiRepo != 1 || r.Totals.Pairs != 2 {
		t.Errorf("totals = %+v", r.Totals)
	}
	if len(r.Points) != 7 || !near(r.Groups[0].Series[0], 5) || !near(r.Groups[0].Series[1], 0) || !near(r.Groups[1].Series[1], 3) {
		t.Errorf("by day: pricing %v, billing-ui %v over %d days", r.Groups[0].Series, r.Groups[1].Series, len(r.Points))
	}
	whole := october
	whole.Split = false
	r = Run(whole, []Session{s})
	if !near(r.Groups[0].Cost, 8) || !near(r.Groups[1].Cost, 8) || !near(r.Totals.Cost, 8) {
		t.Errorf("unsplit: %v and %v, total %v; want each charged the whole $8, the total still $8", r.Groups[0].Cost, r.Groups[1].Cost,
			r.Totals.Cost)
	}
}

// Requests before any file work, or working outside every repo, split by each repo's share of the file calls.
func TestUnplacedRequestsSplitByFileCalls(t *testing.T) {
	p := write(t,
		asst("2026-10-01T09:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"thinking"}`, 0, 40_000, 0, 0, 0),
		asst("2026-10-01T09:01:00Z", "claude-opus-5", "high",
			`{"type":"tool_use","id":"1","name":"Read","input":{"file_path":"/x/a/1.go"}},`+
				`{"type":"tool_use","id":"2","name":"Read","input":{"file_path":"/x/a/2.go"}},`+
				`{"type":"tool_use","id":"3","name":"Read","input":{"file_path":"/x/b/3.go"}}`, 0, 40_000, 0, 0, 0),
		asst("2026-10-01T09:02:00Z", "claude-opus-5", "high",
			`{"type":"tool_use","id":"4","name":"Read","input":{"file_path":"/x/a/1.go"}}`, 0, 40_000, 0, 0, 0),
		asst("2026-10-01T09:03:00Z", "claude-opus-5", "high",
			`{"type":"tool_use","id":"5","name":"Read","input":{"file_path":"/tmp/notes.md"}}`, 0, 40_000, 0, 0, 0),
	)
	s := Session{ID: "s", Repos: []Repo{{Name: "a", Dir: "/x/a"}, {Name: "b", Dir: "/x/b"}}, Scans: []*Scan{readNew(p)}}
	r := Run(october, []Session{s})
	// $1 each: the first and last split 3:1, the second is b's (its last call), the third a's.
	if len(r.Groups) != 2 || r.Groups[0].Key != "a" || !near(r.Groups[0].Cost, 1+.75+.75) || !near(r.Groups[1].Cost, 1+.25+.25) {
		t.Errorf("groups = %+v, want a $2.50 and b $1.50", r.Groups)
	}
	q := october
	q.Filters = map[string][]string{"repo": {"b"}}
	if r := Run(q, []Session{s}); !near(r.Totals.Cost, 1.5) {
		t.Errorf("b alone = $%v, want $1.50", r.Totals.Cost)
	}
}

// A session with no file work splits evenly.
func TestNoFileWorkSplitsEvenly(t *testing.T) {
	p := write(t, asst("2026-10-01T09:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"hi"}`, 0, 400_000, 0, 0, 0))
	s := Session{ID: "s", Repos: []Repo{{Name: "a", Dir: "/x/a"}, {Name: "b", Dir: "/x/b"}}, Scans: []*Scan{readNew(p)}}
	r := Run(october, []Session{s})
	if len(r.Groups) != 2 || !near(r.Groups[0].Cost, 5) || !near(r.Groups[1].Cost, 5) {
		t.Errorf("groups = %+v, want $5 each", r.Groups)
	}
}

func TestGroupBySessionAndFilters(t *testing.T) {
	a, b := fixture(t, "a", "/ws/a"), fixture(t, "b", "/ws/b")
	b.Repos = b.Repos[:1]
	r := Run(Query{Group: BySession, From: october.From, To: october.To, Split: true}, []Session{a, b})
	if len(r.Groups) != 2 || r.Groups[0].Label != "a" || len(r.Groups[0].Repos) != 2 {
		t.Fatalf("groups = %+v", r.Groups)
	}
	q := Query{Group: BySession, From: october.From, To: october.To, Split: true, Filters: map[string][]string{"repo": {"billing-ui"}}}
	r = Run(q, []Session{a, b})
	if len(r.Groups) != 1 || r.Groups[0].Key != "a" || !near(r.Groups[0].Cost, 3) || !near(r.Totals.Cost, 3) {
		t.Errorf("billing-ui by session = %+v, total %v; want a's $3", r.Groups, r.Totals.Cost)
	}
	r = Run(Query{Group: ByModel, From: october.From, To: october.To}, []Session{a, b})
	if len(r.Models) != 1 || r.Models[0].Requests != 6 || len(r.Groups) != 1 || r.Groups[0].Key != "claude-opus-5" {
		t.Errorf("models = %+v, groups %+v", r.Models, r.Groups)
	}
	prev := Query{From: october.To, To: october.To.Add(7 * 24 * time.Hour)}
	if r := Run(prev, []Session{a}); r.Prev.Requests != 3 || r.Totals.Requests != 0 {
		t.Errorf("the period before = %+v, this one %+v", r.Prev, r.Totals)
	}
}

// The stats file keeps each log's offset, so a rescan, in this process or the next, reads only what was
// appended. Rewriting what was already read proves it isn't read again.
func TestStoreRescansOnlyWhatWasAppended(t *testing.T) {
	file := filepath.Join(t.TempDir(), "stats.json")
	first := asst("2026-10-01T09:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 0, 1000, 0, 0, 0)
	p := write(t, first)
	logs := []Log{{Path: p, Format: FormatClaude}}
	st := NewStore(file, nil)
	if sc := st.Scan("s", logs); sc[0].Requests != 1 {
		t.Fatalf("first scan: %d requests", sc[0].Requests)
	}
	st.Save()
	// The same length, saying something else: a reread would count it.
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), `"output_tokens":1000`, `"output_tokens":9999`, 1)), 0o644)
	appendTo(t, p, asst("2026-10-01T09:01:00Z", "claude-opus-5", "high", `{"type":"text","text":"b"}`, 0, 1, 0, 0, 0))
	sc := NewStore(file, nil).Scan("s", logs)
	if sc[0].Requests != 2 || sc[0].Tokens.Output != 1001 {
		t.Errorf("after a restart: %d requests, %d output; want 2 and 1001", sc[0].Requests, sc[0].Tokens.Output)
	}
}

// A log read at other prices is read again at the book's; one whose log is gone keeps the costs it had.
//
// ref:   cost-dev/models.json
// quote: pb_anthropic_claude-opus-5_direct_realtime_global_usd_20260724","verified_at":"2026-07-24T00:00:00Z","source_id":"source:anthropic-pricing"},"prices":{"cache_write_1h_tokens_per_1000000":"10","cache_write_5m_tokens_per_1000000":"6.25","cached_input_tokens_per_1000000":"0.5","input_tokens_per_1000000":"5","output_tokens_per_1000000":"25"}
func TestStoreRepricesWhenThePricesChange(t *testing.T) {
	file := filepath.Join(t.TempDir(), "stats.json")
	kept := write(t, asst("2026-10-01T09:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 0, 1_000_000, 0, 0, 0))
	gone := write(t, asst("2026-10-01T09:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 0, 1_000_000, 0, 0, 0))
	logs := []Log{{Path: kept, Format: FormatClaude}, {Path: gone, Format: FormatClaude}}
	book := price.Default()
	prices := func() *price.Book { return book }
	st := NewStore(file, prices)
	if sc := st.Scan("s", logs); !near(sc[0].Cost(), 25) || !near(sc[1].Cost(), 25) {
		t.Fatalf("at the snapshot's prices: $%v and $%v, want $25 each", sc[0].Cost(), sc[1].Cost())
	}
	st.Save()
	cheaper, err := price.Parse([]byte(`{"items":[{"id":"anthropic:claude-opus-5","prices":{"input_tokens_per_1000000":"1",` +
		`"output_tokens_per_1000000":"2"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	book = cheaper
	os.Remove(gone)
	sc := NewStore(file, prices).Scan("s", logs)
	if !near(sc[0].Cost(), 2) || sc[0].Requests != 1 {
		t.Errorf("read again: $%v over %d requests, want $2 over 1", sc[0].Cost(), sc[0].Requests)
	}
	if !near(sc[1].Cost(), 25) || sc[1].Requests != 1 {
		t.Errorf("its log gone: $%v over %d requests, want the $25 it had", sc[1].Cost(), sc[1].Requests)
	}
}

func TestDetail(t *testing.T) {
	s := fixture(t, "pricing-rounding", "/ws/a")
	d := Detailed(s, true, 0)
	if len(d.Repos) != 2 || d.Repos[0].Repo != "pricing" || !near(d.Repos[0].Share, 5./8) || !near(d.Repos[0].Cost, 5) || d.Repos[0].Files != 3 {
		t.Errorf("repos = %+v", d.Repos)
	}
	if !near(Detailed(s, false, 0).Repos[1].Cost, 8) {
		t.Errorf("counted whole, billing-ui is charged the session")
	}
	if len(d.Buckets) != 2 || d.Buckets[0].Turns != 1 || d.Totals.Turns != 1 || d.Totals.Requests != 3 {
		t.Errorf("buckets = %+v, totals %+v", d.Buckets, d.Totals)
	}
	if len(d.Efforts) != 1 || d.Efforts[0].Name != "high" || d.Efforts[0].Requests != 3 {
		t.Errorf("efforts = %+v", d.Efforts)
	}
}

// Cost adds up across accounts, so a query counts them all; grouped or filtered by account, each is its own.
func TestGroupByAccount(t *testing.T) {
	a, b := fixture(t, "a", "/ws/a"), fixture(t, "b", "/ws/b")
	a.Accounts = []Account{{ID: "u1/o-work", Name: "ada@work.example"}}
	b.Accounts = []Account{{ID: "u1/o-home", Name: "ada@home.example"}}
	if r := Run(october, []Session{a, b}); !near(r.Totals.Cost, 16) {
		t.Errorf("every account: %v, want $16", r.Totals.Cost)
	}
	q := october
	q.Group = ByAccount
	r := Run(q, []Session{a, b})
	if len(r.Groups) != 2 || r.Groups[0].Label != "ada@home.example" && r.Groups[0].Label != "ada@work.example" || !near(r.Groups[0].Cost, 8) {
		t.Errorf("by account = %+v", r.Groups)
	}
	q.Filters = map[string][]string{"account": {"u1/o-work"}}
	if r := Run(q, []Session{a, b}); len(r.Groups) != 1 || r.Groups[0].Key != "u1/o-work" || !near(r.Totals.Cost, 8) {
		t.Errorf("the work account = %+v", r.Groups)
	}
}

// toolCall is an assistant record calling one tool, and result its answer.
func toolCall(at, id, name string) string {
	return asst(at, "claude-opus-5", "high", fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":{}}`, id, name), 0, 1, 0, 0, 0)
}

func result(at, id string, isErr bool) string {
	return user(at, fmt.Sprintf(`{"type":"tool_result","tool_use_id":%q,"is_error":%v}`, id, isErr))
}

// The tools on the Stats page count only the calls inside the range, not the whole sessions they're in: a call
// is in the hour it was made, and its error and timing go with it, even when the result comes after the range ends.
func TestToolsCountOnlyCallsInTheRange(t *testing.T) {
	p := write(t,
		toolCall("2026-09-30T23:59:00Z", "before", "Bash"), result("2026-09-30T23:59:30Z", "before", true),
		toolCall("2026-10-01T00:00:10Z", "first", "Bash"), result("2026-10-01T00:00:10.050Z", "first", false),
		toolCall("2026-10-03T12:00:00Z", "mid", "Read"), result("2026-10-03T12:00:02Z", "mid", true),
		toolCall("2026-10-07T23:59:50Z", "last", "Bash"), result("2026-10-08T00:00:20Z", "last", true),
		toolCall("2026-10-08T00:00:30Z", "after", "Read"), result("2026-10-08T00:00:31Z", "after", false),
	)
	r := Run(october, []Session{{ID: "s", Scans: []*Scan{readNew(p)}}})
	got := map[string]ToolRow{}
	for _, tr := range r.Tools {
		got[tr.Name] = tr
	}
	if b := got["Bash"]; b.Calls != 2 || b.Errors != 1 || b.Buckets[0] != 1 || b.Buckets[3] != 1 {
		t.Errorf("Bash = %+v, want 2 calls in the range, 1 errored, one under 100ms and one of 30s", b)
	}
	if rd := got["Read"]; rd.Calls != 1 || rd.Errors != 1 || rd.Buckets[2] != 1 {
		t.Errorf("Read = %+v, want the one call inside", rd)
	}
	if r.Totals.ToolCalls != 3 || r.Totals.ToolErrors != 2 {
		t.Errorf("totals: %d calls, %d errors; want 3 and 2", r.Totals.ToolCalls, r.Totals.ToolErrors)
	}
	q := october
	q.Filters = map[string][]string{"model": {"claude-sonnet-5"}}
	if r := Run(q, []Session{{ID: "s", Scans: []*Scan{readNew(p)}}}); len(r.Tools) != 0 {
		t.Errorf("another model's tools = %+v, want none", r.Tools)
	}
}

// The Active tile's "of Nh" is the wall-clock span of the range's sessions: on each day a session worked, its
// first record to its last, added up. A night between two days isn't in it, nor what falls outside the range.
func TestActiveSpanOfTheRange(t *testing.T) {
	a := write(t,
		asst("2026-09-30T20:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"before"}`, 0, 1, 0, 0, 0),
		asst("2026-10-01T09:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 0, 1, 0, 0, 0),
		asst("2026-10-01T09:04:00Z", "claude-opus-5", "high", `{"type":"text","text":"b"}`, 0, 1, 0, 0, 0),
		asst("2026-10-01T12:30:00Z", "claude-opus-5", "high", `{"type":"text","text":"c"}`, 0, 1, 0, 0, 0),
		asst("2026-10-02T10:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"d"}`, 0, 1, 0, 0, 0),
		asst("2026-10-02T10:30:00Z", "claude-opus-5", "high", `{"type":"text","text":"e"}`, 0, 1, 0, 0, 0),
	)
	b := write(t,
		asst("2026-10-05T10:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 0, 1, 0, 0, 0),
		asst("2026-10-05T11:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"b"}`, 0, 1, 0, 0, 0),
	)
	r := Run(october, []Session{{ID: "a", Scans: []*Scan{readNew(a)}}, {ID: "b", Scans: []*Scan{readNew(b)}}})
	if want := int64((3*time.Hour + 30*time.Minute + 30*time.Minute + time.Hour) / time.Second); r.Totals.SpanS != want {
		t.Errorf("span = %ds, want %ds: a's 09:00–12:30 and 10:00–10:30 the next day, b's 10:00–11:00", r.Totals.SpanS, want)
	}
	q := october
	q.Offset = -11 * 3600
	// Eleven hours west of UTC the days are the zone's: 09:00–09:04 UTC is the evening of Sep 30, and 12:30 Oct 1 to
	// 10:30 Oct 2 UTC is Oct 1, 01:30–23:30.
	if r := Run(q, []Session{{ID: "a", Scans: []*Scan{readNew(a)}}}); r.Totals.SpanS != int64((22*time.Hour+4*time.Minute)/time.Second) {
		t.Errorf("at UTC-11: span %ds, want 22h04m", r.Totals.SpanS)
	}
	if r.Totals.ActiveS != 240 {
		t.Errorf("active = %ds, want the 4 minutes between a and b", r.Totals.ActiveS)
	}
}

// A session's history stays with the account it ran on at the time: a later login moves only what comes after.
func TestAccountAtTheTime(t *testing.T) {
	a, b := fixture(t, "a", "/ws/a"), fixture(t, "b", "/ws/b")
	oct2 := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	a.Accounts = []Account{{At: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), ID: "u1/o-work", Name: "ada@work.example"},
		{At: oct2, ID: "u1/o-home", Name: "ada@home.example"}}
	b.Accounts = []Account{{At: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), ID: "u1/o-work", Name: "ada@work.example"}}
	q := october
	q.Group = ByAccount
	r := Run(q, []Session{a, b})
	cost := map[string]float64{}
	for _, g := range r.Groups {
		cost[g.Label] = g.Cost
	}
	if len(cost) != 2 || !near(cost["ada@work.example"], 5+8) || !near(cost["ada@home.example"], 3) {
		t.Errorf("by account = %v, want work $13 (a's Oct 1 and all of b), home $3 (a's Oct 2)", cost)
	}
	q.Group, q.Filters = ByRepo, map[string][]string{"account": {"u1/o-work"}}
	if r := Run(q, []Session{a, b}); !near(r.Totals.Cost, 13) || r.Totals.Sessions != 2 {
		t.Errorf("the work account: $%v over %d sessions, want $13 over 2", r.Totals.Cost, r.Totals.Sessions)
	}
	if d := Detailed(a, true, 0); d.Account != "u1/o-home" {
		t.Errorf("the session's account now = %q", d.Account)
	}
}

// Scans write nothing; one Save writes what they all read, and a Save with nothing new writes nothing.
func TestStoreSavesOnce(t *testing.T) {
	file := filepath.Join(t.TempDir(), "stats.json")
	st := NewStore(file, nil)
	for i := range 3 {
		p := write(t, asst("2026-10-01T09:00:00Z", "claude-opus-5", "high", `{"type":"text","text":"a"}`, 0, 1, 0, 0, 0))
		st.Scan(fmt.Sprint("s", i), []Log{{Path: p, Format: FormatClaude}})
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("a scan wrote the stats file")
	}
	st.Save()
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(file)
	st.Save()
	if _, err := os.Stat(file); err == nil {
		t.Fatalf("a save with nothing read wrote %d bytes again", fi.Size())
	}
}
