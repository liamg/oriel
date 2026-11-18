package lang

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/pathx"
)

// liveSessionDirs are the directories of each session that hasn't ended: its workspace and its projects.
func liveSessionDirs(st core.State) map[core.SessionID][]string {
	out := map[core.SessionID][]string{}
	for _, se := range st.Sessions {
		if se.Archived {
			continue
		}
		var dirs []string
		if se.Dir != "" {
			dirs = append(dirs, se.Dir)
		}
		for _, p := range se.Projects {
			if p.Dir != "" && (se.Dir == "" || !pathx.Inside(p.Dir, se.Dir)) {
				dirs = append(dirs, p.Dir)
			}
		}
		out[se.ID] = dirs
	}
	return out
}

// sessionsChanged stops the servers of the sessions that have ended since it last looked: those in their
// directories, unless the window is still on one or a session still going shares it.
func (x *LSP) sessionsChanged(st core.State) {
	live := liveSessionDirs(st)
	x.mu.Lock()
	was := x.sessionDirs
	x.sessionDirs = live
	x.mu.Unlock()
	var ended []string
	for id, dirs := range was {
		if _, ok := live[id]; ok {
			continue
		}
		for _, dir := range dirs {
			if !used(dir, x.dir(), live) {
				ended = append(ended, dir)
			}
		}
	}
	if len(ended) == 0 {
		return
	}
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		for _, dir := range ended {
			x.forgetDocsUnder(dir)
			x.m.StopUnder(ctx, dir)
		}
		x.h.Redraw()
	})
}

// used reports whether dir is still wanted: the window is in it, or a session still going works in it.
func used(dir, window string, live map[core.SessionID][]string) bool {
	if window != "" && (pathx.Inside(window, dir) || pathx.Inside(dir, window)) {
		return true
	}
	for _, dirs := range live {
		for _, d := range dirs {
			if pathx.Inside(d, dir) || pathx.Inside(dir, d) {
				return true
			}
		}
	}
	return false
}

// forgetDocsUnder drops the documents under dir, their servers being stopped: one of their buffers edited again
// opens on a server afresh.
func (x *LSP) forgetDocsUnder(dir string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for path, d := range x.docs {
		if pathx.Inside(path, dir) {
			if d.cancel != nil {
				d.cancel()
			}
			delete(x.docs, path)
		}
	}
}
