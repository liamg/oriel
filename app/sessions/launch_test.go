package sessions_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
)

// Started with nothing open, the app comes back as it was left: the session in front then is in front again, with
// the tabs that were open, in their order. On the way it shows what it's opening, never the start view.
func TestTheWindowComesBackAsItWasLeft(t *testing.T) {
	lw := rig.NewHost(t)
	ids := map[string]core.SessionID{}
	for _, name := range []string{"old", "front", "recent", "closed"} {
		id, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindDirect, Name: name, Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	for _, ev := range []core.Event{core.TabOpened{ID: ids["recent"]}, core.TabOpened{ID: ids["old"]}, core.TabOpened{ID: ids["front"]},
		core.SessionFocused{ID: ids["front"]}} {
		if err := lw.W.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	tabs := lw.W.Store().State().Tabs
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	w.WaitText("front")
	if text := w.Win.Frames()[0].Text(); !strings.Contains(text, "Opening front…") {
		t.Errorf("the first frame isn't the opening view:\n%s", text)
	}
	w.WaitFrame("the tabs as they were", func(s string) bool {
		return strings.Index(s, "recent") < strings.Index(s, "old") && strings.Index(s, "old") < strings.Index(s, "front")
	})
	for i, f := range w.Win.Frames() {
		if text := f.Text(); strings.Contains(text, "Open Directory") {
			t.Fatalf("frame %d showed the start view:\n%s", i, text)
		}
	}
	if st := w.Store.State(); st.Focus != ids["front"] || !slices.Equal(st.Tabs, tabs) {
		t.Errorf("%s in front, tabs %v; want %s, %v as they were", st.Focus, st.Tabs, ids["front"], tabs)
	}
	if strings.Contains(w.Win.Frames()[len(w.Win.Frames())-1].Text(), "closed") {
		t.Error("a session with no tab opened one")
	}
}

// Left on the start view, with tabs open behind it, the app opens on the start view with those tabs.
func TestLeftOnTheStartViewItOpensThere(t *testing.T) {
	lw := rig.NewHost(t)
	id, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindDirect, Name: "behind", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := lw.W.Store().Apply(core.TabOpened{ID: id}); err != nil {
		t.Fatal(err)
	}
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	w.WaitFrame("the start view with the tab", func(s string) bool { return apptest.HasAll(s, "Open Directory", "behind") })
	if f := w.Store.State().Focus; f != "" {
		t.Errorf("%s came to the front", f)
	}
}
