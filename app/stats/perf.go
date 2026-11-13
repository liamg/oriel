package stats

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"runtime/metrics"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
)

// PerfHelp is the Performance page's help.
const PerfHelp = "shell/performance"

const (
	perfEvery  = 2 * time.Second // how often the page samples
	perfFrames = 5 * time.Second // the frames its figures are over
	diskEvery  = 5 * time.Minute // how often it reads the disk
	perfShown  = 40              // the processes it lists
)

// Perf is the Performance page: what Oriel costs (its CPU, memory, goroutines and frames), the machine's load and
// disk, and the processes Oriel started, by session. It samples only while the page is up.
type Perf struct {
	kit.Disposer
	Load  *sysload.Sampler // the machine's load, wanted while the page is up
	Every time.Duration    // how often it samples; perfEvery when 0
	h     plugin.Host
	open  bool
	shown *kit.WhileShown
	work  kit.Work // sampling, while the page is up

	mu   sync.Mutex
	snap perfSnap
}

// perfSnap is one sample.
type perfSnap struct {
	at          time.Time
	procs       []sysload.ProcLoad // Oriel and what it started, heaviest first
	loads       bool               // procs have CPU: there was a sample before
	err         string
	heap, goMem uint64 // the Go heap's live objects, and all the Go runtime holds
	gcs         uint64
	goroutines  int
	frames      kit.FrameStats
	diskFree    uint64
	diskTotal   uint64
	diskAt      time.Time
	diskErr     string
	watches     []watchStat // the freshness managers' sources, app's first
}

// watchStat is a manager's source, as the Watches table lists it.
type watchStat struct {
	manager string // the session's name, or "app"
	watch.SourceStat
}

// Manifest loads it on the first use of one of its commands: a click on the status line's CPU.
func (x *Perf) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "perf", Lazy: kit.LazyOn(x.commands(), nil)}
}

func (x *Perf) commands() []registry.Command {
	return []registry.Command{
		{ID: "perf.open", Title: "Open the Performance page: what Oriel and the processes it started cost, and the machine's load",
			Run: func(registry.Ctx, registry.Args) error { x.open = true; return nil }},
		{ID: "perf.close", Title: "Close the Performance page", Run: func(registry.Ctx, registry.Args) error { x.open = false; return nil }},
	}
}

func (x *Perf) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.shown = &kit.WhileShown{Want: x.want, Focus: h}
	x.Add(h.Framed(x.shown.Framed))
	x.Add(registry.DisposeFunc(x.shown.Drop))
	x.Add(&x.work)
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.View(kit.OverlayPrefix+"perf", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "perf", Fn: x.page} })))
	x.Add(h.Contribute(kit.PagePlace("perf", "Performance", "perf.open", &x.open)))
	return nil
}

func (x *Perf) every() time.Duration { return cmp.Or(x.Every, perfEvery) }

// want samples until the release it answers, called on the loop.
func (x *Perf) want() func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	load := func() {}
	if x.Load != nil {
		load = x.Load.Want()
	}
	var frames *kit.FrameLog
	dir := ""
	if w, ok := kit.WindowOf(x.h); ok {
		frames, dir = w.Frames(), w.Dir()
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	poll := kit.PollsOf(x.h).Start(x.every())
	first := plugin.Begin(x.h, "the Performance page's first sample")
	if !x.work.Loop(func(context.Context) {
		defer close(done)
		defer first()
		x.run(ctx, frames, dir, first)
	}) {
		first()
		close(done)
	}
	return func() {
		cancel()
		<-done
		poll()
		load()
	}
}

// run samples every while until ctx ends, calling sampled after each sample is in.
func (x *Perf) run(ctx context.Context, frames *kit.FrameLog, dir string, sampled func()) {
	tick := time.NewTicker(x.every())
	defer tick.Stop()
	var prev []sysload.Proc
	var prevAt time.Time
	for {
		now := time.Now()
		s := perfSnap{at: now}
		fam, err := sysload.Family(os.Getpid())
		if err != nil {
			s.err = err.Error()
		}
		s.procs, s.loads = sysload.Loads(prev, fam, now.Sub(prevAt)), prev != nil
		prev, prevAt = fam, now
		s.heap, s.goMem, s.gcs, s.goroutines = goRuntime()
		s.watches = x.watchStats()
		if frames != nil {
			s.frames = frames.Stats(now, perfFrames)
		}
		x.mu.Lock()
		last := x.snap
		x.mu.Unlock()
		if last.diskAt.IsZero() || now.Sub(last.diskAt) >= diskEvery {
			s.diskAt = now
			s.diskFree, s.diskTotal, err = sysload.Disk(dir)
			if err != nil {
				s.diskErr = err.Error()
			}
		} else {
			s.diskFree, s.diskTotal, s.diskAt, s.diskErr = last.diskFree, last.diskTotal, last.diskAt, last.diskErr
		}
		x.mu.Lock()
		x.snap = s
		x.mu.Unlock()
		x.h.Redraw()
		sampled()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// goRuntime is the Go heap's live bytes, all the runtime holds, its GC cycles and its goroutines.
func goRuntime() (heap, total, gcs uint64, goroutines int) {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/memory/classes/total:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"}, {Name: "/sched/goroutines:goroutines"}}
	metrics.Read(s)
	u := func(i int) uint64 {
		if s[i].Value.Kind() == metrics.KindUint64 {
			return s[i].Value.Uint64()
		}
		return 0
	}
	return u(0), u(1), u(2), int(u(3))
}

func (x *Perf) sample() perfSnap {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.snap
}

func (x *Perf) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	x.shown.Keep()
	s := x.sample()
	widget.Page(c, "perf", &x.open, widget.PageOpts{Label: "Performance", Help: PerfHelp}, func(c *ui.Ctx) {
		th := c.Theme()
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "Performance", kit.UIFont(16, th.C(theme.Fg)))
			note := "sampling…"
			if !s.at.IsZero() {
				note = "sampled every " + x.every().String() + ", while this page is up"
			}
			c.Text("m", note, kit.MonoFont(11, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: PerfHelp}) {
				x.open = false
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		width := float32(900)
		if r, ok := c.Rect(c.Next("body")); ok {
			width = r.Dx() - 36
		}
		c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
			Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
			x.oriel(c, s)
			x.machine(c, s)
			x.sessions(c, s, width)
			x.processes(c, s, width)
			x.watches(c, s, width)
		})
	})
}

func perfHeading(c *ui.Ctx, key, text string) {
	c.Text(key, text, kit.KickerFont(c.Theme().C(theme.FgFaint)))
}

// share is a CPU share of one core as a percentage, or a dash before there are two samples.
func share(s perfSnap, f float64) string {
	if !s.loads {
		return "—"
	}
	return fmt.Sprintf("%d%%", kit.Pct(f))
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

// oriel is this process: its CPU and memory, the Go runtime's, and the window's frames.
func (x *Perf) oriel(c *ui.Ctx, s perfSnap) {
	perfHeading(c, "oriel.h", "ORIEL")
	var self sysload.ProcLoad
	if i := slices.IndexFunc(s.procs, func(p sysload.ProcLoad) bool { return p.PID == os.Getpid() }); i >= 0 {
		self = s.procs[i]
	}
	f := s.frames
	c.Row("oriel", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		statsTile(c, "cpu", "CPU", share(s, self.Share), "of a core", func(c *ui.Ctx) {
			if cores := x.h.State().Machine.Cores; cores > 0 && s.loads {
				dim(c, "s", fmt.Sprintf("%d%% of the machine", kit.Pct(self.Share/float64(cores))))
			}
		})
		statsTile(c, "mem", "MEMORY", kit.Bytes(int64(self.RSS)), "resident", func(c *ui.Ctx) {
			dim(c, "s", fmt.Sprintf("Go heap %s · runtime %s", kit.Bytes(int64(s.heap)), kit.Bytes(int64(s.goMem))))
		})
		statsTile(c, "go", "GOROUTINES", fmt.Sprint(s.goroutines), "", func(c *ui.Ctx) {
			dim(c, "s", kit.Counted(int(s.gcs), "GC cycle"))
		})
		statsTile(c, "frames", "FRAMES", fmt.Sprintf("%.0f", f.PerSecond), "a second", func(c *ui.Ctx) {
			if f.Frames > 0 {
				dim(c, "s", fmt.Sprintf("build p50 %s · p99 %s", ms(f.P50), ms(f.P99)))
			}
			if f.Wakes > 0 {
				dim(c, "w", fmt.Sprintf("%d asked for by %s", f.Wakes, tail(f.Waker, 60)))
			}
		})
		lag := "—"
		if f.Lag > 0 {
			lag = ms(f.Lag)
		}
		statsTile(c, "lag", "POINTER LAG", lag, "", func(c *ui.Ctx) {
			if f.LagWorst > 0 {
				dim(c, "s", "worst "+ms(f.LagWorst))
			}
		})
	})
}

// machine is the machine's load, as the status line's CPU reads it, and its disk.
func (x *Perf) machine(c *ui.Ctx, s perfSnap) {
	perfHeading(c, "machine.h", "THIS MACHINE")
	m := x.h.State().Machine
	c.Row("machine", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		cpu := "—"
		if !m.Sampled.IsZero() {
			cpu = fmt.Sprintf("%d%%", kit.Pct(m.CPU))
		}
		statsTile(c, "cpu", "CPU", cpu, "all cores", func(c *ui.Ctx) {
			if m.Cores > 0 {
				dim(c, "s", kit.Counted(m.Cores, "core"))
			}
		})
		statsTile(c, "mem", "MEMORY", kit.Bytes(int64(m.Mem)), "of "+kit.Bytes(int64(m.MemTotal)), nil)
		disk := "—"
		if s.diskTotal > 0 {
			disk = kit.Bytes(int64(s.diskFree))
		}
		statsTile(c, "disk", "DISK FREE", disk, "of "+kit.Bytes(int64(s.diskTotal)), func(c *ui.Ctx) {
			switch {
			case s.diskErr != "":
				dim(c, "s", s.diskErr)
			case !s.diskAt.IsZero():
				dim(c, "s", fmt.Sprintf("read every %dm", int(diskEvery.Minutes())))
			}
		})
	})
}

// sessions are the sessions with processes running here, and what those cost.
func (x *Perf) sessions(c *ui.Ctx, s perfSnap, width float32) {
	perfHeading(c, "sessions.h", "SESSIONS")
	type sum struct {
		n     int
		share float64
		rss   uint64
	}
	by := map[string]*sum{}
	for _, p := range s.procs {
		if p.Session == "" {
			continue
		}
		if by[p.Session] == nil {
			by[p.Session] = &sum{}
		}
		b := by[p.Session]
		b.n, b.share, b.rss = b.n+1, b.share+p.Share, b.rss+p.RSS
	}
	th := c.Theme()
	if len(by) == 0 {
		c.Text("none", "No session has a process running here.", kit.UIFont(12, th.C(theme.FgFaint)))
		return
	}
	ids := make([]string, 0, len(by))
	for id := range by {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int { return cmp.Or(cmp.Compare(by[b].share, by[a].share), cmp.Compare(a, b)) })
	rows := [][][]widget.Span{cells("Session", "Status", "Processes", "CPU", "Memory")}
	for _, id := range ids {
		name, status := id, ""
		if se, ok := sessionNamed(x.h.State(), core.SessionID(id)); ok {
			name, status = cmp.Or(se.Name, id), kit.StatusName(se.Status)
		}
		b := by[id]
		rows = append(rows, cells(name, status, fmt.Sprint(b.n), share(s, b.share), kit.Bytes(int64(b.rss))))
	}
	widget.Table(c, "sessions", rows, width, kit.MonoFont(11, th.C(theme.Fg)), widget.TableOpts{Head: true, Label: "Sessions",
		Align: []ui.Align{ui.Start, ui.Start, ui.End, ui.End, ui.End, ui.Start}})
}

// tail is s cut to its last n runes, behind "…".
func tail(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n+1:])
	}
	return s
}

func sessionNamed(st core.State, id core.SessionID) (core.SessionState, bool) {
	i := slices.IndexFunc(st.Sessions, func(se core.SessionState) bool { return se.ID == id })
	if i < 0 {
		return core.SessionState{}, false
	}
	return st.Sessions[i], true
}

// processes are Oriel and the processes under it, heaviest first.
func (x *Perf) processes(c *ui.Ctx, s perfSnap, width float32) {
	th := c.Theme()
	perfHeading(c, "procs.h", fmt.Sprintf("PROCESSES · %d", len(s.procs)))
	if s.err != "" {
		c.Text("err", "Can't read the processes: "+s.err, kit.UIFont(12, th.C(theme.Warn)))
		return
	}
	rows := [][][]widget.Span{cells("PID", "Command", "Session", "CPU", "Memory")}
	for _, p := range s.procs[:min(len(s.procs), perfShown)] {
		cmd := p.Command
		if p.PID == os.Getpid() {
			cmd = "oriel (this window)"
		}
		if r := []rune(cmd); len(r) > 90 {
			cmd = string(r[:89]) + "…"
		}
		session := p.Session
		if se, ok := sessionNamed(x.h.State(), core.SessionID(p.Session)); ok {
			session = cmp.Or(se.Name, session)
		}
		rows = append(rows, cells(fmt.Sprint(p.PID), cmd, session, share(s, p.Share), kit.Bytes(int64(p.RSS))))
	}
	widget.Table(c, "procs", rows, width, kit.MonoFont(11, th.C(theme.Fg)), widget.TableOpts{Head: true, Label: "Processes",
		Align: []ui.Align{ui.End, ui.Start, ui.Start, ui.End, ui.End}})
	if more := len(s.procs) - perfShown; more > 0 {
		dim(c, "more", fmt.Sprintf("and %d more", more))
	}
}

func cells(texts ...string) [][]widget.Span {
	out := make([][]widget.Span, len(texts))
	for i, t := range texts {
		out[i] = []widget.Span{{Text: strings.TrimSpace(t)}}
	}
	return out
}

// watchStats are the sources every freshness manager keeps: the app's, then each open session's.
func (x *Perf) watchStats() []watchStat {
	w, ok := registry.WatchesOf(x.h)
	if !ok {
		return nil
	}
	var out []watchStat
	add := func(name string, id core.SessionID) {
		if m := w.For(id); m != nil {
			for _, st := range m.Stats() {
				out = append(out, watchStat{manager: name, SourceStat: st})
			}
		}
	}
	add("app", "")
	for _, se := range x.h.State().Sessions {
		if !se.Archived {
			add(cmp.Or(se.Name, string(se.ID)), se.ID)
		}
	}
	return out
}

// watches are what the freshness managers keep fresh, who for, and how often they read it.
func (x *Perf) watches(c *ui.Ctx, s perfSnap, width float32) {
	th := c.Theme()
	perfHeading(c, "watch.h", fmt.Sprintf("WATCHES · %d", len(s.watches)))
	if len(s.watches) == 0 {
		c.Text("none", "Nothing is being kept fresh.", kit.UIFont(12, th.C(theme.FgFaint)))
		return
	}
	rows := [][][]widget.Span{cells("Manager", "Source", "State", "Subscribers", "Reads/min", "Last read", "Why")}
	for _, w := range s.watches {
		var who []string
		for _, sub := range w.Subs {
			if !sub.Held {
				who = append(who, sub.Label)
			}
		}
		subs := strings.Join(who, ", ")
		if held := len(w.Subs) - len(who); held > 0 {
			subs = strings.TrimPrefix(subs+fmt.Sprintf(", read by %d", held), ", ")
		}
		last := "—"
		if !w.LastRead.IsZero() {
			last = widget.AgoLong(time.Now(), w.LastRead)
		}
		if w.Err != nil {
			last = "failed: " + w.Err.Error()
		}
		rows = append(rows, cells(w.manager, tail(w.Key, 70), w.State, subs, fmt.Sprint(w.PerMin), last, tail(cmp.Or(w.Cause, "—"), 60)))
	}
	widget.Table(c, "watches", rows, width, kit.MonoFont(11, th.C(theme.Fg)), widget.TableOpts{Head: true, Label: "Watches",
		Align: []ui.Align{ui.Start, ui.Start, ui.Start, ui.Start, ui.End, ui.End}})
}
