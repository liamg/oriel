package shell_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/render"
)

// A record written while the log panel shows is listed in it.
func TestLogPanelShowsNewRecords(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("Log", nil)
	s.Frame()
	s.Frame()
	s.Logs.Logger("core").Info("written while it shows")
	s.Frame()
	s.WantFrame("written while it shows")
}

// Records written with the log panel closed draw no frame; with it open, they show.
func TestLogRecordsDrawOnlyWhileThePanelShows(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir()})
	r.WaitText("ORIEL")
	log := r.Host.Log()
	time.Sleep(time.Second) // what opening started settles
	before := len(r.Win.Frames())
	for i := range 5 {
		log.Info("written while the panel is closed", "i", i)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(r.Win.Frames()) - before; n > 1 {
		t.Errorf("5 records drew %d frames with the log panel closed", n)
	}
	r.Run("Log", nil)
	r.WaitText("written while the panel is closed")
	log.Info("written while it shows")
	r.WaitText("written while it shows")
}

// longErr is a warning's err too long for its row: its end only shows once the row's expanded.
const longErr = "claude: /usage typed but no plan windows came: context deadline exceeded; " +
	"$.session.usage() failed: Invalid time value; the screen ends: What's contributing to your limits usage? the very end"

// An expanded record wraps its message and attributes to the panel's width, so all of them show.
func TestLogRowExpandedWraps(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("Log", nil)
	s.Logs.Logger("core").Warn("usage", "err", longErr)
	s.Frame()
	s.Frame()
	if strings.Contains(s.Text(), "the very end") {
		t.Fatal("the folded row already shows the end of its err")
	}
	s.ClickOn("usage")
	s.WantFrame("the very end")
}

// Copy on a record's context menu copies the record.
func TestLogRowCopiesFromItsMenu(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Run("Log", nil)
	s.Logs.Logger("core").Warn("usage", "err", longErr)
	s.Frame()
	s.Frame()
	if menu := s.RightClick(s.TextPoint("usage")); !strings.Contains(menu, "Copy") {
		t.Fatalf("the row's menu:\n%s", menu)
	}
	item := menuItem(s.Access(), "Copy")
	if item == nil {
		t.Fatal("no Copy in the menu")
	}
	s.Click(apptest.Centre(item.Bounds), 0)
	s.Frame()
	s.RunQueued()
	if got, _ := s.Desktop.ReadClipboard(); !strings.Contains(got, "WARN") || !strings.Contains(got, "err="+longErr) {
		t.Errorf("copied %q", got)
	}
}

// menuItem is the menu item named label under n.
func menuItem(n *render.AccessNode, label string) *render.AccessNode {
	if n == nil {
		return nil
	}
	if n.Role == render.RoleMenuItem && n.Name == label {
		return n
	}
	for _, k := range n.Children {
		if f := menuItem(k, label); f != nil {
			return f
		}
	}
	return nil
}
