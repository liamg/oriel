package sessions

import (
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
)

// landAtLaunch brings back the window as it was left, when it starts with nothing open: the session in front then
// is in front again, before the first frame, so the start view never shows on the way. The tabs are the host's,
// restored with it. Left on the start view, it stays there.
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
		id := st.Focus
		se, ok := st.Session(id)
		switch {
		case id == "":
			log.Info("launch: left on the start view: no session in front", "tabs", len(st.Tabs))
			return
		case !ok:
			log.Info("launch: the session in front is gone", "session", id)
			return
		case se.Snapshot().Archived:
			log.Info("launch: the session in front is archived", "session", id)
			return
		}
		h.Apply(core.SessionFocused{ID: id})
		if !kit.Moves(h, id) {
			log.Info("launch: the session in front has no directory to open", "session", id, "dir", kit.SessionDir(se.Snapshot()))
			return
		}
		log.Info("launch: back on the session in front", "session", id, "dir", kit.SessionDir(se.Snapshot()))
		kit.Follow(h, id)
	})
}
