package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

func (s *errScene) mission() *missionPlugin {
	for _, e := range s.plugins {
		if m, ok := e.(*missionPlugin); ok {
			return m
		}
	}
	return nil
}

// missionScene is 03-switcher's: this Mac, and sessions in every status.
func missionScene(t *testing.T) (*errScene, *[]string) {
	t.Helper()
	s := newScene(t, "", "", mockupSize)
	s.now = at(14, 30, 0)
	ago := func(d time.Duration) time.Time { return s.now.Add(-d) }
	gib := uint64(1 << 30)
	now := core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 10, CPU: .34, Mem: 112 * gib / 10, MemTotal: 32 * gib,
		Sampled: ago(time.Second), Providers: []string{"claude", "codex"}}}
	var evs []core.Event
	for i := range 12 {
		evs = append(evs, core.MachineChanged{Machine: core.MachineState{Host: "mbp", Cores: 10, CPU: .2 + .03*float64(i%5),
			Sampled: ago(time.Duration(12-i) * time.Minute)}})
	}
	evs = append(evs, now)
	for _, u := range []core.ProviderUsage{
		{Provider: "claude", Account: "liam", Tier: "Max", Sampled: ago(time.Minute), Windows: []core.UsageWindow{
			{Name: "5-hour", Used: .38, Length: 5 * time.Hour, Resets: s.now.Add(2 * time.Hour)},
			{Name: "Weekly", Used: .71, Length: 7 * 24 * time.Hour, Resets: s.now.Add(50 * time.Hour)}}},
		{Provider: "codex", Account: "liam@", Sampled: ago(time.Minute), Windows: []core.UsageWindow{
			{Name: "5-hour", Used: .12, Length: 5 * time.Hour, Resets: s.now.Add(3 * time.Hour)}}},
	} {
		evs = append(evs, core.UsageSampled{Usage: u})
	}
	for _, se := range []core.SessionState{
		{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindPlan, Started: true, Status: core.StatusWorking, Created: ago(3 * time.Hour),
			Agent: core.AgentState{Provider: "claude", Running: true, Working: true, TurnStarted: ago(41 * time.Second),
				Stats: core.Stats{Context: 44000, ContextMax: 200000}},
			Tasks: []core.Task{{ID: "1", Status: core.TaskCompleted}, {ID: "2", Status: core.TaskCompleted}, {ID: "3", Status: core.TaskCompleted},
				{ID: "4", Status: core.TaskInProgress}, {ID: "5", Status: core.TaskPending}},
			Transcript: []core.Item{
				{ID: "t1", Kind: core.ItemTool, Tool: "Edit", Title: "round.go", Done: true, Diff: &core.FileDiff{Path: "round.go", Added: 7, Removed: 1}},
				{ID: "t2", Kind: core.ItemTool, Tool: "Bash", Input: `{"command":"go test ./internal/pricing/"}`, Done: true, IsError: true,
					Output: "--- FAIL: TestRound (0.00s)\n    round_test.go:31: -2.5 → -2 got -3\nFAIL"},
				{ID: "t3", Kind: core.ItemTool, Tool: "Edit", Title: "round.go", Done: true, Diff: &core.FileDiff{Path: "round.go", Added: 3, Removed: 2}},
				{ID: "t4", Kind: core.ItemTool, Tool: "Bash", Input: `{"command":"go test ./internal/pricing/"}`, Done: true,
					Output: "ok  \tpricing\t0.214s"},
				{ID: "a1", Kind: core.ItemAssistant, Text: "replying on round.go:46", Partial: true, Time: ago(2 * time.Second)},
			}},
		{ID: "ci-lint", Name: "ci lint fix", Kind: core.KindWork, Status: core.StatusNeedsYou, PR: "https://github.com/acme/api/pull/4418",
			Created: ago(time.Hour), Agent: core.AgentState{Provider: "claude", Running: true},
			Transcript: []core.Item{{ID: "a1", Kind: core.ItemAssistant, Text: "golangci-lint: errcheck in usage/export.go:112, gofumpt in 2 files. Say “fix ci” or open.",
				Time: ago(14 * time.Minute)}}},
		{ID: "dash-filters", Name: "dash-filters", Kind: core.KindWork, Status: core.StatusReviewer, PR: "https://github.com/acme/dash/pull/4402",
			Created: ago(5 * time.Hour), Agent: core.AgentState{Provider: "claude"},
			Transcript: []core.Item{{ID: "a1", Kind: core.ItemAssistant, Text: "@maria requested changes on FilterBar.tsx — 2 threads mirrored, drafts ready.",
				Time: ago(2 * time.Hour)}}},
		{ID: "deps-sweep", Name: "deps sweep", Kind: core.KindJob, Status: core.StatusWorking, Created: ago(10 * time.Minute),
			Agent: core.AgentState{Provider: "codex", Running: true, Working: true, TurnStarted: ago(192 * time.Second)},
			Transcript: []core.Item{
				{ID: "t1", Kind: core.ItemTool, Tool: "shell", Input: `{"command":"go get -u ./... && go mod tidy"}`, Done: true,
					Output: "go: upgraded chroma v2.26.0 => v2.27.0\ngo: upgraded x/net v0.41.0 => v0.44.0"},
				{ID: "t2", Kind: core.ItemTool, Tool: "shell", Input: `{"command":"go test ./..."}`},
			}},
		{ID: "auth-flow", Name: "auth flow", Kind: core.KindPlan, Status: core.StatusQuiet, Created: ago(30 * time.Hour),
			Transcript: []core.Item{{ID: "a1", Kind: core.ItemAssistant, Text: "How a request is authenticated, from the edge middleware to the JWT verifier and session store.",
				Time: ago(26 * time.Hour)}}},
		{ID: "review-4410", Name: "review #4410", Kind: core.KindReview, Status: core.StatusPermission, Created: ago(time.Hour),
			Agent: core.AgentState{Provider: "claude", Running: true, Sandboxed: true, Network: "ask"},
			Asks:  []core.Ask{{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: "make e2e", Created: ago(22 * time.Minute)}}},
		{ID: "search-api", Name: "ENG-2231 search api", Kind: core.KindWork, Status: core.StatusNeedsYou, Created: ago(2 * time.Hour),
			Projects: []core.Project{{Name: "search-api"}, {Name: "billing"}}, Agent: core.AgentState{Provider: "claude", Running: true},
			Transcript: []core.Item{{ID: "a1", Kind: core.ItemAssistant, Text: "Evict stale entries on read, or by the nightly job? Read-time is simpler but adds 4ms p99.",
				Time: ago(6 * time.Minute)}}},
		{ID: "flaky-e2e", Name: "flaky e2e sweep", Kind: core.KindJob, Status: core.StatusCI, PR: "https://github.com/acme/api/pull/4419",
			Created: ago(time.Hour), Agent: core.AgentState{Provider: "claude"},
			Asks: []core.Ask{{ID: "g1", Kind: core.AskGateway, Tool: "oriel__push", Summary: "git push origin flaky-e2e", Created: ago(8 * time.Minute)}}},
	} {
		evs = append(evs, core.SessionSynced{Session: se})
	}
	for _, ev := range evs {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	var answered []string
	s.reg.Command(registry.Command{ID: "ask.answer", Run: func(_ registry.Ctx, a registry.Args) error {
		answered = append(answered, a.String("id")+"/"+a.String("ask")+"="+a.String("decision"))
		return nil
	}})
	s.frame()
	return s, &answered
}

// openMission opens mission control with ⌘⇧K, as the keyboard does, and lets its cards lay out.
func (s *errScene) openMission(t *testing.T) {
	t.Helper()
	s.keyCommand(t, "<D-K>")
	if !s.mission().open {
		t.Fatal("⌘⇧K didn't open mission control")
	}
	s.frame()
	s.frame()
}

// onMission is the node labelled label on mission control, not under it.
func (s *errScene) onMission(t *testing.T, label string) *render.AccessNode {
	t.Helper()
	n := s.missionNode(t, label)
	if n == nil {
		t.Fatalf("nothing labelled %q on mission control", label)
	}
	return n
}

func (s *errScene) missionNode(t *testing.T, label string) *render.AccessNode {
	t.Helper()
	var find func(n *render.AccessNode, name string, role render.Role) *render.AccessNode
	find = func(n *render.AccessNode, name string, role render.Role) *render.AccessNode {
		if n.Name == name && (role == render.RoleNone || n.Role == role) {
			return n
		}
		for _, k := range n.Children {
			if f := find(k, name, role); f != nil {
				return f
			}
		}
		return nil
	}
	page := find(s.ui.AccessTree(), "Mission control", render.RoleDialog)
	if page == nil {
		t.Fatal("mission control isn't up")
	}
	return find(page, label, render.RoleNone)
}

func (s *errScene) top(t *testing.T, label string) float32 {
	t.Helper()
	return s.onMission(t, label).Bounds.Min.Y
}

// SH-7: the cards are grouped by status, the most urgent first, or by repo; there's no card to start a session
// beside them.
func TestMissionControlGroups(t *testing.T) {
	s, _ := missionScene(t)
	s.openMission(t)
	s.wantFrame(t, "MISSION CONTROL", "NEEDS YOU", "WORKING", "2 need you", "1 permission", "2 working", "1 ci", "1 in review", "1 quiet")
	if s.top(t, "NEEDS YOU") > s.top(t, "WORKING") || s.top(t, "pricing-rounding") < s.top(t, "WORKING") {
		t.Error("grouped by status, the cards aren't under their statuses, most urgent first")
	}
	if strings.Contains(s.frameText(), "NEW SESSION ON") {
		t.Error("a group ends with a card to start a session")
	}
	s.run("mission.group", registry.Args{"by": "repo"})
	s.frame()
	s.frame()
	s.onMission(t, "SEARCH-API")
	for _, by := range []string{"colour", "worker"} {
		if err := s.reg.Run(context.Background(), "mission.group", registry.Args{"by": by}); err == nil {
			t.Errorf("mission.group took %s, a grouping it hasn't", by)
		}
	}
}

// SH-7 done when "working cards stream output": a working card's terminal shows the agent's tool calls and what
// they print, colours and all, and what the agent does next comes in as it happens.
func TestMissionWorkingCardsStreamOutput(t *testing.T) {
	s, _ := missionScene(t)
	s.openMission(t)
	s.wantFrame(t, "LIVE", "go test ./internal/pricing/", "--- FAIL: TestRound (0.00s)", "go get -u ./... && go mod tidy",
		"go: upgraded x/net v0.41.0 => v0.44.0", "» replying on round.go:46…")
	s.store.Apply(core.TranscriptAppended{Session: "deps-sweep", Items: []core.Item{
		{ID: "t2", Kind: core.ItemTool, Tool: "shell", Input: `{"command":"go test ./..."}`, Done: true,
			Output: "\x1b[32mok\x1b[0m  internal/diff\t0.31s\n\x1b[32mok\x1b[0m  internal/git\t1.02s\x1b[2J"},
		{ID: "t3", Kind: core.ItemTool, Tool: "shell", Input: `{"command":"go vet ./..."}`},
	}})
	s.frame()
	s.frame()
	s.wantFrame(t, "internal/git", "1.02s", "go vet ./...")
	if !strings.Contains(s.frameText(), "go mod tidy") {
		t.Error("the output written before the clear the tool printed is gone: the card let a tool clear its screen")
	}
	green := s.ui.Theme().C(tTermANSI[2])
	coloured := false
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "ok" && op.Color == green {
			coloured = true
		}
	}
	if !coloured {
		t.Error("the output's colours don't come through")
	}
	if n := len(s.mission().terms); n != 2 {
		t.Errorf("%d live terminals for 2 working cards", n)
	}
	s.store.Apply(core.SessionSynced{Session: core.SessionState{ID: "deps-sweep", Name: "deps sweep", Kind: core.KindJob, Status: core.StatusQuiet}})
	s.frame()
	if n := len(s.mission().terms); n != 1 {
		t.Errorf("a card that stopped working kept its terminal: %d", n)
	}
	s.keyCommand(t, "<D-K>")
	s.frame()
	if n := len(s.mission().terms); n != 0 {
		t.Errorf("mission control put away kept %d terminals", n)
	}
}

// A session whose last turn left work running says how much at its card's foot.
func TestMissionCardShowsBackgroundWork(t *testing.T) {
	s, _ := missionScene(t)
	s.openMission(t)
	s.store.Apply(core.SessionSynced{Session: core.SessionState{ID: "deps-sweep", Name: "deps sweep", Kind: core.KindJob, Status: core.StatusWorking,
		Agent: core.AgentState{Provider: "claude", Running: true, Background: []core.BackgroundTask{
			{Kind: "shell", Description: "Run the whole suite"}, {Kind: "subagent", Description: "Review the diff"}}}}})
	s.frame()
	s.frame()
	s.wantFrame(t, "+2 in background")
}

// SH-7 done when "Allow/Deny works from the card": a waiting session's card is the inbox's card for its ask, and its
// buttons answer that ask through ask.answer; `a` allows the selected card's; nothing is stacked over the window twice.
func TestMissionAllowDenyFromTheCard(t *testing.T) {
	s, answered := missionScene(t)
	s.frame()
	if !strings.Contains(s.frameText(), "Allow once") {
		t.Fatal("the permission isn't stacked over the window before mission control opens")
	}
	s.openMission(t)
	if n := strings.Count(s.frameText(), "Allow once"); n != 1 {
		t.Errorf("%d permission cards; want 1, on review #4410's card, none stacked over it", n)
	}
	if y := s.textPoint(t, "Allow once").Y; y < s.top(t, "review #4410") {
		t.Error("Allow once isn't on review #4410's card")
	}
	s.run("mission.select", registry.Args{"id": "flaky-e2e"})
	s.frame()
	s.keyCommand(t, "a")
	s.clickText(t, "Allow once")
	s.clickText(t, "Deny") // the gateway card's, on flaky e2e sweep
	want := []string{"flaky-e2e/g1=allow", "review-4410/p1=allow", "flaky-e2e/g1=deny"}
	if strings.Join(*answered, " ") != strings.Join(want, " ") {
		t.Errorf("the cards answered %v, want %v", *answered, want)
	}
	s.run("mission.select", registry.Args{"id": "pricing-rounding"})
	if err := s.reg.Run(context.Background(), "mission.allow", nil); err == nil {
		t.Error("a allowed something on a card asking nothing")
	}
	s.mission().close()
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "Allow once") {
		t.Error("the stack didn't come back once mission control went")
	}
}

// SH-7 (ui.md 03): "Fully keyboard-driven: hjkl, ⏎ opens, a allows, x stops"; e ends, r renames on the card, n starts
// a session, / filters.
func TestMissionKeys(t *testing.T) {
	s, _ := missionScene(t)
	var ran []string
	for _, id := range []string{"session.open", "session.stop", "session.end", "session.rename", "session.new"} {
		s.reg.Command(registry.Command{ID: id, Run: func(_ registry.Ctx, a registry.Args) error {
			ran = append(ran, id+" "+a.String("id")+a.String("name"))
			return nil
		}})
	}
	s.openMission(t)
	m := s.mission()
	// By status, the oldest first in each: needs you: ENG-2231 search api, ci lint fix; permission: review #4410;
	// working: pricing-rounding, deps sweep; then flaky e2e sweep, dash-filters, auth flow.
	if m.selected() != "search-api" {
		t.Fatalf("the first card selected is %s", m.selected())
	}
	s.keyCommand(t, "l")
	s.keyCommand(t, "j")
	if got := m.selected(); got != "review-4410" {
		t.Errorf("l then j selected %s, want the card in the next group", got)
	}
	s.keyCommand(t, "j")
	s.keyCommand(t, "l")
	if got := m.selected(); got != "deps-sweep" {
		t.Errorf("j then l selected %s", got)
	}
	s.keyCommand(t, "k")
	s.keyCommand(t, "h")
	if got := m.selected(); got != "ci-lint" {
		t.Errorf("k then h selected %s", got)
	}
	s.keyCommand(t, "x")
	s.keyCommand(t, "e")
	s.keyCommand(t, "n")
	s.keyCommand(t, "r")
	s.frame()
	for _, k := range "nightly" {
		s.typeKeys(string(k))
	}
	s.typeKeys("CR")
	s.frame()
	s.keyCommand(t, "<CR>")
	want := []string{"session.stop ci-lint", "session.end ci-lint", "session.new ", "session.rename ci-lintnightly", "session.open ci-lint"}
	if strings.Join(ran, "|") != strings.Join(want, "|") {
		t.Errorf("the keys ran %q, want %q", ran, want)
	}
	if m.open {
		t.Error("⏎ left mission control open over the session")
	}

	s.run("mission.open", registry.Args{"by": "status"})
	s.frame()
	s.keyCommand(t, "/")
	s.frame()
	for _, k := range "e2e" {
		s.typeKeys(string(k))
	}
	s.frame()
	s.frame()
	if s.missionNode(t, "pricing-rounding") != nil || s.missionNode(t, "flaky e2e sweep") == nil {
		t.Error("the filter didn't narrow the cards to flaky e2e sweep")
	}
	s.run("mission.filter", registry.Args{"text": ""})
	s.frame()
	s.frame()
	s.onMission(t, "pricing-rounding")
}

// SE-3, SH-7: started with nothing open and sessions there, the app lands on the one in front last time, else the
// newest, with mission control over it; Escape from mission control is in that session, not on the start view.
func TestMissionOpensAtLaunch(t *testing.T) {
	lw := newLocalHost(t)
	ids := map[string]core.SessionID{}
	for _, name := range []string{"old", "front", "recent"} {
		id, err := lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindDirect, Name: name, Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	if err := lw.w.Store().Apply(core.SessionFocused{ID: ids["front"]}); err != nil {
		t.Fatal(err)
	}
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	r.waitText("MISSION CONTROL")
	if f := r.store.State().Focus; f != ids["front"] {
		t.Errorf("landed on %q, want the session in front last time (%s)", f, ids["front"])
	}
	r.typeKeys("<Esc>")
	f := r.latest("mission control put away", func(f headless.Frame) bool { return !strings.Contains(f.Text(), "MISSION CONTROL") })
	if text := f.Text(); strings.Contains(text, "Open Directory") {
		t.Errorf("Escape from mission control went to the start view:\n%s", text)
	}
}

// With no sessions the app stays on the start view, and one started after launch doesn't open mission control.
func TestStartViewAtLaunchWithNoSessions(t *testing.T) {
	r := startApp(t, t.TempDir(), "", nil)
	r.waitText("Open Directory")
	if err := r.store.Apply(core.SessionSynced{Session: core.SessionState{ID: "n", Name: "new-one", Kind: core.KindWork, Status: core.StatusWorking,
		Created: time.Now().Add(time.Second), Agent: core.AgentState{Running: true}}}); err != nil {
		t.Fatal(err)
	}
	if text := r.waitText("new-one"); strings.Contains(text, "MISSION CONTROL") {
		t.Errorf("a session started after launch opened mission control:\n%s", text)
	}
}

// SH-7: 03-switcher.
func TestMissionSnapshot(t *testing.T) {
	s, _ := missionScene(t)
	s.store.Apply(core.SessionFocused{ID: "review-4410"})
	s.openMission(t)
	s.run("mission.select", registry.Args{"id": "review-4410"})
	s.lint("mission control")
	s.golden("mission-03")
}

// SH-7, PL-4: the assistant's mission_control reads mission control as it shows the sessions (crit's did), and with
// show opens it, grouped and selected as asked.
func TestMissionControlTool(t *testing.T) {
	s, _ := missionScene(t)
	var tool registry.AgentTool
	for _, c := range s.reg.Contributions("agent.tool") {
		if at := c.(registry.AgentTool); at.Name == "mission_control" {
			tool = at
		}
	}
	if tool.Run == nil || !slices.Contains(tool.For, assistantAgent) {
		t.Fatalf("no mission_control for the assistant: %+v", tool)
	}
	call := func(p map[string]any) missionState {
		t.Helper()
		done := make(chan [2]any, 1)
		go func() {
			out, err := tool.Run(context.Background(), p)
			done <- [2]any{out, err}
		}()
		for {
			select {
			case r := <-done:
				if err, _ := r[1].(error); err != nil {
					t.Fatal(err)
				}
				return r[0].(missionState)
			default:
				s.sh.runTodo()
				time.Sleep(time.Millisecond)
			}
		}
	}
	got := call(map[string]any{})
	if got.Open || got.By != "status" || len(got.Groups) != 6 || got.Groups[0].Title != "NEEDS YOU" {
		t.Fatalf("mission_control read %+v", got)
	}
	var review missionCardLine
	for _, g := range got.Groups {
		for _, c := range g.Sessions {
			if c.ID == "review-4410" {
				review = c
			}
		}
	}
	if review.Status != "permission" || !strings.Contains(review.Waiting, "make e2e") {
		t.Errorf("review #4410 reads %+v", review)
	}
	if len(got.NeedsYou) == 0 || !strings.HasPrefix(got.NeedsYou[0], "Permission: Bash(make e2e)") {
		t.Errorf("needs you, oldest first: %q", got.NeedsYou)
	}
	got = call(map[string]any{"show": true, "by": "repo", "id": "flaky-e2e"})
	if !got.Open || got.By != "repo" || got.Selected != "flaky-e2e" || !s.mission().open {
		t.Errorf("show didn't open it as asked: %+v", got)
	}
}

// A double-click on a card opens its session; a single click only selects it. Hovered, a card shows Open (Resume
// while its agent is stopped), and clicking it opens the session too.
func TestMissionCardOpens(t *testing.T) {
	s, _ := missionScene(t)
	var opened []string
	s.reg.Command(registry.Command{ID: "session.open", Run: func(_ registry.Ctx, a registry.Args) error {
		opened = append(opened, a.String("id"))
		return nil
	}})
	s.openMission(t)
	card := s.onMission(t, "ci lint fix").Bounds
	in := render.Point{X: card.Min.X + 5, Y: card.Min.Y + 5}
	s.click(in)
	if len(opened) != 0 || s.mission().selected() != "ci-lint" {
		t.Fatalf("one click: opened %v, selected %s", opened, s.mission().selected())
	}
	s.click(in)
	if !slices.Equal(opened, []string{"ci-lint"}) {
		t.Fatalf("a double-click on the card opened %v", opened)
	}

	opened = nil
	s.mission().show()
	button := func(label string) *render.AccessNode {
		var find func(n *render.AccessNode) *render.AccessNode
		find = func(n *render.AccessNode) *render.AccessNode {
			if n.Name == label && n.Role == render.RoleButton {
				return n
			}
			for _, k := range n.Children {
				if f := find(k); f != nil {
					return f
				}
			}
			return nil
		}
		return find(s.onMission(t, "ci lint fix"))
	}
	hover := func(p render.Point) {
		s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
		s.frame()
		s.frame()
	}
	hover(render.Point{X: 2, Y: 2})
	if button("Open") != nil {
		t.Fatal("the card shows Open without the pointer on it")
	}
	hover(in)
	b := button("Open")
	if b == nil {
		t.Fatal("no Open on the hovered card")
	}
	s.click(centre(b.Bounds))
	if !slices.Equal(opened, []string{"ci-lint"}) {
		t.Errorf("Open on the hovered card opened %v", opened)
	}

	s.mission().show()
	se, _ := s.store.State().Session("ci-lint")
	stopped := se.Snapshot()
	stopped.Agent = core.AgentState{}
	s.store.Apply(core.SessionSynced{Session: stopped})
	hover(in)
	if button("Resume") == nil {
		t.Error("a hovered card whose agent is stopped doesn't offer Resume")
	}
}
