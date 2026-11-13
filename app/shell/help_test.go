package shell_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

func help(s *apptest.Scene) *shell.Help { return apptest.PluginOf[*shell.Help](s, "help") }

// Every interactive element resolves to a help page and anchor, across the app's surfaces.
func TestHelpLint(t *testing.T) {
	s := omniScene(t, "pri")
	s.Lint("omnibox")
	omni(s).Close()
	s.Run("picker.open", registry.Args{"source": "files", "query": "go"})
	s.Frame()
	s.Frame()
	s.Lint("picker")
	s.Press("<Esc>")
	for i, sev := range []core.Severity{core.SevError, core.SevWarning, core.SevInfo} {
		s.Host().Notify(core.Notice{ID: fmt.Sprint("n", i), Severity: sev, Text: "a notice", Time: s.Now,
			Actions: []core.Action{{Title: "Do it", Command: "Log"}}})
	}
	s.Run("Log", nil)
	s.Frame()
	s.Frame()
	s.Lint("toasts and the log")
	s.Run("help.open", registry.Args{"id": "errors#log"})
	s.Frame()
	s.Frame()
	s.Lint("a help page")
	help(s).Close()
	s.Run("help.mode", nil)
	s.Frame()
	s.Frame()
	s.Lint("help mode")
	help(s).EndHelpMode()
	s.Run("theme.edit", nil)
	s.Frame()
	s.Frame()
	s.Lint("the theme editor")
	s.Run("theme.pick", nil)
	s.Frame()
	s.Frame()
	s.Lint("pick mode")
	s.Type("Esc")
	s.Frame()
	s.Run("settings.open", registry.Args{"section": "ui"})
	s.Frame()
	s.Frame()
	s.Lint("the settings page")
	s.Close("settings")
	s.Run("lua", nil)
	s.Run("Hooks", nil)
	s.Frame()
	s.Frame()
	s.Lint("the Lua REPL and :Hooks")
}

func TestHelpWays(t *testing.T) {
	s := apptest.EditorScene(t)
	hv := help(s)
	// :help topic, by words of a title.
	s.EditorKeys(":help pickers<CR>")
	s.Frame()
	if !hv.Open() || hv.Page() != "shell/pickers" {
		t.Errorf(":help pickers opened %q", hv.Page())
	}
	text := strings.ReplaceAll(s.Text(), "\n", "")
	if !strings.Contains(text, "A picker searches one source") || !strings.Contains(text, "Resume the last picker") {
		t.Errorf("the page:\n%s", s.Text())
	}
	// Related pages, then ⌃o back.
	s.Run("help.open", registry.Args{"id": "shell/omnibox"})
	s.Run("help.back", nil)
	if hv.Page() != "shell/pickers" {
		t.Errorf("back went to %q", hv.Page())
	}
	s.Run("help.forward", nil)
	if hv.Page() != "shell/omnibox" {
		t.Errorf("forward went to %q", hv.Page())
	}
	hv.Close()
	s.Frame()
	// F1 on what has focus: the editor pane.
	s.Run("help.focused", nil)
	if hv.Page() != "editor/pane" {
		t.Errorf("F1 opened %q", hv.Page())
	}
	hv.Close()
	// Right-click anything: Help is its menu's last item.
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 300, Y: 300}})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 300, Y: 300}, Buttons: render.ButtonSecondary})
	s.Frame()
	s.Frame()
	if !strings.Contains(s.Text(), "? Help") {
		t.Fatalf("no Help in the right-click menu:\n%s", s.Text())
	}
	// Help mode: a ? on everything with help; clicking one opens its page.
	s.Run("help.mode", nil)
	s.Frame()
	s.Frame()
	if n := strings.Count(s.Text(), "?"); n < 3 {
		t.Errorf("help mode shows %d markers", n)
	}
}

// 28-help: a page, against a golden.
func TestHelpSnapshot(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), Size: render.Point{X: 1600, Y: 1000}})
	s.Run("help.open", registry.Args{"id": "errors#log"})
	s.Golden("help-page")
}

// :help api shows the Lua API's reference, generated from the API itself.
func TestHelpShowsTheLuaAPI(t *testing.T) {
	s := apptest.EditorScene(t)
	s.EditorKeys(":help Lua API<CR>")
	s.Frame()
	s.Frame()
	text := strings.ReplaceAll(s.Text(), "\n", " ")
	for _, want := range []string{"oriel.agent.tool(spec)", "Offer a tool to agents over MCP", "oriel.keymap"} {
		if !strings.Contains(text, want) {
			t.Errorf("the API page lacks %q:\n%s", want, s.Text())
		}
	}
	if strings.Contains(text, "arrives with") {
		t.Error("the page still says the reference is to come")
	}
}
