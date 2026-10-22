package shell_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
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
