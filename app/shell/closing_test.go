package shell_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
)

func TestLogPanelCloses(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("Log", nil)
	s.Frame()
	s.Frame()
	if !s.Showing("log") {
		t.Fatal(":Log didn't open the log")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.TextPoint("LOG")})
	s.Frame()
	s.Frame()
	s.Click(apptest.Centre(s.Node("Close Log").Bounds), 0)
	if s.Showing("log") {
		t.Error("the header's ✕ didn't close the log")
	}

	s.Run("Log", nil)
	s.Frame()
	k, _ := s.Registry().Lookup([]string{shell.ViewKeyMode, "global"}, keymap.Parse("<Esc>"))
	if k == nil {
		t.Fatal("Escape does nothing in a focused view")
	}
	s.Run(k.Command, k.Args)
	if s.Showing("log") {
		t.Error("Escape didn't close the docked log")
	}

	s.Run("view.open", registry.Args{"id": "log"})
	s.Run(k.Command, k.Args)
	if !s.Showing("log") {
		t.Error("Escape closed a view in the main area")
	}
}

func TestSearchClosesOnTheNextFrame(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Now = time.Unix(1000, 0)
	s.Run("picker.open", registry.Args{"source": "grep"})
	s.Frame()
	s.Frame()
	s.Type("Esc")
	if picker(s).Open() {
		t.Fatal("Escape didn't close the search")
	}
	if next := s.UI().NextFrame(); next.IsZero() || next.After(s.Now) {
		t.Errorf("the frame that closed the search, still showing it, asks for the next at %v, not now", next)
	}
	s.Frame()
	if picker(s).Open() || s.UI().NextFrame().After(s.Now) {
		t.Error("the search is still up a frame later")
	}
}

func TestEventsViewCloses(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Run("omnibox.open", registry.Args{"query": "Events"})
	s.Frame()
	s.Type("CR")
	s.Frame()
	if !s.Showing("events") {
		t.Fatal("running Events from the omnibox didn't open the event stream")
	}
	s.Press("<Esc>")
	if s.Showing("events") {
		t.Errorf("Escape didn't close the event stream (keys went to %q)", s.UI().KeyMode())
	}
}

// Every built-in view opened in the layout closes from the keyboard with ⌃W c.
func TestEveryViewClosesWithCtrlWC(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOpts{Write: map[string]string{"a.txt": "alpha\n"}})
	var views []string
	for _, id := range r.Host.Views() {
		if !strings.HasPrefix(id, "app.") {
			views = append(views, id)
		}
	}
	for _, id := range views {
		t.Run(id, func(t *testing.T) {
			if err := r.Do(func() error { return r.Host.Run(context.Background(), "view.open", registry.Args{"id": id}) }); err != nil {
				t.Skipf("doesn't open on its own: %v", err)
			}
			if len(r.Shown()) == 0 {
				t.Skip("opens nothing in the layout")
			}
			r.TypeKeys("<C-w>c")
			r.WaitFor(id+" closed by ⌃W c", func() bool { return len(r.Host.Layout().Shown()) == 0 })
		})
	}
}

// ⌘W closes the tab in front, a file's in the editor whatever its mode, and leaves the others and the session.
func TestCmdWClosesTheTabInFront(t *testing.T) {
	for _, mode := range []string{"normal", "insert"} {
		t.Run(mode, func(t *testing.T) {
			r := apptest.NewWindow(t, apptest.WindowOpts{Write: map[string]string{"a.txt": "alpha\n", "b.txt": "bravo\n"}, Files: []string{"a.txt", "b.txt"}})
			r.WaitText("bravo")
			if mode == "insert" {
				r.TypeKeys("i")
			}
			r.TypeKeys("<D-w>")
			r.WaitFrame("b.txt closed, a.txt in front", func(s string) bool { return strings.Contains(s, "alpha") && !strings.Contains(s, "b.txt") })
		})
	}
}
