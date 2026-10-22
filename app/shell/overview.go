package shell

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tOverviewCard  = theme.Declare("overview.card", "bg.surface")
	tOverviewHeat  = theme.Declare("overview.heat", "accent")
	tOverviewEmpty = theme.Declare("overview.heat.empty", "bg.overlay")
	tOverviewBar   = theme.Declare("overview.bar", "accent")
	tOverviewOther = theme.Declare("overview.language.other", "fg.ghost")
	tOverviewLangs = []theme.TokenID{
		theme.Declare("overview.language.1", "cyan"),
		theme.Declare("overview.language.2", "violet"),
		theme.Declare("overview.language.3", "green"),
		theme.Declare("overview.language.4", "amber"),
	}
)

const (
	panelBudget  = 5 * time.Second // a panel still computing after this shows its error
	countBudget  = 3 * time.Second // lines are counted for this long, then estimated from the bytes left
	maxScanFiles = 200_000
	maxLineFile  = 4 << 20 // bigger files' lines are estimated from their size
)

// overview is the project overview: a card per repo or directory open, its panels computed off the loop and kept
// until the repo's HEAD moves.
type overview struct {
	kit.Disposer
	dir   string // the work dir
	h     plugin.Host
	mu    sync.Mutex
	cards map[string]*card
	scans map[string]*scan
	now   func() time.Time
}

// card is one directory's results, for the HEAD they were computed at.
type card struct {
	head   string
	git    bool
	header *cardHeader
	panels map[string]*panelResult
	since  time.Time // when its header was asked for
}

type cardHeader struct {
	branch string
	track  gitx.Tracking
}

type panelResult struct {
	node  ui.Node
	err   error
	done  bool
	since time.Time
}

func (r *panelResult) state() widget.LoadState {
	switch {
	case !r.done:
		return widget.LoadState{Phase: widget.Pending, Since: r.since}
	case r.err != nil:
		return widget.LoadState{Phase: widget.Failed, Err: errors.New(kit.FirstLine(r.err.Error()))}
	}
	return widget.LoadState{Phase: widget.Ready}
}

func (o *overview) Manifest() plugin.Manifest { return plugin.Manifest{ID: "overview"} }

func (o *overview) Activate(_ context.Context, h plugin.Host) error {
	o.h, o.cards, o.scans = h, map[string]*card{}, map[string]*scan{}
	if o.now == nil {
		o.now = time.Now
	}
	for _, p := range []uireg.OverviewPanel{
		{Name: "languages", Title: "Languages", Order: 10, Width: 330, Repos: true, Dirs: true, Compute: o.languages},
		{Name: "activity", Title: "Commit activity", Order: 20, Repos: true, Compute: o.activity},
		{Name: "contributors", Title: "Contributors", Order: 30, Width: 300, Repos: true, Compute: o.contributors},
		{Name: "size", Title: "Size", Order: 20, Dirs: true, Compute: o.size},
		{Name: "largest", Title: "Largest files", Order: 30, Width: 300, Dirs: true, Compute: o.largest},
	} {
		o.Add(h.Contribute(p))
	}
	o.Add(h.View(OverviewView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "overview", Fn: o.build}
	})))
	o.Add(h.View(OverviewPanelsView, uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "panels", Fn: func(c *ui.Ctx) {
			if vc.Instance != "" {
				o.panels(c, vc.Instance, o.card(vc.Instance))
			}
		}}
	})))
	return nil
}

// roots are the directories the overview has a card for: the one open, until sessions bring their repos.
func (o *overview) roots() []string {
	if o.dir == "" {
		return nil
	}
	return []string{o.dir}
}

// headOf is what dir's HEAD points at now, read through rd, and whether it's a repo: a change means the cards'
// results are stale. ok is false while it's being read.
func headOf(rd *kit.Reads, dir string) (head string, git, ok bool) {
	gitDir := filepath.Join(dir, ".git")
	fi, st := rd.Stat(gitDir)
	if st.Loading() {
		return "", false, false
	}
	if fi == nil {
		return "", false, true
	}
	b, st := rd.File(filepath.Join(gitDir, "HEAD"))
	if st.Loading() {
		return "", true, false
	}
	head = strings.TrimSpace(string(b.Data))
	if ref, isRef := strings.CutPrefix(head, "ref: "); isRef {
		r, st := rd.File(filepath.Join(gitDir, ref))
		if st.Loading() {
			return "", true, false
		}
		if r.Data != nil {
			return head + " " + strings.TrimSpace(string(r.Data)), true, true
		}
		p, st := rd.Stat(filepath.Join(gitDir, "packed-refs"))
		if st.Loading() {
			return "", true, false
		}
		if p != nil {
			return head + " " + p.ModTime().String(), true, true
		}
	}
	return head, true, true
}

// card is dir's card, started computing if it's new or its HEAD has moved. Until its HEAD is read it's an empty
// card that isn't kept.
func (o *overview) card(dir string) *card {
	o.mu.Lock()
	defer o.mu.Unlock()
	cd := o.cards[dir]
	rd, ok := kit.ReadsOf(o.h)
	if !ok {
		return &card{panels: map[string]*panelResult{}, since: kit.WindowNow(o.h)}
	}
	head, git, ok := headOf(rd, dir)
	switch {
	case !ok && cd != nil:
		return cd
	case !ok:
		return &card{panels: map[string]*panelResult{}, since: kit.WindowNow(o.h)}
	case cd != nil && cd.head == head && cd.git == git:
		o.computeNew(dir, cd) // a panel registered since
		return cd
	}
	cd = &card{head: head, git: git, panels: map[string]*panelResult{}, since: kit.WindowNow(o.h)}
	o.cards[dir] = cd
	delete(o.scans, dir)
	o.computeNew(dir, cd)
	if cd.git {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), panelBudget)
			defer cancel()
			r := gitx.Repo{Dir: dir}
			hd := &cardHeader{}
			hd.branch, _ = r.Branch(ctx)
			hd.track, _ = r.AheadBehind(ctx)
			o.h.Do(func() { cd.header = hd })
		}()
	}
	return cd
}

// computeNew starts the panels that apply to dir's card and haven't started yet.
func (o *overview) computeNew(dir string, cd *card) {
	for _, p := range uireg.OverviewPanels(o.h) {
		if cd.panels[p.Name] == nil && ((cd.git && p.Repos) || (!cd.git && p.Dirs)) {
			o.compute(dir, cd, p)
		}
	}
}

func (o *overview) compute(dir string, cd *card, p uireg.OverviewPanel) {
	res := &panelResult{since: kit.WindowNow(o.h)}
	cd.panels[p.Name] = res
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), panelBudget)
		defer cancel()
		var n ui.Node
		err := registry.Call(func() (err error) { n, err = p.Compute(ctx, dir); return err })
		o.h.Do(func() { res.node, res.err, res.done = n, err, true })
	}()
}

func (o *overview) build(c *ui.Ctx) {
	th := c.Theme()
	c.Column("overview", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(34, 26), Gap: 22, ScrollY: true, Help: "shell/overview",
		Role: ui.RoleRegion, Label: "Overview"}, func(c *ui.Ctx) {
		for _, dir := range o.roots() {
			o.cardView(c, dir)
		}
		c.Row("hint", ui.Style{W: ui.Flex(1), Main: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "open a file with ␣␣ · search with "+cmp.Or(kit.BoundKey(o.h, "search.open", nil), kit.BoundKey(o.h, "picker.open", registry.Args{"source": "grep"}), "␣/")+
				" · everything with ⌘K", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgGhost)})
		})
	})
}

func (o *overview) cardView(c *ui.Ctx, dir string) {
	th := c.Theme()
	cd := o.card(dir)
	c.Column(dir, ui.Style{W: ui.Flex(1), Fill: th.C(tOverviewCard), Stroke: th.C(theme.Line), Border: 1, Radius: 12, Clip: true,
		Role: ui.RoleRegion, Label: filepath.Base(dir)}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 14), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("name", filepath.Base(dir), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 18, Weight: typeset.Bold, Tracking: .06},
				Color: th.C(theme.Fg)})
			c.Text("path", kit.TildePath(dir), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgFaint)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			o.branch(c, cd)
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		o.panels(c, dir, cd)
	})
}

// panels are dir's card's panels, side by side.
func (o *overview) panels(c *ui.Ctx, dir string, cd *card) {
	th := c.Theme()
	c.Row("panels", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		ps := slices.DeleteFunc(uireg.OverviewPanels(o.h), func(p uireg.OverviewPanel) bool {
			return cd.panels[p.Name] == nil
		})
		for i, p := range ps {
			if i > 0 {
				c.Box(fmt.Sprint("sep", i), ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			}
			w := ui.Flex(1)
			if p.Width > 0 {
				w = ui.Px(p.Width)
			}
			c.Column(p.Name, ui.Style{W: w, Pad: ui.PadXY(18, 14), Gap: 10, Clip: true, Role: ui.RoleRegion, Label: p.Title}, func(c *ui.Ctx) {
				kit.Caption(c, "title", p.Title)
				res := cd.panels[p.Name]
				widget.Async(c, "load", res.state(), widget.AsyncOpts{Help: "shell/overview", Loading: func(c *ui.Ctx) {
					widget.Busy(c, "busy", res.state(), widget.BusyOpts{Label: "Counting", Size: 10, Help: "shell/overview"})
				}}, func(c *ui.Ctx) {
					if res.node != nil {
						c.Node(res.node)
					}
				})
			})
		}
	})
}

func (o *overview) branch(c *ui.Ctx, cd *card) {
	th := c.Theme()
	hd := cd.header
	if hd == nil && cd.git {
		widget.Async(c, "branch-load", widget.LoadState{Phase: widget.Pending, Since: cd.since}, widget.AsyncOpts{Help: "shell/overview",
			Loading: func(c *ui.Ctx) {
				widget.Skeleton(c, "branch", widget.SkeletonOpts{Block: render.Point{X: 160, Y: 14}, Label: "Branch", Help: "shell/overview"})
			}}, func(*ui.Ctx) {})
		return
	}
	if hd == nil || hd.branch == "" {
		return
	}
	mono := func(size float32, col theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(col)}
	}
	c.Row("branch", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.StatusWorking)}, nil)
		c.Text("name", hd.branch, mono(11.5, theme.FgMuted))
		if hd.track.Upstream == "" {
			return
		}
		if hd.track.Ahead > 0 {
			c.Text("ahead", fmt.Sprintf("↑%d", hd.track.Ahead), mono(10.5, theme.OK))
		}
		if hd.track.Behind > 0 {
			c.Text("behind", fmt.Sprintf("↓%d", hd.track.Behind), mono(10.5, theme.Warn))
		}
		c.Text("vs", "vs "+hd.track.Upstream, mono(11.5, theme.FgFaint))
	})
}

// scan is a directory's files, each with its language and lines, shared by the panels that need them.
type scan struct {
	once   sync.Once
	files  []scannedFile
	approx bool // some lines were estimated from bytes, past countBudget
	err    error
}

type scannedFile struct {
	path  string
	lang  string
	bytes int64
	lines int
}

func (o *overview) scanOf(ctx context.Context, dir string) *scan {
	o.mu.Lock()
	s := o.scans[dir]
	if s == nil {
		s = &scan{}
		o.scans[dir] = s
	}
	o.mu.Unlock()
	s.once.Do(func() { s.files, s.approx, s.err = scanDir(ctx, dir, langIndex(o.h)) })
	return s
}

// langIndex maps file extensions and names to the registered languages' names.
func langIndex(h plugin.Host) func(name string) string {
	langs, _ := h.Languages()
	byExt, byName := map[string]string{}, map[string]string{}
	for _, l := range langs {
		for _, e := range l.Extensions {
			byExt[strings.ToLower(e)] = l.Name
		}
		for _, n := range l.Filenames {
			byName[n] = l.Name
		}
	}
	return func(name string) string {
		if l, ok := byName[filepath.Base(name)]; ok {
			return l
		}
		return byExt[strings.ToLower(filepath.Ext(name))]
	}
}

// scanDir lists dir's files (as git does, for a repo) and counts their lines, in parallel, for countBudget; files
// left then, or too big, are estimated from their bytes.
func scanDir(ctx context.Context, dir string, langOf func(string) string) ([]scannedFile, bool, error) {
	var names []string
	if gitx.IsRepo(dir) {
		fs, err := gitx.Repo{Dir: dir}.Files(ctx)
		if err != nil {
			return nil, false, err
		}
		names = fs
	} else {
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != dir && kit.SkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if rel, err := filepath.Rel(dir, p); err == nil {
				names = append(names, filepath.ToSlash(rel))
			}
			if len(names) >= maxScanFiles {
				return filepath.SkipAll
			}
			return nil
		})
	}
	names = names[:min(len(names), maxScanFiles)]
	files := make([]scannedFile, len(names))
	deadline := time.Now().Add(countBudget)
	var wg sync.WaitGroup
	next := make(chan int)
	var mu sync.Mutex
	approx := false
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				f := &files[i]
				st, err := os.Lstat(filepath.Join(dir, names[i]))
				if err != nil || !st.Mode().IsRegular() {
					continue // stays zero, and isn't counted
				}
				f.path, f.bytes, f.lang = names[i], st.Size(), langOf(names[i])
				if f.bytes > maxLineFile || time.Now().After(deadline) || ctx.Err() != nil {
					f.lines = -1
					mu.Lock()
					approx = true
					mu.Unlock()
					continue
				}
				f.lines = countLines(filepath.Join(dir, f.path))
			}
		}()
	}
	for i := range names {
		next <- i
	}
	close(next)
	wg.Wait()
	estimate(files)
	return files, approx, nil
}

// countLines is a text file's lines; 0 for a binary one (a NUL in its first 8 KiB).
func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 32<<10)
	head, _ := r.Peek(8 << 10)
	if bytes.IndexByte(head, 0) >= 0 {
		return 0
	}
	n, last := 0, byte('\n')
	buf := make([]byte, 32<<10)
	for {
		k, err := r.Read(buf)
		n += bytes.Count(buf[:k], []byte{'\n'})
		if k > 0 {
			last = buf[k-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}
	if last != '\n' {
		n++
	}
	return n
}

// estimate fills in the lines of files marked -1 from the bytes per line of those counted.
func estimate(files []scannedFile) {
	var lines, size int64
	for _, f := range files {
		if f.lines > 0 {
			lines, size = lines+int64(f.lines), size+f.bytes
		}
	}
	per := 40.0
	if lines > 0 {
		per = float64(size) / float64(lines)
	}
	for i := range files {
		if files[i].lines < 0 {
			files[i].lines = int(float64(files[i].bytes) / per)
		}
	}
}

type langShare struct {
	name  string
	lines int
}

// shares are the languages by lines, the biggest four and then the rest as Other.
func shares(files []scannedFile) (out []langShare, total, n int) {
	by := map[string]int{}
	for _, f := range files {
		if f.path == "" {
			continue
		}
		n++
		total += f.lines
		by[cmp.Or(f.lang, "Other")] += f.lines
	}
	for name, l := range by {
		if name != "Other" {
			out = append(out, langShare{name, l})
		}
	}
	slices.SortFunc(out, func(a, b langShare) int {
		return cmp.Or(cmp.Compare(b.lines, a.lines), strings.Compare(a.name, b.name))
	})
	other := by["Other"]
	if len(out) > len(tOverviewLangs) {
		for _, s := range out[len(tOverviewLangs):] {
			other += s.lines
		}
		out = out[:len(tOverviewLangs)]
	}
	if other > 0 {
		out = append(out, langShare{"Other", other})
	}
	return out, total, n
}

func (o *overview) languages(ctx registry.Ctx, dir string) (ui.Node, error) {
	s := o.scanOf(ctx, dir)
	if s.err != nil {
		return nil, s.err
	}
	ls, total, n := shares(s.files)
	approx := ""
	if s.approx {
		approx = "~"
	}
	return &ui.Func{Key: "languages", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		c.Column("body", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			c.Row("big", ui.Style{Gap: 6, Cross: ui.End, Pad: ui.Insets{B: 4}}, func(c *ui.Ctx) {
				c.Text("lines", approx+kit.Thousands(total), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 26, Weight: typeset.SemiBold},
					Color: th.C(theme.Fg)})
				c.Text("of", fmt.Sprintf("lines · %s files", kit.Thousands(n)), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11},
					Color: th.C(theme.FgFaint)})
			})
			colour := func(i int, name string) render.Color {
				if name == "Other" || i >= len(tOverviewLangs) {
					return th.C(tOverviewOther)
				}
				return th.C(tOverviewLangs[i])
			}
			c.Paint("bar", ui.Flex(1), ui.Px(10), func(f render.Frame, r render.Rect) {
				if total == 0 {
					f.Rect(r, render.Style{Fill: th.C(tOverviewEmpty), Radius: 5})
					return
				}
				x := r.Min.X
				gap := float32(2)
				w := r.Dx() - gap*float32(len(ls)-1)
				for i, l := range ls {
					lw := w * float32(l.lines) / float32(total)
					f.Rect(render.XYWH(x, r.Min.Y, max(lw, 1), r.Dy()), render.Style{Fill: colour(i, l.name), Radius: 2})
					x += lw + gap
				}
			})
			c.Column("legend", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
				for i := 0; i < len(ls); i += 2 {
					c.Row(i, ui.Style{W: ui.Flex(1), Gap: 14}, func(c *ui.Ctx) {
						for j := i; j < min(i+2, len(ls)); j++ {
							l := ls[j]
							c.Row(j, ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
								c.Box("swatch", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: colour(j, l.name)}, nil)
								mono := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgMuted)}
								c.Text("name", l.name, mono)
								c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
								mono.Color = th.C(theme.FgFaint)
								c.Text("pct", percent(l.lines, total), mono)
							})
						}
					})
				}
			})
		})
	}}, nil
}

func (o *overview) size(ctx registry.Ctx, dir string) (ui.Node, error) {
	s := o.scanOf(ctx, dir)
	if s.err != nil {
		return nil, s.err
	}
	var size int64
	n, dirs := 0, map[string]bool{}
	for _, f := range s.files {
		if f.path != "" {
			n, size = n+1, size+f.bytes
			dirs[filepath.Dir(f.path)] = true
		}
	}
	return statRows([][2]string{{"files", kit.Thousands(n)}, {"directories", kit.Thousands(len(dirs))}, {"on disk", kit.Bytes(size)}}), nil
}

func (o *overview) largest(ctx registry.Ctx, dir string) (ui.Node, error) {
	s := o.scanOf(ctx, dir)
	if s.err != nil {
		return nil, s.err
	}
	fs := slices.Clone(s.files)
	slices.SortFunc(fs, func(a, b scannedFile) int { return cmp.Compare(b.lines, a.lines) })
	var rows [][2]string
	for _, f := range fs[:min(len(fs), 5)] {
		if f.lines > 0 {
			rows = append(rows, [2]string{f.path, kit.Thousands(f.lines)})
		}
	}
	return statRows(rows), nil
}

// statRows is a list of names with numbers on the right.
func statRows(rows [][2]string) ui.Node {
	return &ui.Func{Key: "rows", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		c.Column("rows", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			for i, r := range rows {
				c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
					c.Box("name", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
						c.Text("t", r[0], ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgMuted)})
					})
					c.Text("n", r[1], ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.FgFaint)})
				})
			}
		})
	}}
}

const (
	heatWeeks = 52
	heatCell  = 12
	heatGap   = 3
)

func (o *overview) activity(ctx registry.Ctx, dir string) (ui.Node, error) {
	r := gitx.Repo{Dir: dir}
	now := o.now()
	end := now.AddDate(0, 0, 6-int(now.Weekday())) // the Saturday ending this week
	start := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location()).AddDate(0, 0, -7*heatWeeks+1)
	times, err := r.CommitTimes(ctx, start)
	if err != nil {
		return nil, err
	}
	last, err := r.Last(ctx)
	if err != nil {
		return nil, err
	}
	var days [heatWeeks * 7]int
	var weekdays [7]int
	for _, t := range times {
		if d := int(t.Sub(start).Hours() / 24); d >= 0 && d < len(days) {
			days[d]++
		}
		weekdays[t.Weekday()]++
	}
	peak := slices.Max(days[:])
	busiest := ""
	if len(times) > 0 {
		busiest = " · busiest " + time.Weekday(slices.Index(weekdays[:], slices.Max(weekdays[:]))).String()[:3]
	}
	summary := fmt.Sprintf("%s commits in the last year%s", kit.Thousands(len(times)), busiest)
	return &ui.Func{Key: "activity", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		c.Column("body", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			faint := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)}
			c.Text("summary", summary, faint)
			c.Paint("heat", ui.Flex(1), ui.Px(7*heatCell+6*heatGap), func(f render.Frame, rc render.Rect) {
				cell := min(heatCell, (rc.Dx()-heatGap*(heatWeeks-1))/heatWeeks)
				step := (rc.Dx() - cell) / (heatWeeks - 1) // the weeks spread across the panel
				heat := th.C(tOverviewHeat)
				for i, n := range days {
					x := rc.Min.X + float32(i/7)*step
					y := rc.Min.Y + float32(i%7)*(cell+heatGap)
					col := th.C(tOverviewEmpty)
					if n > 0 {
						col = theme.WithAlpha(heat, .25+.75*float64(n)/float64(max(peak, 1)))
					}
					f.Rect(render.XYWH(x, y, cell, cell), render.Style{Fill: col, Radius: 2})
				}
			})
			c.Row("months", ui.Style{W: ui.Flex(1), Main: ui.SpaceBetween}, func(c *ui.Ctx) {
				for m := range 12 {
					c.Text(m, start.AddDate(0, m+1, 0).Format("Jan"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5}, Color: th.C(theme.FgGhost)})
				}
			})
			c.Row("last", ui.Style{Pad: ui.Insets{T: 4}, Gap: 10}, func(c *ui.Ctx) {
				c.Box("rule", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: th.C(theme.LineStrong)}, nil)
				c.Column("text", ui.Style{Gap: 3}, func(c *ui.Ctx) {
					c.Text("subject", last.Subject, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgMuted)})
					c.Text("meta", fmt.Sprintf("%s · %s · %s ago", last.Hash, last.Author, kit.Ago(o.now(), last.When)), faint)
				})
			})
		})
	}}, nil
}

func (o *overview) contributors(ctx registry.Ctx, dir string) (ui.Node, error) {
	as, err := gitx.Repo{Dir: dir}.Authors(ctx)
	if err != nil {
		return nil, err
	}
	return &ui.Func{Key: "contributors", Fn: func(c *ui.Ctx) {
		th := c.Theme()
		c.Column("body", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			top := 0
			if len(as) > 0 {
				top = as[0].Commits
			}
			for i, a := range as[:min(len(as), 4)] {
				c.Row(i, ui.Style{W: ui.Flex(1), Gap: 9, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("av", ui.Style{W: ui.Px(20), H: ui.Px(20), Radius: 5, Fill: th.C(tOverviewLangs[i%len(tOverviewLangs)]), Main: ui.Center, Cross: ui.Center},
						func(c *ui.Ctx) {
							c.Text("i", kit.Initials(a.Name), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 8.5, Weight: typeset.Bold}, Color: th.C(theme.AccentInk)})
						})
					c.Box("name", ui.Style{W: ui.Px(110), Clip: true}, func(c *ui.Ctx) {
						c.Text("t", a.Name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgMuted)})
					})
					share := float32(a.Commits) / float32(max(top, 1))
					c.Paint("bar", ui.Flex(1), ui.Px(5), func(f render.Frame, r render.Rect) {
						f.Rect(r, render.Style{Fill: th.C(tOverviewEmpty), Radius: 2.5})
						f.Rect(render.XYWH(r.Min.X, r.Min.Y, r.Dx()*share, r.Dy()), render.Style{Fill: th.C(tOverviewBar), Radius: 2.5})
					})
					c.Box("n", ui.Style{W: ui.Px(40), Main: ui.End}, func(c *ui.Ctx) {
						c.Text("t", kit.Thousands(a.Commits), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
					})
				})
			}
			if len(as) > 4 {
				c.Text("more", fmt.Sprintf("+ %d more · %d people", len(as)-4, len(as)),
					ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
			}
		})
	}}, nil
}

func percent(n, total int) string {
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%d%%", (n*100+total/2)/total)
}
