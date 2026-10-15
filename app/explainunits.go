package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ChangedUnitsView is the Changed units panel: every unit the branch touched, with its explain status.
const ChangedUnitsView = "explain.units"

// fileUnits are a version of a file's explain units, and the ones its changes against the branch's base touch.
type fileUnits struct {
	version uint64 // the buffer's, +1; 0 for a file read from disk
	gen     int    // the base's
	src     []byte
	units   []text.Unit
	owners  []int    // the units owning a change, by index
	hashes  []string // each unit's content hash
	hunks   int
}

// explainBase is a file as the branch's base has it.
type explainBase struct {
	data    []byte
	loaded  bool
	loading bool
	gen     int
}

// unitHash keys a unit's explanation: its kind and its text, so an edit inside it changes the key and an edit
// elsewhere doesn't.
func unitHash(kind string, content []byte) string {
	h := sha256.New()
	h.Write([]byte(kind + "\x00"))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// parseUnits are src's explain units, in the language for path; none when it has no units query.
func (x *explainExt) parseUnits(path string, src []byte) []text.Unit {
	lang, ok := languageFor(x.h, path, src)
	if !ok {
		return nil
	}
	x.parseMu.Lock()
	defer x.parseMu.Unlock()
	p := x.parsers[lang.Name]
	if p == nil {
		var err error
		if p, err = syntax.NewParser(lang); err != nil {
			return nil
		}
		x.parsers[lang.Name] = p
	}
	p.Parse(src)
	us, _ := p.Units()
	return text.FilterUnits(us, text.Explainable)
}

// changedRanges are the changed lines of src as ranges of their text, and a deletion as the end of the line before it.
func changedRanges(src []byte, hunks []gitx.Hunk) []text.Range {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	lineEnd := func(l int) int {
		if l+1 < len(starts) {
			return starts[l+1] - 1
		}
		return len(src)
	}
	var out []text.Range
	for _, h := range hunks {
		if h.NewLines == 0 {
			l := min(max(h.NewStart-1, 0), len(starts)-1)
			out = append(out, text.Range{Start: lineEnd(l), End: lineEnd(l)})
			continue
		}
		for l := h.NewStart - 1; l < h.NewStart-1+h.NewLines && l < len(starts); l++ {
			a, z := starts[l], lineEnd(l)
			for a < z && (src[a] == ' ' || src[a] == '\t') {
				a++
			}
			for z > a && (src[z-1] == ' ' || src[z-1] == '\t' || src[z-1] == '\r') {
				z--
			}
			if a < z {
				out = append(out, text.Range{Start: a, End: z})
			}
		}
	}
	return out
}

// computeUnits are src's units and the ones base→file touches: file is src as it would be written.
func (x *explainExt) computeUnits(path string, src, file, base []byte, haveBase bool) *fileUnits {
	fu := &fileUnits{src: src, units: x.parseUnits(path, src)}
	for _, u := range fu.units {
		fu.hashes = append(fu.hashes, unitHash(u.Kind, src[u.Range.Start:u.Range.End]))
	}
	if haveBase {
		hunks := gitx.DiffLines(base, file)
		fu.hunks = len(hunks)
		fu.owners = text.Owners(fu.units, changedRanges(src, hunks))
	}
	return fu
}

// baseOf is the branch's base's version of a file, loaded off the loop: ok is false until it's read, or outside a repo.
func (x *explainExt) baseOf(abs string) (data []byte, gen int, ok bool) {
	g := x.sh.git
	if g == nil || g.status == nil || abs == "" {
		return nil, 0, false
	}
	rel := g.rel(abs)
	if strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return nil, 0, false
	}
	st := g.state()
	if st == nil {
		return nil, 0, false
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	b := x.bases[rel]
	if b == nil {
		b = &explainBase{}
		x.bases[rel] = b
	}
	if !b.loaded && !b.loading {
		b.loading = true
		rev := st.BaseHash
		repo := g.status.Repo()
		x.busy.Add(1)
		go func() {
			defer x.busy.Done()
			var data []byte
			if rev != "" {
				data, _ = repo.FileAt(context.Background(), rev, rel)
			}
			x.h.Do(func() {
				x.mu.Lock()
				b.data, b.loaded, b.loading = data, true, false
				b.gen++
				x.mu.Unlock()
				x.h.Redraw()
			})
		}()
	}
	return b.data, b.gen, b.loaded
}

// bufferUnits are a buffer's units and changed units, worked out off the loop for each version; until then, the
// last version's.
func (x *explainExt) bufferUnits(path string, buf *text.Buffer, fileBytes func() []byte) *fileUnits {
	snap := buf.Snapshot()
	base, gen, haveBase := x.baseOf(path)
	x.mu.Lock()
	fu := x.bufs[buf]
	stale := fu == nil || fu.version != snap.Version()+1 || fu.gen != gen
	if !stale || x.computing[buf] {
		x.mu.Unlock()
		return fu
	}
	x.computing[buf] = true
	x.mu.Unlock()
	src, file := snap.Bytes(), fileBytes()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		nu := x.computeUnits(path, src, file, base, haveBase)
		nu.version, nu.gen = snap.Version()+1, gen
		x.h.Do(func() {
			x.mu.Lock()
			x.bufs[buf] = nu
			delete(x.computing, buf)
			x.mu.Unlock()
			x.h.Redraw()
		})
	}()
	return fu
}

// unitsNow are a buffer's units as it is now, parsed here if they haven't been yet.
func (x *explainExt) unitsNow(path string, buf *text.Buffer) []text.Unit {
	snap := buf.Snapshot()
	x.mu.Lock()
	fu := x.bufs[buf]
	x.mu.Unlock()
	if fu != nil && fu.version == snap.Version()+1 {
		return fu.units
	}
	return x.parseUnits(path, snap.Bytes())
}

// changedUnit is a row of the Changed units panel: a unit the branch touched, or one around it.
type changedUnit struct {
	path  string
	unit  text.Unit
	label string
	hash  string
	line  int // 0-based
	depth int
	owner bool // it owns a change itself
}

// changedFile is a changed file's rows.
type changedFile struct {
	path, rel string
	units     []changedUnit
}

// panelState is the Changed units panel's: its rows, and what they were worked out from.
type panelState struct {
	key     string
	files   []changedFile
	loading bool
}

// changedFiles are the branch's changed files' units, worked out off the loop when the status or an open buffer
// changes.
func (x *explainExt) changedFiles() ([]changedFile, bool) {
	g := x.sh.git
	st := g.state()
	if st == nil {
		return nil, false
	}
	var paths []string
	key := st.BaseHash
	for _, f := range st.Files(gitx.Changed) {
		if f.Code == 'D' || f.Binary || strings.HasSuffix(f.Path, "/") {
			continue
		}
		abs := filepath.Join(g.root(), filepath.FromSlash(f.Path))
		paths = append(paths, abs)
		if b, ok := x.sh.editor.openBuffer(abs); ok {
			key += fmt.Sprintf("|%s@%d", f.Path, b.Text.Snapshot().Version())
		} else if fi, err := os.Stat(abs); err == nil {
			key += fmt.Sprintf("|%s:%d:%d", f.Path, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.panel.key == key || x.panel.loading {
		return x.panel.files, x.panel.loading
	}
	x.panel.loading = true
	rev, repo, root := st.BaseHash, g.status.Repo(), g.root()
	srcs, writes := map[string][]byte{}, map[string][]byte{}
	for _, p := range paths {
		if b, ok := x.sh.editor.openBuffer(p); ok {
			srcs[p], writes[p] = b.Text.Snapshot().Bytes(), b.Bytes()
		}
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		var files []changedFile
		for _, p := range paths {
			src, ok := srcs[p]
			file := writes[p]
			if !ok {
				var err error
				if src, err = os.ReadFile(p); err != nil {
					continue
				}
				file = src
			}
			rel, _ := filepath.Rel(root, p)
			var base []byte
			if rev != "" {
				base, _ = repo.FileAt(context.Background(), rev, filepath.ToSlash(rel))
			}
			fu := x.computeUnits(p, src, file, base, true)
			if cf := (changedFile{path: p, rel: filepath.ToSlash(rel), units: unitRows(p, fu)}); len(cf.units) > 0 {
				files = append(files, cf)
			}
		}
		x.h.Do(func() {
			x.mu.Lock()
			x.panel = panelState{key: key, files: files}
			x.mu.Unlock()
			x.h.Redraw()
		})
	}()
	return x.panel.files, true
}

// unitRows are the units owning changes and the units around them, in the file's order, each once.
func unitRows(path string, fu *fileUnits) []changedUnit {
	keep := map[int]bool{}
	for _, i := range fu.owners {
		for _, j := range text.UnitPath(fu.units, i) {
			keep[j] = true
		}
	}
	var out []changedUnit
	for i, u := range fu.units {
		if !keep[i] {
			continue
		}
		out = append(out, changedUnit{path: path, unit: u, label: u.Label(fu.src), hash: fu.hashes[i], line: lineAt(fu.src, u.Range.Start),
			depth: len(text.UnitPath(fu.units, i)) - 1, owner: slices.Contains(fu.owners, i)})
	}
	return out
}

func lineAt(src []byte, off int) int { return strings.Count(string(src[:min(off, len(src))]), "\n") }

// unitStatus is what the panel and the ? marks say of a unit: "open" while its card is, "explained", "changed" once
// an edit inside it has made its explanation stale, else "".
func (x *explainExt) unitStatus(path string, u text.Unit, hash string) string {
	if cd := x.card; cd != nil && filepath.Clean(cd.path) == filepath.Clean(path) {
		if r, ok := cd.rangeOf(cd.sel); ok && r.Start == u.Range.Start && !cd.crumbs[cd.sel].selection {
			return "open"
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	for k, a := range x.answers {
		if k.hash == hash && a.text != "" {
			return "explained"
		}
	}
	for _, m := range x.marks {
		if filepath.Clean(m.path) != filepath.Clean(path) || m.kind != u.Kind {
			continue
		}
		if off, ok := m.buf.Resolve(m.start); ok && off == u.Range.Start {
			return "changed"
		}
	}
	return ""
}

var (
	tUnitsDone    = theme.Declare("explain.status.explained", "ok")
	tUnitsOpen    = theme.Declare("explain.status.open", "accent")
	tUnitsChanged = theme.Declare("explain.status.changed", "accent.strong")
)

// unitStatusWord is a status as the panel shows it, and its colour.
func unitStatusWord(s string) (string, theme.TokenID) {
	switch s {
	case "explained":
		return "explained ✓", tUnitsDone
	case "open":
		return "open", tUnitsOpen
	case "changed":
		return "changed since", tUnitsChanged
	}
	return "–", theme.FgGhost
}

type changedUnitsView struct{ x *explainExt }

func (v changedUnitsView) Title(string) string { return "Changed units" }
func (v changedUnitsView) EdgeToEdge()         {}
func (v changedUnitsView) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "changed-units", Fn: v.x.panelView}
}

// panelView lists every unit the branch touched by file, nested as the code is, with each one's explain status;
// opening one goes to it and explains it (25-explain-unit).
func (x *explainExt) panelView(c *ui.Ctx) {
	th := c.Theme()
	files, loading := x.changedFiles()
	n := 0
	var roots []widget.TreeItem
	for _, f := range files {
		file := widget.TreeItem{ID: f.path, Label: f.rel, Section: true, Open: true, Help: "editor/pane#changed-units"}
		for _, u := range f.units {
			if u.owner {
				n++
			}
		}
		i := 0
		file.Children = unitTree(f.units, &i, 0, func(u changedUnit) widget.TreeItem {
			word, tok := unitStatusWord(x.unitStatus(f.path, u.unit, u.hash))
			return widget.TreeItem{ID: fmt.Sprintf("%s:%d:%d", f.path, u.line+1, u.unit.Range.Start), Label: u.label, Note: strings.ToUpper(u.unit.Kind),
				Mark: word, MarkTone: th.C(tok), Open: true, Help: "editor/pane#changed-units"}
		})
		roots = append(roots, file)
	}
	c.Column("units", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tExplorerBg), Help: "editor/pane#changed-units", Role: ui.RoleRegion,
		Label: "Changed units"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "CHANGED UNITS", monoCaps(c, theme.FgFaint))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprintf("%d %s · tree-sitter", n, plural(n, "unit", "units")), monoTok(c, 10, theme.FgGhost))
		})
		if n > 0 {
			c.Row("walk", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 6}}, func(c *ui.Ctx) {
				if widget.Link(c, "explain", "Explain this change, step by step", "editor/pane#explain-change") {
					if err := x.h.Run(context.Background(), "explain.change", nil); err != nil {
						x.notify(err)
					}
				}
			})
		}
		if len(roots) == 0 {
			msg := "The branch changes no code units."
			switch {
			case x.sh.git.state() == nil:
				msg = "Not in a git repository: there's no branch to compare with."
			case loading:
				msg = "Reading the changes…"
			}
			c.Box("none", ui.Style{Pad: ui.PadXY(12, 6)}, func(c *ui.Ctx) { c.Text("t", msg, monoTok(c, 11, theme.FgGhost)) })
			return
		}
		res := widget.Tree(c, "tree", roots, widget.TreeOpts{Help: "editor/pane#changed-units", Label: "Changed units"})
		if res.Activated != "" {
			x.openRow(res.Activated)
		}
	})
}

// unitTree nests rows by depth, from row *i while they're at least depth deep.
func unitTree(us []changedUnit, i *int, depth int, item func(changedUnit) widget.TreeItem) []widget.TreeItem {
	var out []widget.TreeItem
	for *i < len(us) && us[*i].depth >= depth {
		it, d := item(us[*i]), us[*i].depth
		*i++
		it.Children = unitTree(us, i, d+1, item)
		out = append(out, it)
	}
	return out
}

// monoCaps is a panel heading's style.
func monoCaps(c *ui.Ctx, tok theme.TokenID) ui.TextStyle {
	st := monoTok(c, 10, tok)
	st.Font.Tracking = .14
	return st
}

// openRow goes to a panel row's unit and explains it.
func (x *explainExt) openRow(id string) {
	parts := strings.Split(id, ":")
	if len(parts) < 3 {
		return
	}
	path := strings.Join(parts[:len(parts)-2], ":")
	var line int
	fmt.Sscan(parts[len(parts)-2], &line)
	ctx := context.Background()
	if err := x.h.Run(ctx, "editor.open", registry.Args{"path": path, "line": line}); err != nil {
		x.notify(err)
		return
	}
	if err := x.h.Run(ctx, "explain.unit", registry.Args{"file": path, "line": line}); err != nil {
		x.notify(err)
	}
}
