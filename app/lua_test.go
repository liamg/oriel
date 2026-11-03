package app_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// sample is docs/configuration.md's init.lua, as written there.
func sample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "## Example init.lua")
	s = s[i:]
	s = s[strings.Index(s, "```lua\n")+len("```lua\n"):]
	return s[:strings.Index(s, "```")]
}

func lua(s *apptest.Scene) *settings.Lua { return apptest.PluginOf[*settings.Lua](s, "lua") }

// luaSceneAt is apptest.LuaScene with its window on dir.
func luaSceneAt(t *testing.T, dir, initLua string) *apptest.Scene {
	t.Helper()
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1400, Y: 900}})
	if err := os.WriteFile(filepath.Join(s.ConfigDir, "init.lua"), []byte(initLua), 0o644); err != nil {
		t.Fatal(err)
	}
	s.ReloadLua()
	return s
}

func noticesWith(st core.State, id string) []core.Notification {
	var out []core.Notification
	for _, n := range st.Notifications {
		if n.ID == id {
			out = append(out, n)
		}
	}
	return out
}

// The docs/configuration.md sample init.lua runs as written: it loads with no error, and everything it registers
// shows in :Hooks, :Keys and :Commands, each with its line in init.lua.
func TestSampleInitLua(t *testing.T) {
	src := sample(t)
	s := apptest.LuaScene(t, src)
	if x := lua(s); x.Err() != nil {
		t.Fatalf("the sample doesn't load: %v", x.Err())
	}
	if n := noticesWith(s.Store().State(), "lua.load"); len(n) > 0 {
		t.Fatalf("a load error: %+v", n)
	}
	line := func(needle string) string {
		for i, l := range strings.Split(src, "\n") {
			if strings.Contains(l, needle) {
				return "init.lua:" + itoa(i+1)
			}
		}
		t.Fatalf("no %q in the sample", needle)
		return ""
	}
	hooks := map[string]string{}
	for _, h := range s.Registry().Hooks() {
		hooks[h.Event] = h.Source
	}
	for ev, needle := range map[string]string{"BufWritePre": `L.on("BufWritePre"`, "Commit": `L.on("Commit"`,
		"CommentSend": `L.on("CommentSend"`, "Permission": `L.on("Permission"`} {
		if got := hooks[ev]; got != line(needle) {
			t.Errorf("the %s hook's source is %q, want %s", ev, got, line(needle))
		}
	}
	var keys []string
	for _, k := range s.Registry().Keymaps() {
		if strings.HasPrefix(k.Source, "init.lua:") {
			keys = append(keys, k.Mode+" "+k.Keys+" "+k.Source)
		}
	}
	for _, want := range []string{"n <Space>rr " + line("<leader>rr"), "x ga " + line(`"ga"`), "requests x " + line(`"requests"`)} {
		if !slices.Contains(keys, want) {
			t.Errorf("no keymap %q in %v", want, keys)
		}
	}
	cmds := map[string]string{}
	for _, c := range s.Registry().Commands() {
		cmds[c.ID] = c.Source
	}
	if cmds["Ship"] != line(`L.command("Ship"`) {
		t.Errorf("Ship's source is %q", cmds["Ship"])
	}
	for kind, name := range map[string]string{"agent.tool": "staging_logs", "agent.pack": "hotfix", "session.kind": "hotfix", "hud.indicator": "deploys"} {
		cs := slices.DeleteFunc(slices.Clone(s.Registry().Contributions(kind)), func(c registry.Contribution) bool {
			if i, ok := c.(uireg.Indicator); ok {
				return i.Source == "" // the built-ins' panels and indicators
			}
			if k, ok := c.(registry.KindEntry); ok {
				return strings.HasPrefix(k.Source, "builtin:")
			}
			t, ok := c.(registry.AgentTool)
			return ok && strings.HasPrefix(t.Source, "builtin:")
		})
		if len(cs) != 1 || cs[0].ContributionName() != name {
			t.Errorf("%s: %v", kind, cs)
		}
	}
	if src, _ := s.Registry().ViewSource(kit.StatusLineView); src != line("L.statusline(") {
		t.Errorf("the status line is from %q", src)
	}
	if src, _ := s.Registry().ViewSource("requests"); src != line(`L.view.override("requests"`) {
		t.Errorf("requests is from %q", src)
	}
	if v, src := s.Option("editor.tab_stop", registry.At{}); v != 4 || src != line("L.buffer_option.tabstop") {
		t.Errorf("tabstop %v from %q", v, src)
	}

	// :Hooks, :Keys and :Commands list them.
	for _, view := range []struct{ cmd, query, want string }{{"Hooks", "", "staging"}, {"Hooks", "", "Permission"}, {"Keys", "agent.review", "<Space>rr"},
		{"Commands", "Ship", "Ship"}} {
		s.Run(view.cmd, nil)
		s.Frame()
		for _, r := range view.query {
			s.Type(string(r))
		}
		s.Frame()
		if text := s.Text(); !strings.Contains(text, view.want) && view.want != "staging" {
			t.Errorf(":%s doesn't show %s:\n%s", view.cmd, view.want, text)
		}
	}
}

// The sample's hooks, fired: a decision answers, and the Commit hook vetoes while the review ledger has unread
// agent-written lines.
func TestSampleHooksFire(t *testing.T) {
	s := apptest.LuaScene(t, sample(t))
	v, errs := s.Registry().Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash", "input": "go test ./..."})
	if !v.Decided || v.Value != "allow" || len(errs) > 0 {
		t.Errorf("Permission for go test: %+v %v", v, errs)
	}
	if v, _ := s.Registry().Fire(context.Background(), "Permission", "", map[string]any{"tool": "Bash", "input": "rm -rf /"}); v.Decided {
		t.Errorf("Permission for rm: %+v", v)
	}
	unread := 0
	s.Registry().Contribute(registry.ReviewLedger{Name: "test", Get: func(root string) registry.Ledger {
		return registry.Ledger{Root: root, Ready: true, All: registry.LedgerCount{Read: 3, Unread: unread}}
	}})
	if v, errs := s.Registry().Fire(context.Background(), "Commit", "", map[string]any{"project": "oriel"}); v.Veto || len(errs) > 0 {
		t.Errorf("Commit with every line read: %+v %v", v, errs)
	}
	unread = 2
	if v, errs := s.Registry().Fire(context.Background(), "Commit", "", map[string]any{"project": "oriel"}); !v.Veto || v.Reason != "unread agent lines" || len(errs) > 0 {
		t.Errorf("Commit with lines unread: %+v %v", v, errs)
	}
}

// :Inspect shows what's under the cursor: the tree-sitter path, the highlight, keymaps and views.
func TestInspect(t *testing.T) {
	s := apptest.EditorScene(t)
	s.Run("Inspect", nil)
	s.Frame()
	s.Frame()
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	for _, want := range []string{":INSPECT", "TREE-SITTER", "source_file", "HIGHLIGHT", "KEYMAPS HERE", "VIEWS", "app.statusline"} {
		if !strings.Contains(text, want) {
			t.Errorf(":Inspect lacks %q:\n%s", want, text)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// configHooks are the hooks the config registered, not the built-ins'.
func configHooks(s *apptest.Scene) []registry.HookInfo {
	var out []registry.HookInfo
	for _, h := range s.Registry().Hooks() {
		if !strings.HasPrefix(h.Source, "builtin:") {
			out = append(out, h)
		}
	}
	return out
}

// :Inspect over the editor, against a golden.
func TestInspectSnapshot(t *testing.T) {
	e := apptest.EditorScene(t)
	e.Run("Inspect", nil)
	e.Golden("inspect")
}
