package shell

import (
	"image"
	"io/fs"
	"slices"
	"time"

	"github.com/liamg/oriel/render"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// window is the window's own services: the directory it's on, Lua plugins from their files, the plugins it started
// with, and quitting.
type window struct{ sh *shell }

// contribute contributes them, and answers how to take them back.
func (w window) contribute() func() {
	var d []registry.Disposable
	for _, name := range []string{kit.ScriptsService, kit.BuiltinsService, kit.QuitService, kit.IconsService, kit.WindowService} {
		d = append(d, w.sh.cfg.Registry.Contribute(registry.Service{Name: name, Impl: w, Source: "builtin:window"}))
	}
	d = append(d, w.sh.cfg.Registry.Contribute(registry.Service{Name: kit.ReadsService, Impl: w.sh.reads, Source: "builtin:window"}))
	// A read from disk the last frame asked for, or one landed it hasn't shown.
	d = append(d, w.sh.cfg.Registry.Contribute(registry.Busy{Name: "the window's reads", Busy: w.sh.reads.Busy, Source: "builtin:window"}))
	// A window moving is laid out once it arrives (Window.Hold), which replaces what's open meanwhile.
	d = append(d, w.sh.cfg.Registry.Contribute(registry.Busy{Name: "window arriving", Busy: func() bool { return !w.Arrived() },
		Source: "builtin:window"}))
	// What's been handed the loop (Host.Do) and not run yet: looked at from the loop, what the work running hands it.
	d = append(d, w.sh.cfg.Registry.Contribute(registry.Busy{Name: "the loop's queue", Busy: w.sh.queued, Source: "builtin:window"}))
	d = append(d, w.sh.cfg.Registry.Contribute(registry.Busy{Name: "work off the loop", Busy: w.sh.inflight.busy, What: w.sh.inflight.what,
		Source: "builtin:window"}))
	return func() {
		for _, x := range d {
			x.Dispose()
		}
	}
}

func (w window) IconImage(name string, col render.Color) image.Image {
	return contributedIcon(w.sh.cfg.Registry, w.sh.reads.Pictures, name, col)
}

func (w window) Dir() string { return w.sh.workDir() }

func (w window) Hold() (release func()) { return w.sh.hold() }

func (w window) Polls() *kit.PollSet { return w.sh.polls }

func (w window) Frames() *kit.FrameLog { return &w.sh.frames }

func (w window) FSWatch() (*fswatch.Watcher, error) {
	if w.sh.cfg.FSWatch != nil {
		return w.sh.cfg.FSWatch()
	}
	return fswatch.New()
}

func (w window) Reopen(dir string, session core.SessionID) error {
	return w.sh.reopenOn(reopenTo{dir: dir, session: session})
}

func (w window) AfterReopen(session core.SessionID, calls []kit.Call) {
	w.sh.afterOpen = &afterOpen{session: session, calls: calls}
}

func (w window) Arrived() bool {
	a := &w.sh.holds
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.done
}

func (w window) LaunchedEmpty() bool {
	if w.sh.launchDone {
		return false
	}
	w.sh.launchDone = true
	return w.sh.cfg.WorkDir == "" && len(w.sh.cfg.Files) == 0
}

func (window) Script(fsys fs.FS, o kit.ScriptOpts) plugin.Plugin { return ScriptPluginWith(fsys, o) }

func (w window) Builtins() []kit.Builtin {
	if w.sh.active == nil {
		return nil
	}
	var out []kit.Builtin
	for _, p := range *w.sh.active {
		if _, ok := p.(*scriptPlugin); !ok {
			out = append(out, kit.Builtin{Manifest: p.Manifest(), Native: slices.Contains(plugin.Native(), unwrapped(p)), Waits: waits(p)})
		}
	}
	return out
}

func (w window) Restart(relaunch func() error) {
	w.sh.relaunch.Store(&relaunch)
	w.sh.quit(true)
}

func (w window) Quit() { w.sh.quit(true) }

func (w window) Draining() (kit.Drain, bool) {
	d := w.sh.draining.Load()
	return d, d != nil
}
func (r *quitDrain) Hide(hidden bool)    { r.hidden = hidden }
func (r *quitDrain) Handing() bool       { return r.handing.Load() }
func (r *quitDrain) Deadline() time.Time { return r.deadline }
func (r *quitDrain) Now()                { r.goNow() }
func (r *quitDrain) Cancel()             { r.cancelQuit() }

func (r *quitDrain) Busy() ([]core.SessionState, bool) {
	busy, _, polled := r.shown()
	return busy, polled
}

// workDir is the directory the window is on, from any goroutine.
func (sh *shell) workDir() string {
	if d := sh.dir.Load(); d != nil {
		return *d
	}
	return ""
}

// dirState is a state file kept for the directory the window is on now (Config.DirState), from any goroutine.
func (sh *shell) dirState(name string) string {
	return dirStateFor(sh.cfg.StateDir, sh.workDir(), name)
}
