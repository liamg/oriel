package agentui_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// cardScene is pricing-rounding in front with the transcript given, its asks answered by a stand-in ask.answer that
// closes each and says what it was given: "ask decision for scope", fields left out when empty.
func cardScene(t *testing.T, size render.Point, transcript ...core.Item) (*apptest.Scene, *[]string) {
	t.Helper()
	s := apptest.New(t, apptest.Options{Size: size})
	s.Now = apptest.At(14, 30, 0)
	se := core.SessionState{ID: "pricing-rounding", Name: "pricing-rounding", Kind: core.KindWork, Status: core.StatusPermission, Branch: "rethink/rounding",
		Projects: []core.Project{{Name: "pricing", Dir: "/src/pricing", Branch: "rethink/rounding", Primary: true}},
		Agent:    core.AgentState{Provider: "claude", Running: true}, Transcript: transcript}
	s.Apply(core.SessionSynced{Session: se}, core.TabOpened{ID: se.ID}, core.SessionFocused{ID: se.ID})
	var got []string
	s.Registry().Command(registry.Command{ID: "ask.answer", Run: func(_ registry.Ctx, a registry.Args) error {
		var parts []string
		for _, k := range []string{"ask", "decision", "for", "scope"} {
			if v := a.String(k); v != "" {
				parts = append(parts, v)
			}
		}
		got = append(got, strings.Join(parts, " "))
		return s.Store().Apply(core.AskClosed{Session: core.SessionID(a.String("id")), ID: a.String("ask"), Decision: a.String("decision")})
	}})
	s.Frame()
	return s, &got
}

// ask opens a in pricing-rounding and brings its prompt up, past the moment its keys are ignored.
func ask(s *apptest.Scene, a core.Ask) {
	if a.Created.IsZero() {
		a.Created = s.Now
	}
	s.Apply(core.AskOpened{Session: "pricing-rounding", Ask: a})
	s.Frame()
	s.Frame()
	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
}

func input(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func said(s *apptest.Scene, ago time.Duration, text string) core.Item {
	return core.Item{ID: fmt.Sprint("a", ago), Kind: core.ItemAssistant, Time: s.Now.Add(-ago), Text: text}
}

// wantAnswers fails unless the prompt's answers are list items named with their key and the rule each makes.
func wantAnswers(t *testing.T, s *apptest.Scene, want ...string) {
	t.Helper()
	list := s.FindRole(render.RoleList, "Answers")
	if list == nil {
		t.Fatalf("no list of answers:\n%s", s.Text())
	}
	var got []string
	for _, n := range list.Children {
		if n.Role != render.RoleListItem {
			t.Errorf("%q is a %s, not a list item", n.Name, n.Role)
		}
		got = append(got, n.Name)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the answers are\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

var permissionAnswers = func(rule string) []string {
	return []string{"Allow once, y", "Allow for this session, a, " + rule, "Allow for all sessions, ⇧A, " + rule + " · every session", "Deny, n",
		"Deny with a note…, ⇧N"}
}

// Each ask that holds the agent comes up as the same prompt: what it is and where, the agent's words before it asked,
// what it's about (a command in full, an edit's diff, a tool's arguments laid out, a gateway's call), and its answers
// as a list with the rule each makes; a network ask chooses its host or its domain above them.
func TestPromptLayouts(t *testing.T) {
	size := apptest.MockupSize
	t.Run("bash", func(t *testing.T) {
		s, _ := cardScene(t, size)
		s.Apply(core.TranscriptAppended{Session: "pricing-rounding", Items: []core.Item{
			said(s, time.Minute, "The negative cases are in the table now; running the package's tests to check ties go to even for -2.5 and -3.5.")}})
		ask(s, core.Ask{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Rule: "Bash(go test:*)",
			Summary: "go test ./internal/pricing/... -run 'TestRoundHalfEven|TestRoundMinor' -count=1 -v",
			Input:   input(map[string]string{"command": "go test ./internal/pricing/... -run 'TestRoundHalfEven|TestRoundMinor' -count=1 -v"})})
		s.WantFrame("PRICING-ROUNDING · PERMISSION", "1 OF 1", "Claude wants to run a command", "WHY", "running the package's tests",
			"go test ./internal/pricing/... -run 'TestRoundHalfEven|TestRoundMinor' -count=1 -v", "pricing-rounding · rethink/rounding · /src/pricing · on this machine")
		wantAnswers(t, s, permissionAnswers("Bash(go test:*)")...)
		s.Golden("prompt-bash")
	})
	t.Run("edit", func(t *testing.T) {
		s, _ := cardScene(t, size)
		s.Apply(core.TranscriptAppended{Session: "pricing-rounding", Items: []core.Item{
			said(s, time.Minute, "Floor rounds toward −∞, so −2.5 floors to −3; switching to Truncate with sign handling.")}})
		ask(s, core.Ask{ID: "p1", Kind: core.AskPermission, Tool: "Edit", Rule: "Edit", Summary: "/src/pricing/internal/pricing/round.go",
			Input: input(map[string]string{"file_path": "/src/pricing/internal/pricing/round.go"}),
			Diff: &core.FileDiff{Path: "/src/pricing/internal/pricing/round.go", Added: 3, Removed: 2, Lines: []core.DiffLine{
				{Op: " ", Old: 45, New: 45, Text: "\t\treturn floor.Add(one).Shift(-p)"},
				{Op: "-", Old: 46, Text: "\tcase 0:"},
				{Op: "-", Old: 47, Text: "\t\tif floor.IntPart()%2 == 0 { return floor.Shift(-p) }"},
				{Op: "+", New: 46, Text: "\tcase 0:"},
				{Op: "+", New: 47, Text: "\t\tt := scaled.Truncate(0)"},
				{Op: "+", New: 48, Text: "\t\tif t.IntPart()%2 == 0 { return t.Shift(-p) }"},
			}}})
		s.WantFrame("Claude wants to edit internal/pricing/round.go", "pricing-rounding · rethink/rounding · lines 46–48", "t := scaled.Truncate(0)")
		wantAnswers(t, s, permissionAnswers("Edit")...)
		s.Golden("prompt-edit")
	})
	t.Run("connector", func(t *testing.T) {
		s, _ := cardScene(t, size)
		s.Apply(core.TranscriptAppended{Session: "pricing-rounding", Items: []core.Item{
			said(s, time.Minute, "Filing the follow-up for the JPY precision table, as you asked.")}})
		ask(s, core.Ask{ID: "p1", Kind: core.AskPermission, Tool: "mcp__claude_ai_Linear__create_issue", Rule: "mcp__claude_ai_Linear__create_issue",
			Input: `{"team":"ENG","title":"Move currency precision table to config","priority":3,"description":"RoundMinor assumes 2 decimal places; JPY and KWD have 0, BHD has 3.\nRead precision from the currency table instead of hard-coding it.","labels":["pricing","tech-debt"]}`})
		s.WantFrame("Claude wants to use Linear: create_issue", "pricing-rounding · claude.ai connector · acts as you", "Move currency precision table to config",
			"Read precision from the currency table instead of hard-coding it.", `["pricing", "tech-debt"]`)
		wantAnswers(t, s, permissionAnswers("mcp__claude_ai_Linear__create_issue")...)
		s.Golden("prompt-connector")
	})
	t.Run("gateway", func(t *testing.T) {
		s, _ := cardScene(t, size)
		ask(s, core.Ask{ID: "g1", Kind: core.AskGateway, Tool: "github__create_pull_request", Rule: "github__create_pull_request",
			Summary: `github__create_pull_request {"base":"main","title":"Round half to even"}`, Detail: "leaves the sandbox",
			Input: `{"title":"Round half to even","base":"main","head":"rethink/rounding","draft":true}`})
		s.WantFrame("PRICING-ROUNDING · GATEWAY", "Claude wants to use github: create_pull_request through the gateway", "rethink/rounding")
		wantAnswers(t, s, permissionAnswers("github__create_pull_request")...)
		s.Golden("prompt-gateway")
	})
	t.Run("network", func(t *testing.T) {
		s, _ := cardScene(t, size)
		ask(s, core.Ask{ID: "n1", Kind: core.AskNetwork, Host: "proxy.golang.org:443", Rule: "net(proxy.golang.org:443)",
			Detail: "The agent is trying to connect to proxy.golang.org:443 for the first time; it waits until you answer."})
		s.WantFrame("PRICING-ROUNDING · NETWORK", "The agent is trying to connect to proxy.golang.org", "pricing-rounding · CONNECT :443",
			"proxy.golang.org", "*.golang.org")
		wantAnswers(t, s, "Once, o", "Allow for this session, a, proxy.golang.org", "Allow for all sessions, ⇧A, proxy.golang.org · every session", "Deny, n")
		s.Golden("prompt-network")
	})
}

// detailScroll is how far the prompt's detail, named label, is scrolled.
func detailScroll(t *testing.T, s *apptest.Scene, label string) (float32, render.Rect) {
	t.Helper()
	n := s.FindRole(render.RoleText, label)
	if n == nil {
		t.Fatalf("no detail %q", label)
	}
	return s.UI().Scroll(ui.ID(n.ID)).Y, n.Bounds
}

// wantFits fails unless the prompt is inside the window with its answers in view.
func wantFits(t *testing.T, s *apptest.Scene) {
	t.Helper()
	win := s.Size()
	list := s.FindRole(render.RoleList, "Answers")
	if list == nil {
		t.Fatal("no answers")
	}
	for _, n := range append([]*render.AccessNode{list}, list.Children...) {
		if b := n.Bounds; b.Min.Y < 0 || b.Max.Y > win.Y || b.Max.X > win.X || b.Min.X < 0 {
			t.Errorf("%q at %v is outside the %v window", n.Name, b, win)
		}
	}
}

// A command that runs on for screens and an edit thousands of lines long scroll inside the prompt: the card fits
// the window, its answers stay in view and answer a click, and the wheel, ⇧↓ and PgDn scroll the detail while ↓
// still moves the answers' highlight.
func TestPromptLongDetailScrolls(t *testing.T) {
	size := render.Point{X: 1280, Y: 800}
	s, got := cardScene(t, size)
	long := "go test " + strings.Repeat("./internal/pricing/rounding/halfeven/... ", 200) + "-count=1"
	ask(s, core.Ask{ID: "p1", Kind: core.AskPermission, Tool: "Bash", Summary: long, Input: input(map[string]string{"command": long}), Rule: "Bash(go test:*)"})
	wantFits(t, s)
	if !strings.Contains(s.FullText(), "-count=1") && !strings.Contains(s.FullText(), "more lines") {
		t.Error("the command's end is neither shown nor said to be below")
	}
	before, _ := detailScroll(t, s, "The command")
	s.Press("<S-Down>")
	if after, _ := detailScroll(t, s, "The command"); after <= before {
		t.Errorf("⇧↓ didn't scroll the command: %v, then %v", before, after)
	}
	s.ClickTop("Deny")
	if strings.Join(*got, ";") != "p1 deny" {
		t.Fatalf("a click on Deny answered %v", *got)
	}

	var lines []core.DiffLine
	for i := range 3000 {
		lines = append(lines, core.DiffLine{Op: "+", New: i + 1, Text: fmt.Sprintf("\tvar v%d = round(%d)", i, i)})
	}
	ask(s, core.Ask{ID: "p2", Kind: core.AskPermission, Tool: "Write", Rule: "Write", Input: input(map[string]string{"file_path": "/src/pricing/table.go"}),
		Diff: &core.FileDiff{Path: "/src/pricing/table.go", Added: 3000, Lines: lines}})
	wantFits(t, s)
	top, box := detailScroll(t, s, "The edit")
	if box.Dy() > size.Y*.4+1 {
		t.Errorf("the edit's box is %v tall in a %v window", box.Dy(), size.Y)
	}
	s.Press("<PageDown>")
	paged, _ := detailScroll(t, s, "The edit")
	if paged <= top {
		t.Fatalf("PgDn didn't scroll the edit: %v, then %v", top, paged)
	}
	mid := render.Point{X: (box.Min.X + box.Max.X) / 2, Y: (box.Min.Y + box.Max.Y) / 2}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: mid})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: mid, Scroll: render.Point{Y: 200}})
	s.Frame()
	s.Frame()
	if wheeled, _ := detailScroll(t, s, "The edit"); wheeled <= paged {
		t.Errorf("the wheel didn't scroll the edit: %v, then %v", paged, wheeled)
	}
	s.Press("<S-Down>")
	s.Press("Down")
	s.Press("CR")
	if (*got)[len(*got)-1] != "p2 always" {
		t.Errorf("⇧↓ ↓ ↵ answered %v, want Allow for this session", *got)
	}
}

// The keys pick as the list shows: ↑/↓ and ↵, or an answer's own key; a for this session, ⇧A for all of them.
func TestPromptKeys(t *testing.T) {
	s, got := cardScene(t, apptest.MockupSize)
	cmd := func(id, c string) core.Ask {
		return core.Ask{ID: id, Kind: core.AskPermission, Tool: "Bash", Summary: c, Input: input(map[string]string{"command": c}), Rule: "Bash(go test:*)"}
	}
	ask(s, cmd("p1", "go test ./..."))
	s.Press("Down")
	s.Press("Down")
	s.Press("Up")
	s.Press("Down")
	s.Press("CR")
	ask(s, cmd("p2", "go test ./a"))
	s.Press("a")
	ask(s, cmd("p3", "go test ./b"))
	s.Press("<S-a>")
	ask(s, cmd("p4", "go test ./c"))
	s.Press("y")
	ask(s, cmd("p5", "go test ./d"))
	s.Press("n")
	want := []string{"p1 always all", "p2 always", "p3 always all", "p4 allow", "p5 deny"}
	if strings.Join(*got, ";") != strings.Join(want, ";") {
		t.Errorf("answered %q, want %q", *got, want)
	}
}

// A network ask's answers apply to the scope chosen above them: the host, or (← →, or a click) every host under its
// domain; for this session or for all.
func TestPromptNetworkScope(t *testing.T) {
	s, got := cardScene(t, apptest.MockupSize)
	net := func(id string) core.Ask {
		return core.Ask{ID: id, Kind: core.AskNetwork, Host: "proxy.golang.org:443", Rule: "net(proxy.golang.org:443)"}
	}
	ask(s, net("n1"))
	s.Press("Right")
	s.WantFrame("*.golang.org · every session")
	s.Press("a")
	ask(s, net("n2"))
	s.ClickTop("*.golang.org")
	s.Press("<S-a>")
	ask(s, net("n3"))
	s.Press("<S-a>")
	ask(s, net("n4"))
	s.Press("o")
	want := []string{"n1 always *.golang.org:443", "n2 always all *.golang.org:443", "n3 always all proxy.golang.org:443", "n4 allow"}
	if strings.Join(*got, ";") != strings.Join(want, ";") {
		t.Errorf("answered %q, want %q", *got, want)
	}
}

// The prompt counts its session's queue: its place, and how many there are, answered ones counted until none wait.
func TestPromptCountsTheQueue(t *testing.T) {
	s, _ := cardScene(t, apptest.MockupSize)
	for _, id := range []string{"p1", "p2", "p3"} {
		s.Apply(core.AskOpened{Session: "pricing-rounding", Ask: permission(id, "make "+id)})
	}
	s.Frame()
	s.Frame()
	s.WantFrame("1 OF 3", "make p1")
	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
	s.Press("y")
	s.Frame()
	s.WantFrame("2 OF 3", "make p2")
	s.Apply(core.AskOpened{Session: "pricing-rounding", Ask: permission("p4", "make p4")})
	s.Frame()
	s.WantFrame("2 OF 4")
	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
	s.Press("y")
	s.Frame()
	s.WantFrame("3 OF 4", "make p3")
	for range 2 {
		s.Now = s.Now.Add(widget.CardKeyGuard)
		s.Frame()
		s.Press("y")
		s.Frame()
	}
	ask(s, permission("p5", "make p5"))
	s.WantFrame("1 OF 1", "make p5")
}
