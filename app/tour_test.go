package app

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// sessionGo is internal/auth/session.go with 14-tour's Mint at lines 42-49.
func sessionGo() string {
	var b strings.Builder
	b.WriteString("package auth\n\nimport (\n\t\"context\"\n\t\"time\"\n)\n\n")
	for i := 8; i <= 40; i++ {
		fmt.Fprintf(&b, "// line %d of the minter's docs.\n", i)
	}
	b.WriteString("\n")
	b.WriteString("func (m *Minter) Mint(ctx context.Context, u User) (Pair, error) {\n")
	b.WriteString("\tnow := m.clock.Now()\n")
	b.WriteString("\taccess, err := m.sign(claims{Sub: u.ID, Exp: now.Add(15 * time.Minute)})\n")
	b.WriteString("\tif err != nil { return Pair{}, err }\n")
	b.WriteString("\tfamily := newFamilyID()\n")
	b.WriteString("\trefresh := m.store.Insert(ctx, family, u.ID, now.Add(30 * 24 * time.Hour))\n")
	b.WriteString("\treturn Pair{Access: access, Refresh: refresh}, nil\n")
	b.WriteString("}\n")
	return b.String()
}

var tourSteps = []string{"The login request arrives", "OAuth callback and state check", "Finding or creating the user", "Session tokens are minted",
	"The cookie, and why it's split", "Middleware on every request", "Refresh without a round trip", "API keys: the other door", "Where it can go wrong"}

const tourStep4 = "# Session tokens are minted\n\n" +
	"Once the user exists, the callback hands off to `session.Mint`. It issues two tokens with very different lifetimes: a short **access token** " +
	"the API checks on every request, and a long **refresh token** that only the refresh endpoint ever reads.\n\n" +
	"```go repo=api path=internal/auth/session.go lines=42-49 mark=44,47\n```\n\n" +
	"```note 45\nFifteen minutes is the whole blast radius of a leaked access token: no revocation list is checked on the hot path.\n```\n\n" +
	"```note 47\nThe refresh token is a row, not a JWT: the `family` lets step 7 revoke every descendant when a reused token shows up.\n```\n\n" +
	"The dashboard never sees the refresh token's value. It only learns when the access token will expire, so it can schedule a refresh before the user notices:\n\n" +
	"```ts repo=dashboard path=src/auth/useSession.ts lines=18-19 mark=19\n```\n\n" +
	"```mermaid caption=\"refresh never leaves the server as a JWT\"\nsequenceDiagram\n  callback->>Minter: Mint(user)\n  Minter->>store: Insert(family, 30d)\n```\n"

// tourScene is 14-tour: a walkthrough of how authentication works across two repos (api and dashboard), nine
// steps, on step 4, with a question about it answered.
func tourScene(t *testing.T) (*errScene, string) {
	t.Helper()
	ws := t.TempDir()
	repo := func(name string, files map[string]string) string {
		r := &gittest.Repo{T: t, Dir: filepath.Join(ws, host.ReposDir, name), When: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
		os.MkdirAll(r.Dir, 0o755)
		r.Git("init", "-q", "-b", "main")
		r.Write(files)
		r.Commit("Ada", "auth")
		return strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	}
	api := repo("api", map[string]string{"internal/auth/session.go": sessionGo()})
	var ts strings.Builder
	for i := 1; i <= 17; i++ {
		fmt.Fprintf(&ts, "// %d\n", i)
	}
	ts.WriteString("const expiresAt = decodeExp(res.headers.get(\"x-access-exp\"))\nsetTimeout(refresh, expiresAt - Date.now() - 60_000)\n")
	dash := repo("dashboard", map[string]string{"src/auth/useSession.ts": ts.String()})
	m := host.Manifest{Name: "auth-flow", Kind: "tour", Tour: &host.TourInfo{Title: "How authentication works", Kind: "walkthrough"},
		Projects: []host.ManifestEntry{{Name: "api", Repo: "/src/api", Dir: "repos/api", Primary: true, Detached: true, Commit: api, Base: "main"},
			{Name: "dashboard", Repo: "/src/dashboard", Dir: "repos/dashboard", Detached: true, Commit: dash, Base: "main"}}}
	b, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(ws, host.ManifestFile), b, 0o644)
	for i, title := range tourSteps {
		body := "# " + title + "\n\nWhat happens here.\n"
		if i == 3 {
			body = tourStep4
		}
		os.WriteFile(filepath.Join(ws, fmt.Sprintf("%02d-%s.md", i+1, strings.ToLower(strings.Fields(title)[0]))), []byte(body), 0o644)
	}
	s := newScene(t, "", filepath.Join(ws, "repos/api"), mockupSize)
	s.now = at(14, 30, 0)
	ago := func(d time.Duration) time.Time { return s.now.Add(-d) }
	projects := []core.Project{{Name: "api", Dir: filepath.Join(ws, "repos/api"), Primary: true, Detached: true, Commit: api, ReadOnly: true},
		{Name: "dashboard", Dir: filepath.Join(ws, "repos/dashboard"), Detached: true, Commit: dash, ReadOnly: true}}
	se := core.SessionState{ID: "auth-flow", Name: "auth flow", Kind: core.KindTour, Dir: ws, Prompt: "How authentication works", Status: core.StatusQuiet,
		Projects: projects, Agent: core.AgentState{Provider: "claude", Model: "opus", Running: true},
		Threads: []core.Thread{
			{ID: "q0", Status: core.ThreadOpen, Created: ago(time.Hour), Comments: []core.Comment{{ID: "c0", Author: core.AuthorYou, Name: "liam",
				Body: "How authentication works"}}},
			{ID: "s4", Status: core.ThreadOpen, Created: ago(3 * time.Minute), Anchor: &core.Anchor{Project: core.DocsProject, File: "04-session.md", Line: 1},
				Comments: []core.Comment{
					{ID: "c1", Author: core.AuthorYou, Name: "liam", Time: ago(3 * time.Minute), Body: "what stops a stolen refresh token being used twice?"},
					{ID: "c2", Author: core.AuthorAgent, Name: "claude", Time: ago(2 * time.Minute),
						Body: "Reuse detection, which is step 7. I've added a pinned note here pointing forward to it (note 2)."}}},
		}}
	for _, ev := range []core.Event{core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	return s, ws
}

func (s *errScene) tourFrames() {
	for range 3 {
		s.sh.runTodo()
		s.frame()
	}
}

// RV-8 (14-tour, ui.md: "Prose, quoted code (real files, real line numbers) and pinned notes… Ask about any step.
// Export to a single HTML file."): the reader opens on a tour session, and walks its steps.
func TestTourReader(t *testing.T) {
	s, _ := tourScene(t)
	s.tourFrames()
	s.wantText(t, "the reader opens by itself", "TOUR · WALKTHROUGH", "How authentication works", "API", "DASHBOARD", "STEP 1 OF 9",
		"The login request arrives", "Where it can go wrong", "↓ Export as HTML", "self-contained", "step 1 / 9")
	s.keyCommand(t, "l")
	s.keyCommand(t, "l")
	s.keyCommand(t, "l")
	s.tourFrames()
	s.wantText(t, "l three times: step 4", "STEP 4 OF 9", "Session tokens are minted", "internal/auth/session.go", "L42-49", "42", "49",
		"func", "Mint", "LINE 45", "Fifteen minutes is the whole blast radius", "LINE 47", "src/auth/useSession.ts", "L18-19", "setTimeout",
		"DIAGRAM", "sequenceDiagram", "refresh never leaves the server as a JWT", "ASK ABOUT THIS STEP", "what stops a stolen refresh token",
		"Reuse detection, which is step 7.", "step 4 / 9", "TOUR", "auth flow", "walkthrough · 2 repos · 2 quotes",
		"h/l step · j/k scroll · gd open quote in editor", "4/9")
	if strings.Contains(s.frameText(), "line 41 of") {
		t.Error("the quote shows a line outside its range")
	}
	s.keyCommand(t, "h")
	s.tourFrames()
	s.wantText(t, "h goes back", "STEP 3 OF 9", "Finding or creating the user")
	s.clickText(t, "Where it can go wrong")
	s.tourFrames()
	s.wantText(t, "a step clicked", "STEP 9 OF 9", "step 9 / 9")
	s.keyCommand(t, "l")
	s.wantText(t, "l at the end stays", "STEP 9 OF 9")
}

// The steps the agent writes appear as they land: the reader reads the tour again while it's shown.
func TestTourStepsLandWhileReading(t *testing.T) {
	s, ws := tourScene(t)
	s.tourFrames()
	os.WriteFile(filepath.Join(ws, "10-summary.md"), []byte("# What you now know\n\nAll of it.\n"), 0o644)
	s.now = s.now.Add(2 * time.Second)
	x := s.tourPlugin()
	se, _ := x.front()
	x.state(se.ID).read = time.Time{}
	s.tourFrames()
	s.wantText(t, "a new step", "What you now know", "step 1 / 10")
}

func (s *errScene) tourPlugin() *tourPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*tourPlugin); ok {
			return x
		}
	}
	return nil
}

// crit's reader: "Ask about this step" lands with the step named; here in the step's open thread, else a new one on
// the step's file. gd opens the step's quote in the editor at its marked line.
func TestTourAskAndOpenQuote(t *testing.T) {
	s, ws := tourScene(t)
	var args []registry.Args
	s.reg.Command(registry.Command{ID: "request.comment", Run: func(_ registry.Ctx, a registry.Args) error {
		args = append(args, a)
		return nil
	}})
	s.tourFrames()
	s.run("tour.step", registry.Args{"step": 4})
	s.tourFrames()
	s.typeInto(t, "Ask about this step", "and a stolen access token?")
	s.ui.Key(render.KeyEvent{Key: "CR"})
	s.tourFrames()
	if len(args) != 1 || args[0].String("thread") != "s4" || args[0].String("body") != "and a stolen access token?" {
		t.Fatalf("asked %v", args)
	}
	s.run("tour.step", registry.Args{"step": 2})
	s.tourFrames()
	s.run("tour.ask", registry.Args{"text": "why a state check?"})
	if len(args) != 2 || args[1].String("file") != filepath.Join(ws, "02-oauth.md") || args[1].Int("line") != 1 ||
		args[1].String("title") != "Step 2: OAuth callback and state check" {
		t.Fatalf("asked %v", args)
	}

	s.run("tour.step", registry.Args{"step": 4})
	s.tourFrames()
	s.keyCommand(t, "gd")
	s.tourFrames()
	if p := s.editor().current(); !strings.HasSuffix(p.Name(), "internal/auth/session.go") || p.Cursor().Line != 43 {
		t.Errorf("gd opened %q at %+v, want session.go's line 44", p.Name(), p.Cursor())
	}
}

// crit's Export: "The Export button writes tour.html — the whole tour in one self-contained file — and asks you here
// to publish it."
func TestTourExport(t *testing.T) {
	s, ws := tourScene(t)
	var args []registry.Args
	s.reg.Command(registry.Command{ID: "request.comment", Run: func(_ registry.Ctx, a registry.Args) error {
		args = append(args, a)
		return nil
	}})
	s.tourFrames()
	s.clickText(t, "↓ Export as HTML")
	s.tourFrames()
	b, err := os.ReadFile(filepath.Join(ws, "tour.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{"<title>How authentication works</title>", "Step 4 of 9", "Session tokens are minted", `data-n="44"`,
		"Fifteen minutes", "@media (prefers-color-scheme: light)", `class="hl-`, "sequenceDiagram"} {
		if !strings.Contains(html, want) {
			t.Errorf("tour.html lacks %q", want)
		}
	}
	if len(args) != 1 || args[0].String("thread") != "q0" || !strings.Contains(args[0].String("body"), "mcp__oriel__tour_share") {
		t.Errorf("asked %v", args)
	}

	tool := registry.AgentTools(s.reg)
	var out any
	for _, tl := range tool {
		if tl.Name == "tour_export" {
			out, err = tl.Run(registry.WithSession(t.Context(), "auth-flow"), map[string]any{"format": "md"})
		}
	}
	if err != nil || !strings.Contains(fmt.Sprint(out), filepath.Join(ws, "tour.md")) {
		t.Fatalf("tour_export: %v, %v", out, err)
	}
	md, _ := os.ReadFile(filepath.Join(ws, "tour.md"))
	if !strings.Contains(string(md), "func (m *Minter) Mint(ctx context.Context, u User) (Pair, error) {") {
		t.Errorf("tour.md lacks the quote:\n%s", md)
	}
}

// RV-8 (14-tour) against a golden: step 4 of the auth flow tour.
func TestTourSnapshot(t *testing.T) {
	s, _ := tourScene(t)
	s.tourFrames()
	s.run("tour.step", registry.Args{"step": 4})
	s.tourFrames()
	s.lint("the tour reader")
	s.golden("tour")
}

// crit's demo tours: a screenshot "with things pointed at", pins and arrows numbered in the order written, and a
// recorded run with what it printed.
func TestTourScreenshotsAndRuns(t *testing.T) {
	s, ws := tourScene(t)
	os.MkdirAll(filepath.Join(ws, "assets"), 0o755)
	f, _ := os.Create(filepath.Join(ws, "assets", "dash.png"))
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 800, 500)))
	f.Close()
	os.WriteFile(filepath.Join(ws, "01-login.md"), []byte("# The login request arrives\n\n```shot src=assets/dash.png caption=\"where it starts\"\n"+
		"pin 62,18 New tour\nbox 4,52 46,14 the tours you have\narrow 62,22 -> 24,58 it lands here\n```\n\n"+
		"```run cwd=harness cmd=\"go run ./demo\" exit=1 at=2026-10-01T09:00:00Z\nlistening on :8080\n```\n"), 0o644)
	s.tourFrames()
	s.wantText(t, "a shot and a run", "New tour", "the tours you have", "it lands here", "1", "2", "where it starts",
		"harness $ go run ./demo", "exit 1", "listening on :8080")
	images := 0
	for _, op := range s.list.Ops {
		if op.Kind == render.OpImage {
			images++
		}
	}
	if images != 1 {
		t.Errorf("%d images drawn, want the screenshot", images)
	}
}
