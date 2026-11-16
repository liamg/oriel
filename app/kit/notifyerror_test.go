package kit_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
)

// An error notice clears itself only when it answers what the user just did; a hook's stop and no error post none.
func TestNotifyError(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	h := s.HostAs("builtin:test")
	kit.NotifyError(h, "test.click", errors.New("refused"), true)
	kit.NotifyError(h, "test.background", errors.New("offline"), false)
	kit.NotifyError(h, "test.stopped", fmt.Errorf("commit: %w", kit.ErrStopped), true)
	kit.NotifyError(h, "test.none", nil, true)
	got := map[string]core.Notification{}
	for _, n := range s.Store().State().Notifications {
		got[n.ID] = n
	}
	if n, ok := got["test.click"]; !ok || !n.AutoClear || n.Severity != core.SevError || n.Text != "refused" || n.Source != "builtin:test" {
		t.Errorf("the click's notice: %+v", n)
	}
	if n, ok := got["test.background"]; !ok || n.AutoClear {
		t.Errorf("the background failure's notice: %+v", n)
	}
	if _, ok := got["test.stopped"]; ok {
		t.Error("a hook's stop was posted")
	}
	if _, ok := got["test.none"]; ok {
		t.Error("no error was posted")
	}
}

// A failure's notice keeps what it says of itself (its title and actions) and takes err's text as an error.
func TestNotifyFailure(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	h := s.HostAs("builtin:test")
	kit.NotifyFailure(h, core.Notice{ID: "test.ship", Title: "api", Severity: core.SevInfo,
		Actions: []core.Action{{Title: "Retry", Command: "ship.push"}}}, errors.New("push refused"))
	kit.NotifyFailure(h, core.Notice{ID: "test.stopped"}, kit.ErrStopped)
	var n core.Notification
	for _, x := range s.Store().State().Notifications {
		if x.ID == "test.ship" {
			n = x
		}
		if x.ID == "test.stopped" {
			t.Error("a hook's stop was posted")
		}
	}
	if n.Severity != core.SevError || n.Text != "push refused" || n.Title != "api" || len(n.Actions) != 1 {
		t.Errorf("the failure's notice: %+v", n)
	}
}
