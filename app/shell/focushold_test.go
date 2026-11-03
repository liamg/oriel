package shell_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// focusField is a text field somewhere in the app, how it's opened, and its accessible name.
type focusField struct {
	name  string
	open  func(s *apptest.Scene)
	field string
	view  bool // it's in a view in the layout, so it can sit beside the others
}

func runOpen(id string, args registry.Args) func(s *apptest.Scene) {
	return func(s *apptest.Scene) { s.Run(id, args) }
}

var (
	openConsole  = runOpen("view.open", registry.Args{"id": kit.AgentConsoleView, "place": "tab", "title": "Agent"})
	openRequests = runOpen("view.open", registry.Args{"id": kit.RequestsView, "place": "dock", "edge": "left", "size": 320})
)

// focusFields are the fields of every view, page and dialog the scene can open.
var focusFields = []focusField{
	{"agent console", openConsole, "Message the agent", true},
	{"requests", openRequests, "Search requests", true},
	{"request", runOpen("request.open", nil), "Write to the request", true},
	{"search", runOpen("search.open", nil), "Find", true},
	{"search replace", runOpen("search.open", nil), "Replace", true},
	{"search files", runOpen("search.open", nil), "Files", true},
	{"log", runOpen("log.open", nil), "Filter the log", true},
	{"debug watch", runOpen("debug.open", nil), "Add a watch", true},
	{"assistant", runOpen("assistant.open", nil), "Ask the assistant", false},
	{"directories", runOpen("directory.open", nil), "Filter Directories", false},
	{"quickfix", runOpen("quickfix.open", nil), "Filter Quickfix", false},
	{"settings", runOpen("settings.open", nil), "Search settings", false},
	{"plugins", runOpen("plugins.open", nil), "Filter plugins", false},
	{"prompts", runOpen("prompts.open", nil), "Coding style text", false},
	{"reviews", runOpen("reviews.open", nil), "Filter reviews", false},
	{"tickets", runOpen("tickets.open", nil), "Filter tickets", false},
	{"jobs", runOpen("jobs.open", nil), "Filter jobs", false},
	{"new job title", runOpen("jobs.new", nil), "Title", false},
	{"new job prompt", runOpen("jobs.new", nil), "Prompt", false},
	{"repos", runOpen("repos.open", nil), "Filter repos", false},
	{"new repo", runOpen("repos.new", nil), "Directory", false},
	{"theme editor", runOpen("theme.edit", nil), "Filter tokens", false},
	{"theme expression", runOpen("theme.edit", nil), "Expression", false},
	{"mission control", runOpen("mission.open", nil), "Filter sessions", false},
	{"new session prompt", runOpen("session.new", nil), "Prompt", false},
	{"new session ticket", runOpen("session.new", nil), "Ticket", false},
	{"omnibox", runOpen("omnibox.open", nil), "Search", false},
}

// grabbers are the views that hold focus as they're used: each is open beside a field, in turn.
var grabbers = []struct {
	name string
	open func(s *apptest.Scene)
}{
	{"editor", runOpen("editor.open", registry.Args{"path": "notes.txt"})},
	{"inbox", runOpen("inbox.open", nil)},
	{"agent console", openConsole},
	{"requests", openRequests},
}

// focusScene is the app on a project with a file and two sessions.
func focusScene(t *testing.T) *apptest.Scene {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("one\ntwo\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	for _, ev := range []core.Event{
		core.SessionOpened{ID: "config", Kind: core.KindWork, Status: core.StatusQuiet},
		core.SessionOpened{ID: "pricing-rounding", Kind: core.KindWork, Status: core.StatusWorking},
		core.SessionFocused{ID: "config"},
	} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Now = apptest.At(14, 7, 21)
	s.Frame()
	return s
}

// textbox is the last frame's text field named name; "" is the topmost one.
func textbox(s *apptest.Scene, name string) (*render.AccessNode, bool) {
	var found *render.AccessNode
	var walk func(n *render.AccessNode)
	walk = func(n *render.AccessNode) {
		if n.Role == render.RoleTextBox && (name == "" || n.Name == name) {
			found = n
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(s.Access())
	return found, found != nil
}

// clickInto clicks into the field named name, and says what holds focus after.
func clickInto(s *apptest.Scene, t *testing.T, name string) ui.ID {
	t.Helper()
	for range 3 {
		s.Frame() // laid out as it settles: a strip that wraps moves what's under it
	}
	n, ok := textbox(s, name)
	if !ok {
		t.Fatalf("no field %q", name)
	}
	b := n.Bounds
	s.Click(render.Point{X: (b.Min.X + b.Max.X) / 2, Y: (b.Min.Y + b.Max.Y) / 2}, 0)
	if f := s.UI().Focused(); f != ui.ID(n.ID) {
		t.Fatalf("a click into %q left focus on %v", name, f)
	}
	return ui.ID(n.ID)
}

// keepsFocus runs 3s of frames, with the clock moving, work landing from off the loop and the store changing, and
// fails if focus leaves id.
func keepsFocus(s *apptest.Scene, t *testing.T, what string, id ui.ID) {
	t.Helper()
	statuses := []core.Status{core.StatusWorking, core.StatusQuiet}
	for i := range 30 {
		s.Now = s.Now.Add(100 * time.Millisecond)
		s.RunQueued()
		if i%5 == 0 {
			s.Store().Apply(core.SessionStatusChanged{ID: "pricing-rounding", Status: statuses[i/5%2]})
			s.Store().Apply(core.SessionFocused{ID: "config"})
		}
		s.Frame()
		if f := s.UI().Focused(); f != id {
			t.Fatalf("%s: %v after %v, focus jumped from the field to %v", what, i, time.Duration(i+1)*100*time.Millisecond, f)
		}
	}
}

// Focus has one owner: click into a field and focus stays there through timers, async loads and store
// events: alone, beside each view that holds focus, and after an overlay over it comes and goes.
func TestFocusStaysInAClickedField(t *testing.T) {
	for _, f := range focusFields {
		t.Run(f.name, func(t *testing.T) {
			s := focusScene(t)
			f.open(s)
			s.Frame()
			keepsFocus(s, t, "alone", clickInto(s, t, f.field))
		})
		if !f.view {
			continue
		}
		for _, g := range grabbers {
			if g.name == f.name {
				continue
			}
			t.Run(f.name+" beside "+g.name, func(t *testing.T) {
				s := focusScene(t)
				f.open(s)
				g.open(s)
				s.Run("layout.move", registry.Args{"place": "split", "side": "right"})
				s.Frame()
				keepsFocus(s, t, "beside "+g.name, clickInto(s, t, f.field))
			})
		}
		t.Run(f.name+" under the omnibox", func(t *testing.T) {
			s := focusScene(t)
			f.open(s)
			s.Frame()
			id := clickInto(s, t, f.field)
			s.Run("omnibox.open", nil)
			s.Frame()
			s.Key("Esc")
			s.Frame()
			if got := s.UI().Focused(); got != id {
				t.Fatalf("with the omnibox closed focus is on %v, not back in the field", got)
			}
			keepsFocus(s, t, "after the omnibox", id)
		})
	}
}
