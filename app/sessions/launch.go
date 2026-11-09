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
		if w.Dir() != "" {
			return
		}
		id := h.State().Focus
		se, ok := h.State().Session(id)
		if !ok || se.Snapshot().Archived {
			return
		}
		h.Apply(core.SessionFocused{ID: id})
		if kit.Moves(h, id) {
			kit.Follow(h, id)
		}
	})
}
