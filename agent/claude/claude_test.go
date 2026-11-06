package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/claude/claudebox"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/hookcall"
)

func TestGuardRails(t *testing.T) {
	spec := agent.StartSpec{Cwd: "/w/ws", Protect: []string{"/src/repo", "/w"}}
	sandboxed := spec
	sandboxed.Sandbox = &agent.Sandbox{Name: "box"}
	cases := []struct {
		name     string
		spec     agent.StartSpec
		tool     string
		input    string
		decision string
		reason   string
	}{
		{"Oriel's tools go ahead", spec, "mcp__oriel__reply", `{}`, "allow", ""},
		{"an edit of the checkout", spec, "Edit", `{"file_path":"/src/repo/a.go"}`, "deny", "original checkout"},
		{"a notebook in the checkout", spec, "NotebookEdit", `{"notebook_path":"/src/repo/n.ipynb"}`, "deny", "original checkout"},
		{"an edit of the session's own copy, inside a protected root", spec, "Write", `{"file_path":"/w/ws/a.go"}`, "", ""},
		{"a relative path stays in cwd", spec, "Edit", `{"file_path":"a.go"}`, "", ""},
		{"a neighbour that only shares a prefix", spec, "Edit", `{"file_path":"/src/repository/a.go"}`, "", ""},
		{"a read of the checkout", spec, "Read", `{"file_path":"/src/repo/a.go"}`, "", ""},
		{"git push in a sandbox", sandboxed, "Bash", `{"command":"cd x && git -C y push origin main"}`, "deny", "mcp__oriel-gw__oriel__push"},
		{"gh in a sandbox", sandboxed, "Bash", `{"command":"gh pr create"}`, "deny", "mcp__oriel-gw__oriel__pr_create"},
		{"git push on the host", spec, "Bash", `{"command":"git push"}`, "", ""},
		{"a quoted git push is an argument", sandboxed, "Bash", `{"command":"echo 'git push'"}`, "", ""},
		{"git status in a sandbox", sandboxed, "Bash", `{"command":"git status"}`, "", ""},
		// The agent never posts to a review, on the host or in a sandbox, by gh or by MCP.
		{"a review reply by gh api on the host", spec, "Bash", `{"command":"gh api repos/acme/api/pulls/7/comments/9/replies -f body=Agreed"}`, "deny", "never post"},
		{"a review by gh in a sandbox", sandboxed, "Bash", `{"command":"gh pr review 7 --approve"}`, "deny", "never post"},
		{"a GitHub MCP review reply", spec, "mcp__github__add_reply_to_pull_request_comment", `{}`, "deny", "never post"},
		{"the gateway's GitHub comment tool", sandboxed, "mcp__oriel-gw__github__add_issue_comment", `{}`, "deny", "never post"},
		{"reading the review on the host", spec, "Bash", `{"command":"gh api repos/acme/api/pulls/7/comments"}`, "", ""},
	}
	for _, c := range cases {
		got := check(c.spec, c.tool, json.RawMessage(c.input))
		if got.Decision != c.decision || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: %+v, want %q with %q", c.name, got, c.decision, c.reason)
		}
	}
}

func TestPolicyToFlags(t *testing.T) {
	box := &agent.Sandbox{Name: "box", GatewayMCP: "/run/gw.json"}
	cases := []struct {
		spec agent.StartSpec
		want string
	}{
		{agent.StartSpec{}, "--permission-mode default"},
		{agent.StartSpec{Policy: agent.PolicyAsk}, "--permission-mode default"},
		{agent.StartSpec{Policy: agent.PolicyLight}, "--permission-mode acceptEdits"},
		{agent.StartSpec{Policy: agent.PolicyAuto}, "--permission-mode auto"},
		{agent.StartSpec{Policy: agent.PolicyAuto, Sandbox: box}, "--permission-mode bypassPermissions"},
		{agent.StartSpec{Policy: agent.PolicyBypass, Sandbox: box}, "--permission-mode bypassPermissions --mcp-config /run/gw.json --strict-mcp-config"},
		// In a sandbox the sandbox is the gate: Claude asks nothing in its own terminal, where nobody would see it.
		{agent.StartSpec{Policy: agent.PolicyLight, Sandbox: box}, "--permission-mode bypassPermissions"},
		{agent.StartSpec{Policy: agent.PolicyAsk, Sandbox: box}, "--permission-mode bypassPermissions"},
	}
	for _, c := range cases {
		a, err := args(c.spec, "/m", "id", "")
		if err != nil || !strings.Contains(strings.Join(a, " "), c.want) {
			t.Errorf("%+v: %q, %v; want %q", c.spec, a, err, c.want)
		}
	}
	if _, err := args(agent.StartSpec{Policy: agent.PolicyBypass}, "/m", "id", ""); err == nil {
		t.Error("bypass without a sandbox started")
	}
	if _, err := args(agent.StartSpec{Policy: "yolo"}, "/m", "id", ""); err == nil {
		t.Error("an unknown policy started")
	}
}

func TestArgs(t *testing.T) {
	spec := agent.StartSpec{Model: "opus", Effort: "high", Pack: agent.Pack{Skill: "plan"}, Prompt: "add a cache"}
	a, _ := args(spec, "/m", "s1", "")
	want := []string{"--plugin-dir", "/m", "--permission-mode", "default", "--model", "opus", "--effort", "high", "--session-id", "s1", "/oriel:plan add a cache"}
	if !slices.Equal(a, want) {
		t.Errorf("fresh = %q\nwant %q", a, want)
	}
	a, _ = args(spec, "/m", "s1", "s1")
	if !slices.Contains(a, "--resume") || slices.Contains(a, "--session-id") || strings.Contains(strings.Join(a, " "), "/oriel:") {
		t.Errorf("resumed = %q: it has its skill and history already", a)
	}
	a, _ = args(agent.StartSpec{Prompt: "-x"}, "/m", "s1", "")
	if a[len(a)-2] != "--" {
		t.Errorf("a prompt like a flag = %q", a)
	}
	// Linux takes no argument over 128 KiB (MAX_ARG_STRLEN): a longer first prompt is submitted once claude is up.
	a, _ = args(agent.StartSpec{Pack: agent.Pack{Skill: "work"}, Prompt: strings.Repeat("x", maxArgPrompt+1)}, "/m", "s1", "")
	if a[len(a)-1] != "/oriel:work" {
		t.Errorf("a long first prompt = …%q", a[len(a)-1][:min(40, len(a[len(a)-1]))])
	}
	if !submitsFirst(agent.StartSpec{Prompt: strings.Repeat("x", maxArgPrompt+1)}, "") || submitsFirst(spec, "") || !submitsFirst(spec, "s1") {
		t.Error("submitsFirst: a long or resumed first prompt is submitted, a short fresh one goes on the command line")
	}
	// AGENTS.md: "Claude is driven through its interactive CLI in a PTY with hooks, never claude -p". A utility
	// agent (the assistant) is no exception: its turn goes in as the skill's prompt.
	a, _ = args(agent.StartSpec{Model: "haiku", Pack: agent.Pack{Skill: "assistant"}, Prompt: "what needs me?"}, "/m", "s1", "")
	want = []string{"--plugin-dir", "/m", "--permission-mode", "default", "--model", "haiku", "--session-id", "s1", "/oriel:assistant what needs me?"}
	if !slices.Equal(a, want) || slices.Contains(a, "-p") || slices.Contains(a, "--print") {
		t.Errorf("the assistant = %q\nwant %q", a, want)
	}
	a, _ = args(agent.StartSpec{NoMCP: true}, "/m", "s1", "")
	if j := strings.Join(a, " "); !strings.Contains(j, `--mcp-config {"mcpServers":{}} --strict-mcp-config`) {
		t.Errorf("no MCP = %q", a)
	}
}

// Outside a sandbox, the memory directory goes on the command line; a sandbox's settings.json has it.
func TestArgsSetTheMemoryDirectory(t *testing.T) {
	a, _ := args(agent.StartSpec{Memory: "/mem"}, "/m", "s1", "s1")
	if i := slices.Index(a, "--settings"); i < 0 || a[i+1] != `{"autoMemoryDirectory":"/mem"}` {
		t.Errorf("args = %q", a)
	}
	a, _ = args(agent.StartSpec{Memory: "/mem", Sandbox: &agent.Sandbox{}}, "/m", "s1", "")
	if slices.Contains(a, "--settings") {
		t.Errorf("sandboxed args = %q", a)
	}
}

func TestWriteMod(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mod")
	if err := writeMod(dir, agent.Pack{Name: "plan", Skill: "plan", Text: "# Plan\n\nDraft the docs first."}); err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Name string }
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil || json.Unmarshal(b, &manifest) != nil || manifest.Name != "oriel" {
		t.Fatalf("plugin.json = %s, %v", b, err)
	}
	var hooks struct{ Modules []string }
	b, err = os.ReadFile(filepath.Join(dir, "hooks", "hooks.json"))
	if err != nil || json.Unmarshal(b, &hooks) != nil || !slices.Equal(hooks.Modules, []string{"./register.ts"}) {
		t.Fatalf("hooks.json = %s, %v", b, err)
	}
	ts, err := os.ReadFile(filepath.Join(dir, "hooks", "register.ts"))
	if err != nil || !bytes.Contains(ts, []byte("ORIEL_SPAWNED")) {
		t.Fatalf("register.ts: %v", err)
	}
	skill, err := os.ReadFile(filepath.Join(dir, "skills", "plan", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(skill); !strings.HasPrefix(s, "---\nname: plan\ndescription: ") || !strings.Contains(s, "Draft the docs first.") {
		t.Errorf("SKILL.md = %s", s)
	}
	// Written again for another pack, the old skill goes.
	if err := writeMod(dir, agent.Pack{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills")); !os.IsNotExist(err) {
		t.Error("a pack with no skill kept the last one's")
	}
	if err := writeMod(dir, agent.Pack{Skill: "../x"}); err == nil {
		t.Error("a skill name that's a path was written")
	}
}

func TestHookMain(t *testing.T) {
	if agent.HookSocketEnv != hookcall.SocketEnv {
		t.Fatalf("the provider points the mod at %s, but oriel hook reads %s", agent.HookSocketEnv, hookcall.SocketEnv)
	}
	dir, err := sockDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", sockPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req request
			_ = json.NewDecoder(c).Decode(&req)
			res := response{Body: json.RawMessage(`{"op":"` + req.Op + `","got":` + string(req.Body) + `}`)}
			if req.Op == "bad" {
				res = response{Error: "no such op"}
			}
			_ = json.NewEncoder(c).Encode(res)
			c.Close()
		}
	}()
	t.Setenv("ORIEL_HOOK_SOCK", sockPath(dir))
	var out, errs bytes.Buffer
	if code := HookMain([]string{"check"}, strings.NewReader(`{"tool":"Bash"}`), &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if got := strings.TrimSpace(out.String()); got != `{"op":"check","got":{"tool":"Bash"}}` {
		t.Errorf("printed %s", got)
	}
	errs.Reset()
	if code := HookMain([]string{"bad"}, strings.NewReader(`{}`), &out, &errs); code != 1 || !strings.Contains(errs.String(), "no such op") {
		t.Errorf("a refused op: exit %d, %q", code, errs.String())
	}
	if code := HookMain([]string{"check"}, strings.NewReader(`not json`), &out, &errs); code != 1 {
		t.Errorf("bad stdin: exit %d", code)
	}
	if code := HookMain(nil, strings.NewReader(""), &out, &errs); code != 2 {
		t.Errorf("no op: exit %d", code)
	}
	t.Setenv("ORIEL_HOOK_SOCK", "")
	if code := HookMain([]string{"wait"}, strings.NewReader("{}"), &out, &errs); code != 1 {
		t.Errorf("outside a session: exit %d", code)
	}
}

func TestSockPathIsShort(t *testing.T) {
	dir, err := sockDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if n := len(sockPath(dir)); n > 100 {
		t.Errorf("socket path is %d bytes; macOS allows 104", n)
	}
}

func TestTrustKeepsExistingState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := t.TempDir()
	body := `{"numStartups": 3, "userID": 12345678901234567890, "projects": {"/elsewhere": {"allowedTools": ["Bash"]}, "` + dir + `": {"lastCost": 1.5}}}`
	if err := os.WriteFile(statePath(nil), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := trust(nil, dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(statePath(nil))
	if !strings.Contains(string(b), "12345678901234567890") {
		t.Errorf("numbers changed: %s", b)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	projects := m["projects"].(map[string]any)
	if m["numStartups"] != 3.0 || projects["/elsewhere"].(map[string]any)["allowedTools"] == nil {
		t.Fatalf("lost what was there: %s", b)
	}
	if p := projects[dir].(map[string]any); p["hasTrustDialogAccepted"] != true || p["lastCost"] != 1.5 {
		t.Fatalf("not trusted in place: %v", p)
	}
}

// Claude Code 2.1 asks about a CLAUDE.md importing files from outside the folder unless the project has an answer:
// "if(r.hasClaudeMdExternalIncludesApproved||r.hasClaudeMdExternalIncludesWarningShown)return!1" (its bundle).
// Nobody is at the PTY, so the dialog's own default, No, is the answer; one the user gave is kept.
func TestTrustAnswersTheExternalImportsDialog(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}
	fresh, approved := t.TempDir(), t.TempDir()
	body := `{"projects": {"` + approved + `": {"hasClaudeMdExternalIncludesApproved": true, "hasClaudeMdExternalIncludesWarningShown": true}}}`
	if err := os.WriteFile(statePath(env), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{fresh, approved} {
		if err := trust(env, d); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(statePath(env))
	var m struct{ Projects map[string]map[string]any }
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if p := m.Projects[fresh]; p["hasClaudeMdExternalIncludesWarningShown"] != true || p["hasClaudeMdExternalIncludesApproved"] == true {
		t.Errorf("a new project's dialog isn't answered No: %v", p)
	}
	if p := m.Projects[approved]; p["hasClaudeMdExternalIncludesApproved"] != true {
		t.Errorf("the user's Yes was lost: %v", p)
	}
}

func TestTrustUsesTheSessionsConfigDirAndTheResolvedPath(t *testing.T) {
	cfg := t.TempDir()
	env := []string{"CLAUDE_CONFIG_DIR=" + cfg}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	if err := trust(env, link); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(cfg, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(link)
	for _, d := range []string{link, resolved} {
		if !strings.Contains(string(b), `"`+d+`"`) {
			t.Errorf("%s not trusted: %s", d, b)
		}
	}
}

func TestTrustRefusesToOverwriteBadJSON(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}
	if err := os.WriteFile(statePath(env), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := trust(env, t.TempDir()); err == nil {
		t.Fatal("no error for invalid JSON")
	}
	if b, _ := os.ReadFile(statePath(env)); string(b) != "{nope" {
		t.Fatalf("overwritten: %q", b)
	}
}

func TestAgentDefaultsNeverOverrideTheUser(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}
	body := `{"promptSuggestionEnabled": true, "env": {"FOO": "1"}, "hooks": {"Stop": []}}`
	if err := os.WriteFile(settingsPath(env), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaults(env); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	b, _ := os.ReadFile(settingsPath(env))
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	envs := m["env"].(map[string]any)
	if m["promptSuggestionEnabled"] != true || envs["FOO"] != "1" || m["hooks"] == nil {
		t.Errorf("the user's settings changed: %s", b)
	}
	if envs["CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY"] != "1" || m["awaySummaryEnabled"] != false {
		t.Errorf("defaults missing: %s", b)
	}
	fi, _ := os.Stat(settingsPath(env))
	if err := ensureDefaults(env); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.Stat(settingsPath(env)); !again.ModTime().Equal(fi.ModTime()) {
		t.Error("rewritten with nothing missing")
	}
}

func TestDetect(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("ANTHROPIC_API_KEY", "")
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '2.1.296 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := (&Provider{Bin: bin}).Detect(context.Background())
	if err != nil || in.Version != "2.1.296" || in.LoggedIn || in.Note == "" {
		t.Fatalf("signed out: %+v, %v", in, err)
	}
	creds := `{"claudeAiOauth":{"accessToken":"tok","subscriptionType":"max"}}`
	if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ = (&Provider{Bin: bin}).Detect(context.Background())
	if !in.LoggedIn || in.Account != "Claude Max" || in.Note != "" || in.Login == "" || strings.Contains(in.Login, "tok") {
		t.Fatalf("signed in: %+v", in)
	}
	// A new token on disk is a new login.
	if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(strings.Replace(creds, "tok", "tok2", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if again, _ := (&Provider{Bin: bin}).Detect(context.Background()); !again.LoggedIn || again.Login == in.Login {
		t.Errorf("a new token: %+v, was %+v", again, in)
	}
	if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '2.0.1 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if in, _ = (&Provider{Bin: bin}).Detect(context.Background()); !strings.Contains(in.Note, "too old") {
		t.Errorf("an old claude: %+v", in)
	}
	if in, _ = (&Provider{Bin: filepath.Join(cfg, "none")}).Detect(context.Background()); in.Note == "" {
		t.Errorf("no claude: %+v", in)
	}
}

// With the token in the Keychain, Detect marks it from there, read afresh each time, and hands it to the
// sandboxes: so a login renewed on the host tells from the one that was refused.
func TestDetectMarksTheKeychainsToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("ANTHROPIC_API_KEY", "")
	if err := os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"organizationType":"claude_max"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '2.1.296 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "ws")
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	tok := "k1"
	p := &Provider{Bin: bin}
	p.box.Keychain = func(context.Context) ([]byte, error) {
		return json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": tok,
			"expiresAt": time.Now().Add(time.Hour).UnixMilli()}})
	}
	first, _ := p.Detect(context.Background())
	if !first.LoggedIn || first.Login == "" {
		t.Fatalf("signed in: %+v", first)
	}
	tok = "k2"
	again, _ := p.Detect(context.Background())
	if again.Login == "" || again.Login == first.Login {
		t.Errorf("a renewed token: %q, was %q", again.Login, first.Login)
	}
	b, _ := os.ReadFile(filepath.Join(claudebox.Dir(root), ".credentials.json"))
	if !strings.Contains(string(b), `"k2"`) {
		t.Errorf("the sandbox holds %s", b)
	}
}

func TestLoginFromTheAccountWhenTheTokenIsInTheKeychain(t *testing.T) {
	cfg := t.TempDir()
	env := []string{"CLAUDE_CONFIG_DIR=" + cfg}
	if err := os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"organizationType":"claude_pro"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, account, _ := login(env); !ok || account != "Claude Pro" {
		t.Errorf("login = %v, %q", ok, account)
	}
}

func TestModels(t *testing.T) {
	ms, _ := (&Provider{}).Models(context.Background())
	defaults := 0
	for _, m := range ms {
		if m.Default {
			defaults++
		}
		if m.Context == 0 {
			t.Errorf("%s has no context size", m.ID)
		}
	}
	if defaults != 1 {
		t.Errorf("%d defaults", defaults)
	}
}

// A turn's end carries what it used, and the plan's windows reach Usage.
func TestTurnUsageAndPlanWindows(t *testing.T) {
	h := fakeHarness(t)
	s := agenttest.Start(t, h)
	ready := s.Wait("ready", func(ev agent.Event) bool { _, ok := ev.(agent.Ready); return ok }).(agent.Ready)
	if ready.ID != s.S.ID() {
		t.Errorf("ready as %q, the session is %q", ready.ID, s.S.ID())
	}
	s.Send("say hello")
	end := s.Wait("the turn's end", func(ev agent.Event) bool { _, ok := ev.(agent.TurnEnd); return ok }).(agent.TurnEnd)
	u := end.Usage
	if u.Input != 10 || u.Output != 5 || u.CacheRead != 100 || u.CacheWrite != 20 || u.Context != 130 || u.ContextMax != 200000 || u.Cost <= 0 {
		t.Errorf("usage = %+v", u)
	}
	up := s.Wait("the plan's windows", func(ev agent.Event) bool { _, ok := ev.(agent.UsageUpdate); return ok }).(agent.UsageUpdate)
	if len(up.Usage.Windows) != 1 || up.Usage.Windows[0].Name != "Current session" || up.Usage.Windows[0].Used != 0.125 {
		t.Errorf("windows = %+v", up.Usage)
	}
	p := h.Provider.(*Provider)
	p.Bin = filepath.Join(t.TempDir(), "none") // no probe: the session's reading
	got, _ := p.Usage(context.Background())
	if got.Provider != "claude" || len(got.Windows) != 1 || got.Windows[0].Resets.IsZero() {
		t.Errorf("Usage = %+v", got)
	}
}

// A turn that leaves background work running says what it is: the Stop hook's background_tasks.
func TestTurnEndCarriesBackgroundWork(t *testing.T) {
	h := fakeHarness(t)
	s := agenttest.Start(t, h)
	s.Wait("ready", func(ev agent.Event) bool { _, ok := ev.(agent.Ready); return ok })
	s.Send("test in the background")
	end := s.Wait("the turn's end", func(ev agent.Event) bool { _, ok := ev.(agent.TurnEnd); return ok }).(agent.TurnEnd)
	if len(end.Background) != 1 || end.Background[0] != (core.BackgroundTask{Kind: "shell", Description: "Run the suite"}) {
		t.Errorf("background = %+v", end.Background)
	}
}

func TestMessagesFromTheLog(t *testing.T) {
	h := fakeHarness(t)
	s := agenttest.Start(t, h)
	s.Send("say hello")
	user := s.Wait("the prompt as the agent got it", func(ev agent.Event) bool {
		m, ok := ev.(agent.Message)
		return ok && m.Role == core.ItemUser
	}).(agent.Message)
	if user.Text != "say hello" {
		t.Errorf("user message = %+v", user)
	}
}

// With nothing to fork yet, the session's model answers alone.
func TestForkBeforeAnything(t *testing.T) {
	h := fakeHarness(t)
	s := agenttest.Start(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if got, err := s.S.Fork(ctx, agent.ForkSpec{Prompt: "why?"}); err != nil || got != "complete: why?" {
		t.Errorf("fork = %q, %v; want the model's answer alone", got, err)
	}
}

func TestTerminal(t *testing.T) {
	h := fakeHarness(t)
	s := agenttest.Start(t, h)
	term := s.S.Terminal()
	if term == nil {
		t.Fatal("no terminal")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		backlog, _, detach := term.Attach()
		detach()
		if bytes.Contains(backlog, []byte("fake claude "+s.S.ID())) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backlog = %q", backlog)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := term.Resize(100, 30); err != nil {
		t.Error(err)
	}
	_, live, detach := term.Attach()
	if err := s.S.Close(); err != nil {
		t.Fatal(err)
	}
	for range live {
	}
	detach()
	s.Wait("the process to exit", func(ev agent.Event) bool { _, ok := ev.(agent.Exited); return ok })
}

func TestStartRefusesBypassOutsideASandbox(t *testing.T) {
	p := fakeProvider(t)
	_, err := p.Start(context.Background(), agent.StartSpec{Cwd: t.TempDir(), Policy: agent.PolicyBypass})
	if err == nil {
		t.Fatal("started with nothing gating the agent")
	}
}

func fakeHarness(t *testing.T) agenttest.Harness {
	return agenttest.Harness{
		Provider: fakeProvider(t),
		Spec: func(t *testing.T, dir string) agent.StartSpec {
			return agent.StartSpec{StateDir: t.TempDir(), Env: []string{"ORIEL_FAKE_CLAUDE=1"}}
		},
		Timeout: 20 * time.Second,
	}
}

// A plan belongs to an account: who Claude is signed in as tells a company subscription from a personal one.
func TestAccount(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	if id, _ := account(nil); id != "" {
		t.Errorf("signed out: %q", id)
	}
	os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"u1","emailAddress":"ada@work.example",
		"organizationUuid":"o1","organizationName":"Work Co"}}`), 0o600)
	if id, name := account(nil); id != "u1/o1" || name != "ada@work.example" {
		t.Errorf("account = %q %q", id, name)
	}
}

// fixtureConfig copies testdata/account (Claude Code's .claude.json and .credentials.json: the real files' keys,
// invented values) into a config directory, with edit applied to each file's JSON first.
func fixtureConfig(t *testing.T, edit func(file string, v map[string]any)) []string {
	t.Helper()
	cfg := t.TempDir()
	for _, f := range []string{".claude.json", ".credentials.json"} {
		b, err := os.ReadFile(filepath.Join("testdata", "account", f))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		if edit != nil {
			edit(f, v)
		}
		if v == nil {
			continue
		}
		b, _ = json.Marshal(v)
		if err := os.WriteFile(filepath.Join(cfg, f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return []string{"CLAUDE_CONFIG_DIR=" + cfg}
}

// The account is read from the state file's oauthAccount by the keys Claude Code writes.
func TestAccountFromTheFixture(t *testing.T) {
	env := fixtureConfig(t, nil)
	if id, name := account(env); id != "11111111-1111-4111-8111-111111111111/22222222-2222-4222-8222-222222222222" || name != "ada@acme.example" {
		t.Errorf("account = %q %q", id, name)
	}
}

// The plan's tier, for its chip, from the provider's own account data: the credentials' subscriptionType and
// rateLimitTier, else (the token in the Keychain) oauthAccount's organizationType and organizationRateLimitTier. A
// plan Claude Code doesn't name has no tier, rather than a guess.
func TestPlanTier(t *testing.T) {
	set := func(file string, kv map[string]any) func(string, map[string]any) {
		return func(f string, v map[string]any) {
			if f != file {
				return
			}
			o := v["claudeAiOauth"]
			if file == ".claude.json" {
				o = v["oauthAccount"]
			}
			for k, x := range kv {
				o.(map[string]any)[k] = x
			}
		}
	}
	keychain := func(kv map[string]any) func(string, map[string]any) {
		return func(f string, v map[string]any) {
			if f == ".credentials.json" {
				clear(v)
				return
			}
			set(".claude.json", kv)(f, v)
		}
	}
	for _, c := range []struct {
		name       string
		edit       func(string, map[string]any)
		plan, tier string
	}{
		{"max 20x", nil, "Claude Max", "Max 20×"},
		{"max 5x", set(".credentials.json", map[string]any{"rateLimitTier": "default_claude_max_5x"}), "Claude Max", "Max 5×"},
		{"max, no tier", set(".credentials.json", map[string]any{"rateLimitTier": nil}), "Claude Max", "Max"},
		{"pro", set(".credentials.json", map[string]any{"subscriptionType": "pro", "rateLimitTier": nil}), "Claude Pro", "Pro"},
		{"team", set(".credentials.json", map[string]any{"subscriptionType": "team", "rateLimitTier": "default_claude_max_5x"}), "Claude Team", "Team"},
		{"enterprise", set(".credentials.json", map[string]any{"subscriptionType": "enterprise"}), "Claude Enterprise", "Enterprise"},
		{"unknown", set(".credentials.json", map[string]any{"subscriptionType": "galaxy"}), "Claude Galaxy", ""},
		{"keychain, max", keychain(nil), "Claude Max", "Max 20×"},
		{"keychain, team", keychain(map[string]any{"organizationType": "claude_team", "organizationRateLimitTier": nil}), "Claude Team", "Team"},
		{"keychain, unknown", keychain(map[string]any{"organizationType": nil, "organizationRateLimitTier": nil}), "", ""},
	} {
		plan, tier := planOf(fixtureConfig(t, c.edit))
		if plan != c.plan || tier != c.tier {
			t.Errorf("%s: plan %q, tier %q; want %q, %q", c.name, plan, tier, c.plan, c.tier)
		}
	}
}

// Usage carries the plan and its tier, from the fixture account.
func TestUsageCarriesTheTier(t *testing.T) {
	p, _ := usageProbe(t)
	u, _ := p.Usage(context.Background())
	if u.Plan != "Claude Max" || u.Tier != "Max 20×" || u.AccountName != "ada@acme.example" {
		t.Errorf("usage = %+v", u)
	}
}

// ref:   semver/semver.md#Semantic Versioning Specification (SemVer)
// quote: "Major, minor, and patch versions are always compared numerically."
func TestVersionLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"2.1.283", "2.1.284", true}, {"2.1.284", "2.1.284", false}, {"2.10.0", "2.9.9", false},
		{"2.1", "2.1.284", true}, {"3", "2.1.284", false}, {"2.1.284", "2", false},
	} {
		if got := versionLess(tc.a, tc.b); got != tc.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
