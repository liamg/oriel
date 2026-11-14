package stats_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/stats"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	hoststats "github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func statsScene(t *testing.T) (*apptest.Scene, *rig.Stats, *stats.Plugin) {
	return apptest.StatsScene(t)
}

// statsOf is the scene's stats plugin, on the scene's clock and in UTC.
func statsOf(s *apptest.Scene) *stats.Plugin {
	x := apptest.PluginOf[*stats.Plugin](s, "stats")
	x.Clock, x.Loc = s.Clock(), time.UTC
	return x
}

// settle draws frames until every answer the stats asked for, and the sidebar's git status, is in.
func settle(s *apptest.Scene, x *stats.Plugin) {
	s.T.Helper()
	s.SettleGit()
	s.Until("the stats to come in", func() bool { return !x.Busy() })
	s.Quiet()
	s.Until("the stats to come in", func() bool { return !x.Busy() })
}

// startCard is the start view's card or row named label, not the rail's entry of the same name.
func startCard(s *apptest.Scene, label string) *render.AccessNode {
	s.T.Helper()
	var found *render.AccessNode
	var find func(n *render.AccessNode)
	find = func(n *render.AccessNode) {
		if n.Name == label && n.Role == render.RoleButton && n.Bounds.Min.X >= 0 {
			found = n
		}
		for _, k := range n.Children {
			find(k)
		}
	}
	find(s.Access())
	if found == nil {
		s.T.Fatalf("no start card %q:\n%s", label, s.Text())
	}
	return found
}

// The global page grouped by repo: the tiles, the chart in the repos' colours, the table with
// the split's repo-sessions; drilling into a repo adds its chip and regroups by session.
func TestStatsPageSnapshots(t *testing.T) {
	s, _, x := statsScene(t)
	s.Press("<D-S>")
	settle(s, x)
	if !s.Shown("stats") {
		t.Fatal("⌘⇧S didn't open the Stats page")
	}
	s.Press("j")
	s.Press("j")
	settle(s, x)
	s.Lint("the stats page")
	s.Golden("stats-page")
	s.WantFrame("STATS", "COST · EST.", "COST PER REPO PER DAY", "pricing", "billing-ui", "repo-sessions from", "TOKENS BY KIND",
		"TOOLS BY ERROR RATE", "HOURS OF THE DAY", "drill into")
	r := x.Report(x.Query())
	want := r.Groups[x.Selected()].Key
	s.Press("<CR>")
	settle(s, x)
	if x.Group() != hoststats.BySession || len(x.Filters()["repo"]) != 1 || x.Filters()["repo"][0] != want {
		t.Errorf("drilling into %s: group %s, filters %v", want, x.Group(), x.Filters())
	}
	s.Press("<BS>")
	if x.Group() != hoststats.ByRepo || len(x.Filters()) != 0 {
		t.Errorf("⌫ didn't back out: %s %v", x.Group(), x.Filters())
	}
	s.Press("gs")
	settle(s, x)
	if x.Group() != hoststats.BySession {
		t.Fatalf("g s grouped by %s", x.Group())
	}
	s.Golden("stats-sessions")
	s.WantFrame("COST PER SESSION PER DAY", "pricing-rounding", "search-api", "span several repos")
}

// Resting on the cost tile says where its prices come from: cost.dev's list, as last checked.
//
// ref:   cost-dev/models.json
// quote: pb_mistral_mistral-large-4_direct_realtime_global_usd_20261020","verified_at":"2026-10-22T08:30:00Z"
func TestCostTileSaysWhereThePricesCameFrom(t *testing.T) {
	s, _, x := statsScene(t)
	s.Press("<D-S>")
	settle(s, x)
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.TextPoint("COST · EST.")})
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Frame()
	s.WantFrame("Prices from cost.dev, updated 22 Oct 2026")
}

// One session's stats as a main tab, from the session stats box's cost.
func TestSessionStatsSnapshot(t *testing.T) {
	s, r, x := statsScene(t)
	s.Run("agent.console", nil)
	s.Frame()
	s.Frame()
	s.Click(apptest.Centre(s.Node("Open the session's stats").Bounds), 0)
	settle(s, x)
	if !s.Showing(stats.StatsSessionView) {
		t.Fatal("the session stats box's cost didn't open the session's stats")
	}
	s.Run("view.close", registry.Args{"id": "agent.console"})
	settle(s, x)
	s.Golden("stats-session")
	s.WantFrame("pricing-rounding", "COST OVER THE SESSION", "COST PER REPO", "count whole session in each", "MODELS", "TOOLS",
		"compacted", "idle", "turns", "EFFORT")
	d, _ := x.Session(r.IDs["pricing-rounding"])
	if len(d.Repos) != 2 || d.Repos[0].Repo != "pricing" || d.Repos[0].Share < .6 || d.Repos[0].Share > .76 {
		t.Errorf("pricing-rounding's repos = %+v", d.Repos)
	}
	// The toggle and the setting agree: counted whole, each repo is charged the session.
	s.Click(apptest.Centre(s.Node("Count whole session in each").Bounds), 0)
	settle(s, x)
	if x.Split() {
		t.Error("the session tab's toggle didn't turn stats.split_sessions off")
	}
	d, _ = x.Session(r.IDs["pricing-rounding"])
	if len(d.Repos) != 2 || d.Repos[0].Cost != d.Repos[1].Cost {
		t.Errorf("counted whole, repos = %+v", d.Repos)
	}
	s.Press("<D-S>")
	settle(s, x)
	if n := s.Node("Split multi-repo sessions"); n.Checked == nil || *n.Checked {
		t.Error("the page's toggle doesn't show the setting off")
	}
}

// Usage: each window's burn and where it takes it; the weekly one runs out before its reset.
func TestUsageSnapshot(t *testing.T) {
	s, _, x := statsScene(t)
	s.Press("<D-U>")
	settle(s, x)
	if !s.Shown("usage") {
		t.Fatal("⌘⇧U didn't open Usage")
	}
	s.Lint("the usage page")
	s.Golden("usage")
	s.WantFrame("USAGE", "Claude Max", "Work", "5-HOUR WINDOW", "WEEKLY", "WEEKLY · OPUS", "BURNED THE WEEKLY WINDOW",
		"won't run out", "before the reset", "WINDOW USAGE · LAST 7 DAYS", "WORK · CLAUDE MAX")
	// Codex's account is its own: picked, its windows show instead.
	s.Click(s.TextPoint("ADA@EXAMPLE.COM · CHATGPT PRO"), 0)
	settle(s, x)
	s.WantFrame("ChatGPT Pro", "22%", "48%")
}

// Every way to the pages opens them: the sidebar's entries, the title bar's meters, :Stats and :Usage, the
// keys, and the start view's links.
func TestStatsEntryPoints(t *testing.T) {
	s, _, x := statsScene(t)
	settle(s, x)
	s.WantFrame("today", "wk 71%")
	s.Click(apptest.Centre(s.Node("Stats").Bounds), 0)
	if !s.Shown("stats") {
		t.Error("the sidebar's Stats didn't open the page")
	}
	s.Close("stats")
	s.Frame()
	s.Click(apptest.Centre(s.Node("Usage").Bounds), 0)
	if !s.Shown("usage") {
		t.Error("the sidebar's Usage didn't open the page")
	}
	s.Close("usage")
	s.Frame()
	s.Click(apptest.Centre(s.Node("Claude Max usage").Bounds), 0)
	if !s.Shown("usage") {
		t.Error("the title bar's meters didn't open Usage")
	}
	s.Press("<Esc>")
	if s.Shown("usage") {
		t.Error("Escape didn't close Usage")
	}
	s.Run("Stats", registry.Args{"args": "repo=api 7d"})
	if !s.Shown("stats") || x.Range() != 0 || x.Filters()["repo"][0] != "api" {
		t.Errorf(":Stats repo=api 7d: open %v, range %d, filters %v", s.Shown("stats"), x.Range(), x.Filters())
	}
	s.Run("Usage", nil)
	if !s.Shown("usage") || s.Shown("stats") {
		t.Error(":Usage didn't open Usage in Stats' place")
	}
	s.Run("Stats", registry.Args{"args": "session"})
	s.Frame()
	if !s.Showing(stats.StatsSessionView) {
		t.Error(":Stats session didn't open the session's tab")
	}
	// The pages are in the omnibox, with their keys.
	var labels []string
	for _, src := range s.Registry().Pickers() {
		if src.ID == "pages" {
			for _, it := range src.Items(context.Background(), "") {
				labels = append(labels, it.Label+" "+it.Key)
			}
		}
	}
	if got := strings.Join(labels, ", "); !strings.Contains(got, "Stats ⇧⌘S") || !strings.Contains(got, "Usage ⇧⌘U") ||
		!strings.Contains(got, "Settings ⌘,") {
		t.Errorf("pages = %s", got)
	}
}

// The start view's links, each with its figure, open their pages.
func TestStartViewLinks(t *testing.T) {
	r := rig.NewStats(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(r.W, r.Claude, r.Codex)})
	x := apptest.PluginOf[*stats.Plugin](s, "stats")
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	settle(s, x)
	s.Click(apptest.Centre(startCard(s, "Stats").Bounds), 0)
	if !s.Shown("stats") {
		t.Error("the start view's Stats link didn't open the page")
	}
	s.Close("stats")
	s.Frame()
	s.Click(apptest.Centre(startCard(s, "Usage").Bounds), 0)
	if !s.Shown("usage") {
		t.Error("the start view's Usage link didn't open the page")
	}
}

// Plan usage belongs to an account. One machine signed in to two Claude accounts, a company's and a personal
// one: each is its own plan, never added to the other. Each session's meters are its own account's.
func TestUsageIsPerAccount(t *testing.T) {
	fake := agenttest.NewFake()
	fake.Name = "claude"
	lw := rig.NewHostWith(t, host.Options{}, fake)
	accounts := []struct {
		account string
		used    float64
	}{{"u1/o-acme", .71}, {"u1/o-personal", .22}}
	var sessions []core.SessionID
	for _, a := range accounts {
		id, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "on-" + strings.TrimPrefix(a.account, "u1/o-"), Prompt: "go",
			Provider: "claude", Repos: []core.RepoID{core.RepoID(lw.Repo)}})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, id)
		fake.WaitStarted(t).Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "claude", Account: a.account,
			AccountName: strings.TrimPrefix(a.account, "u1/o-") + "@example.com", Plan: "Claude Max", Sampled: rig.StatsNow,
			Windows: []core.UsageWindow{{Name: "Current week (all models)", Used: a.used, Resets: rig.StatsNow.Add(40 * time.Hour), Length: 7 * 24 * time.Hour}}}})
	}
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Now = rig.StatsNow
	st := s.UntilState("two accounts, each its sessions'", func(st core.State) bool {
		if len(st.Usage) != 2 {
			return false
		}
		for _, id := range sessions {
			if se, ok := st.Session(id); !ok || len(se.Snapshot().Agent.Accounts) == 0 {
				return false
			}
		}
		return true
	})
	x := statsOf(s)
	for _, u := range st.Usage {
		if u.Account == "u1/o-personal" && u.Windows[0].Used != .22 {
			t.Errorf("the personal account = %+v", u)
		}
	}
	s.Run("usage.open", nil)
	settle(s, x)
	s.WantFrame("ACCOUNT", "ACME@EXAMPLE.COM · CLAUDE MAX", "PERSONAL@EXAMPLE.COM · CLAUDE MAX", "71%")
	s.Click(s.TextPoint("PERSONAL@EXAMPLE.COM · CLAUDE MAX"), 0)
	settle(s, x)
	s.WantFrame("22%")
	if u, _ := x.UsageAccount(); u.Account != "u1/o-personal" {
		t.Errorf("the switcher picked %s", u.Account)
	}
	for i, a := range accounts {
		s.Store().Apply(core.SessionFocused{ID: sessions[i]})
		if u, _ := x.FrontUsage(); u.Account != a.account {
			t.Errorf("the session on %s shows %s's plan", a.account, u.Account)
		}
	}
}

// logRecord is the part of a fixture log line the oracles below read: Claude's records and Oriel's events.
type logRecord struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Error     bool      `json:"error"`
	Message   struct {
		Usage   json.RawMessage `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// fixtureCalls counts the tool calls the rig's logs make in [from, to), and the ones of them that errored, by tool:
// read from the logs themselves, not through the stats.
func fixtureCalls(t *testing.T, r *rig.Stats, from, to time.Time) map[string][2]int64 {
	t.Helper()
	out := map[string][2]int64{}
	for _, p := range r.Logs {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// A session's logs reuse a call's id from day to day: a call waits for its result by id until it comes.
		type call struct {
			name    string
			in, bad bool
		}
		var calls []*call
		pending := map[string]*call{}
		use := func(id, name string, in bool) {
			c := &call{name: name, in: in}
			calls = append(calls, c)
			pending[id] = c
		}
		answer := func(id string, bad bool) {
			if c := pending[id]; c != nil {
				c.bad = bad
				delete(pending, id)
			}
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			var rec logRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatal(err)
			}
			at := rec.Timestamp
			if at.IsZero() {
				at = rec.At
			}
			in := !at.Before(from) && at.Before(to)
			switch rec.Kind {
			case "tool":
				use(rec.ID, rec.Name, in)
			case "result":
				answer(rec.ID, rec.Error)
			}
			var blocks []struct {
				Type    string `json:"type"`
				ID      string `json:"id"`
				Name    string `json:"name"`
				UseID   string `json:"tool_use_id"`
				IsError bool   `json:"is_error"`
			}
			json.Unmarshal(rec.Message.Content, &blocks)
			for _, bl := range blocks {
				switch bl.Type {
				case "tool_use":
					use(bl.ID, bl.Name, in)
				case "tool_result":
					answer(bl.UseID, bl.IsError)
				}
			}
		}
		for _, c := range calls {
			if c.in {
				v := out[c.name]
				v[0]++
				if c.bad {
					v[1]++
				}
				out[c.name] = v
			}
		}
	}
	return out
}

// fixtureSpan is the wall-clock of the rig's sessions in [from, to), from their logs: for each one with a request in
// it, its first record to its last on each (UTC) day, added up, in whole seconds as the logs' times are kept.
func fixtureSpan(t *testing.T, r *rig.Stats, from, to time.Time) time.Duration {
	t.Helper()
	var sum time.Duration
	for _, p := range r.Logs {
		b, _ := os.ReadFile(p)
		days := map[string][2]time.Time{}
		requests := false
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			var rec logRecord
			json.Unmarshal([]byte(line), &rec)
			at := rec.Timestamp
			if at.IsZero() {
				at = rec.At
			}
			if at.Before(from) || !at.Before(to) {
				continue
			}
			d := at.UTC().Format(time.DateOnly)
			v, ok := days[d]
			if !ok || at.Before(v[0]) {
				v[0] = at
			}
			if at.After(v[1]) {
				v[1] = at
			}
			days[d] = v
			if rec.Kind == "request" || len(rec.Message.Usage) > 0 {
				requests = true
			}
		}
		if requests {
			for _, v := range days {
				sum += time.Duration(v[1].Unix()-v[0].Unix()) * time.Second
			}
		}
	}
	return sum
}

// wantTools checks the Stats page's tools against the calls the fixture logs make in its range.
func wantTools(t *testing.T, r *rig.Stats, x *stats.Plugin) {
	t.Helper()
	q := x.Query()
	want := fixtureCalls(t, r, q.From, q.To)
	rep := x.Report(q)
	got := map[string][2]int64{}
	for _, tr := range rep.Tools {
		got[tr.Name] = [2]int64{tr.Calls, tr.Errors}
	}
	if len(got) != len(want) {
		t.Errorf("tools %v, want %v", got, want)
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s in %s – %s: %d calls, %d errored; the logs have %d and %d", name, q.From.Format(time.DateOnly), q.To.Format(time.DateOnly),
				got[name][0], got[name][1], w[0], w[1])
		}
	}
	whole := fixtureCalls(t, r, time.Time{}, rig.StatsNow.Add(time.Hour))
	if whole["Read"] == want["Read"] {
		t.Errorf("the range doesn't cut through the fixture sessions: Read %v either way", want["Read"])
	}
}

// CUSTOM opens the date picker; a range picked with the keys, then with the mouse, re-queries, and the page's
// tools are the calls inside that range, though the fixture sessions run over its edges. The Active tile reads
// "of Nh", the sessions' wall-clock in the range.
func TestStatsCustomRange(t *testing.T) {
	s, r, x := statsScene(t)
	s.Press("<D-S>")
	settle(s, x)
	s.Click(apptest.Centre(s.Node("CUSTOM").Bounds), 0)
	s.Frame()
	if s.Count("Custom range, calendar") == 0 {
		t.Fatal("CUSTOM didn't open the date picker")
	}
	s.Lint("the stats page's date picker")
	s.Golden("stats-custom")
	// It opens on the range shown, 30 days to today, the cursor on Oct 14: a week back, ⏎, two more back, ⏎.
	for _, k := range []string{"<Up>", "<CR>", "<Up>", "<Up>", "<CR>"} {
		s.Press(k)
	}
	settle(s, x)
	q := x.Query()
	if !q.From.Equal(time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)) || !q.To.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) ||
		x.RangeDays() != 0 {
		t.Fatalf("picked with the keys: %v – %v, range %d", q.From, q.To, x.Range())
	}
	if s.Count("Custom range, calendar") != 0 {
		t.Error("the picker stays open after a pick")
	}
	if !s.Shown("stats") {
		t.Fatal("picking closed the Stats page")
	}
	wantTools(t, r, x)
	span := fixtureSpan(t, r, q.From, q.To)
	rep := x.Report(q)
	if time.Duration(rep.Totals.SpanS)*time.Second != span {
		t.Errorf("span = %v, the logs say %v", time.Duration(rep.Totals.SpanS)*time.Second, span)
	}
	s.WantFrame("ACTIVE", "of "+stats.Hours(int64(span/time.Second)), "Sep 23 – Oct 7")

	s.Click(apptest.Centre(s.Node("Change the custom range").Bounds), 0)
	s.Frame()
	s.Click(apptest.Centre(s.Node("2026-10-10").Bounds), 0)
	s.Click(apptest.Centre(s.Node("2026-10-01").Bounds), 0)
	settle(s, x)
	if q := x.Query(); !q.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !q.To.Equal(time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("picked with the mouse: %v – %v", q.From, q.To)
	}
	wantTools(t, r, x)

	// Escape on the picker closes it, not the page.
	s.Click(apptest.Centre(s.Node("Change the custom range").Bounds), 0)
	s.Frame()
	s.Press("<Esc>")
	s.Frame()
	if !s.Shown("stats") || s.Count("Custom range, calendar") != 0 {
		t.Errorf("Escape: page open %v, picker open %v", s.Shown("stats"), s.Count("Custom range, calendar") != 0)
	}
	s.Run("Stats", registry.Args{"args": "2026-09-01..2026-09-30"})
	if q := x.Query(); !q.From.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || !q.To.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf(":Stats 2026-09-01..2026-09-30 = %v – %v", q.From, q.To)
	}
}

// The Usage page's account header shows the plan's tier as a chip, and so does its switcher; an account whose
// tier isn't known (the fixture's Codex readings carry none) shows none.
func TestUsageTierChip(t *testing.T) {
	s, _, x := statsScene(t)
	s.Press("<D-U>")
	settle(s, x)
	s.WantFrame("Claude Max", "MAX 20×")
	if s.Count("Max 20×") != 1 || s.Count("WORK · CLAUDE MAX, Max 20×") != 1 {
		t.Error("the header or the switcher lacks the tier")
	}
	s.Click(s.TextPoint("ADA@EXAMPLE.COM · CHATGPT PRO"), 0)
	settle(s, x)
	s.WantFrame("ChatGPT Pro")
	if s.Count("Max 20×") != 0 {
		t.Error("Codex's header shows a tier")
	}
	if s.Count("ADA@EXAMPLE.COM · CHATGPT PRO") != 1 {
		t.Error("the switcher gave Codex's account a badge")
	}
}

// Hovering the chart or a row names the series over the chart without moving anything: what's under the pointer
// stays under it, frame after frame.
func TestStatsHoverKeepsTheLayout(t *testing.T) {
	s, _, x := statsScene(t)
	s.Press("<D-S>")
	settle(s, x)
	chart := s.Node("Cost per repo per day").Bounds
	for y := chart.Min.Y; y < chart.Max.Y+120; y += 6 {
		p := render.Point{X: (chart.Min.X + chart.Max.X) / 2, Y: y}
		s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
		for range 3 {
			s.Frame()
		}
		if got := s.Node("Cost per repo per day").Bounds; got != chart {
			t.Fatalf("hovering %v moved the chart from %v to %v", p, chart, got)
		}
	}
}

// The plan usage card, hovered from the title bar's top right corner, opens on the indicator's left: all of it in
// the window.
func TestUsageCardStaysInTheWindow(t *testing.T) {
	s, _, x := statsScene(t)
	settle(s, x)
	var ind *render.AccessNode
	var walk func(n *render.AccessNode)
	walk = func(n *render.AccessNode) {
		if n.Role == render.RoleButton && strings.HasSuffix(n.Name, " usage") {
			ind = n
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(s.Access())
	if ind == nil {
		t.Fatalf("no usage indicator:\n%s", s.Text())
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: (ind.Bounds.Min.X + ind.Bounds.Max.X) / 2, Y: (ind.Bounds.Min.Y + ind.Bounds.Max.Y) / 2}})
	s.Until("the card", func() bool { return s.Find("Plan usage") != nil })
	card, win := s.Node("Plan usage").Bounds, s.Size()
	if card.Min.X < 0 || card.Min.Y < 0 || card.Max.X > win.X || card.Max.Y > win.Y {
		t.Errorf("the card %v runs out of the %v window", card, win)
	}
}
