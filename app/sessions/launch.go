package sessions

import (
	"context"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// frontOf is the session to have in front: the one in front, while it's open, else the first open tab's; "" with
// no tabs open.
func frontOf(st core.State) core.SessionID {
	open := func(id core.SessionID) bool {
		se, ok := st.Session(id)
		return ok && !se.Snapshot().Archived
	}
	if st.Focus != "" && open(st.Focus) {
		return st.Focus
	}
	for _, id := range st.Tabs {
		if open(id) {
			return id
		}
	}
	return ""
}

// landAtLaunch brings back the window as it was left, when it starts with nothing open: the session in front then
// is in front again, else the first tab's, before the first frame, so the start view never shows on the way. The
// tabs are the host's, restored with it. Only with no tabs open does it stay on the start view.
func landAtLaunch(h plugin.Host) {
	w, ok := kit.WindowOf(h)
	if !ok || !w.LaunchedEmpty() {
		return
	}
	h.Do(func() {
		log := h.Log()
		if d := w.Dir(); d != "" {
			log.Info("launch: on a directory, not the session in front", "dir", d)
			return
		}
		st := h.State()
		id := frontOf(st)
		if id == "" {
			log.Info("launch: on the start view: no tabs open")
			return
		}
		se, _ := st.Session(id)
		h.Apply(core.SessionFocused{ID: id})
		if !kit.WindowMovesFor(h, id) {
			log.Info("launch: the session in front has no directory to open", "session", id, "dir", kit.SessionDir(se.Snapshot()))
			return
		}
		log.Info("launch: back on the session in front", "session", id, "was in front", id == st.Focus, "dir", kit.SessionDir(se.Snapshot()))
		kit.Follow(h, id)
	})
}

// keepATabInFront brings the first open tab to the front whenever tabs are open and none is: the start view is
// for when there are none.
func keepATabInFront(h plugin.Host) (cancel func()) {
	return h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev := ev.(type) {
		case core.TabClosed, core.TabsRestored, core.SessionClosed, core.SessionArchived:
		case core.SessionFocused:
			if ev.ID != "" {
				return
			}
		default:
			return
		}
		h.Do(func() {
			st := h.State()
			if st.Focus != "" {
				return
			}
			if id := frontOf(st); id != "" {
				if err := h.Run(context.Background(), "session.focus", registry.Args{"id": string(id)}); err != nil {
					h.Log().Warn("bringing a tab to the front", "session", id, "err", err)
				}
			}
		})
	})
}
