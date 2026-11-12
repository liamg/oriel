package editor

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/search"
	"github.com/liamg/oriel/regex"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
)

// buffersSource is the open buffers, as :ls lists them; it's what :ls opens.
func (x *Editor) buffersSource() uireg.PickerSource {
	return uireg.PickerSource{ID: "buffers", Title: "Buffers", Order: 32, PickerOnly: true,
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			for _, b := range x.e.BufferList() {
				flag := ""
				switch {
				case b.Current:
					flag = "%"
				case b.Alternate:
					flag = "#"
				}
				it := uireg.PickerItem{ID: fmt.Sprint(b.Num), Label: b.Name, Detail: fmt.Sprintf("%d%s", b.Num, flag), IconStyle: "status.working",
					Line: b.Line + 1, Data: b}
				if b.File {
					it.Path = b.Name
				}
				if b.Modified {
					it.Note = "modified"
				}
				out = append(out, it)
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			x.e.Run("buffer " + it.ID)
			return nil
		},
	}
}

// quickfixSource is the quickfix list; :copen opens it.
func (x *Editor) quickfixSource() uireg.PickerSource {
	return uireg.PickerSource{ID: "quickfix", Title: "Quickfix", Order: 60, PickerOnly: true,
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			items, _, cur := x.e.Quickfix()
			var out []uireg.PickerItem
			for i, q := range items {
				it := uireg.PickerItem{ID: fmt.Sprint(i), Label: q.Text, Detail: fmt.Sprintf("%s:%d", q.Name, q.Line), Path: q.Name, Line: q.Line, Col: q.Col, Data: i}
				if i == cur {
					it.Note = "current"
				}
				out = append(out, it)
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
		Run:     func(ctx registry.Ctx, it uireg.PickerItem) error { return x.e.QuickfixJump(it.Data.(int)) },
	}
}

// fileSymbolsSource is the current buffer's functions, methods and types.
func (x *Editor) fileSymbolsSource(all uireg.PickerSource) uireg.PickerSource {
	s := all
	s.ID, s.Title, s.Prefix, s.Order, s.PickerOnly = "symbols.file", "Symbols here", "", 41, true
	s.Items = func(ctx registry.Ctx, q string) []uireg.PickerItem {
		name := x.e.Name()
		var out []uireg.PickerItem
		for _, it := range all.Items(ctx, q) {
			if it.Path == name {
				out = append(out, it)
			}
		}
		return out
	}
	return s
}

// grep is the live grep's state: the query it last ran, its results, and the search running.
type grep struct {
	mu      sync.Mutex
	query   string
	results []uireg.PickerItem
	cancel  context.CancelFunc
	running bool // the query's search hasn't finished
}

const maxGrep = 1000

// grepSource searches the work dir's files as you type, with project search's engine (ripgrep, streamed), leaving
// out generated and vendored files unless the query starts "@all ". The query is a PCRE2 pattern, or a literal
// when it isn't one.
func (x *Editor) grepSource() uireg.PickerSource {
	g := &grep{}
	emit := func(q string) func([]search.Match) {
		return func(ms []search.Match) {
			g.mu.Lock()
			if g.query == q {
				for _, m := range ms {
					col := 1
					if len(m.Spans) > 0 {
						col = m.Spans[0][0] + 1
					}
					g.results = append(g.results, uireg.PickerItem{ID: fmt.Sprintf("%s:%d", m.Path, m.Line), Label: strings.TrimSpace(m.Text),
						Detail: fmt.Sprintf("%s:%d", m.Path, m.Line), IconStyle: "status.working", Path: m.Path, Line: m.Line, Col: col, Boost: -len(g.results)})
				}
			}
			g.mu.Unlock()
			x.h.Redraw()
		}
	}
	run := func(q string) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.query == q {
			return
		}
		if g.cancel != nil {
			g.cancel()
		}
		g.query, g.results, g.running = q, nil, false
		if q == "" {
			return
		}
		pat, all := strings.CutPrefix(q, "@all ")
		if _, err := regex.Compile(pat, regex.Flags{}); err != nil {
			pat = `\Q` + pat + `\E`
		}
		sq := search.Query{Pattern: pat, Dir: x.dir, Max: maxGrep}
		ctx, cancel := context.WithCancel(context.Background())
		g.cancel, g.running = cancel, true
		x.busy.Go(func() {
			if !all {
				sq.Exclude = x.unsearchable()
			}
			x.Tools.Run(ctx, sq, emit(q))
			g.mu.Lock()
			if g.query == q {
				g.running = false
			}
			g.mu.Unlock()
			x.h.Redraw()
		})
	}
	return uireg.PickerSource{ID: "grep", Title: "Grep", Order: 31, Live: true, PickerOnly: true,
		Items: func(_ registry.Ctx, q string) []uireg.PickerItem {
			run(q)
			g.mu.Lock()
			defer g.mu.Unlock()
			return slices.Clone(g.results)
		},
		Loading: func() bool {
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.running
		},
		Preview: func(it uireg.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
	}
}

// pickerKeys are the default keys, with the leader.
var pickerKeys = []struct{ keys, source, desc string }{
	{"<Leader><Leader>", "files", "Find files"},
	{"<Leader>/", "grep", "Grep"},
	{"<Leader>,", "buffers", "Buffers"},
	{"<Leader>s", "symbols.file", "Symbols here"},
	{"<Leader>S", "symbols", "Symbols"},
	{"<Leader>k", "keymaps", "Keymaps"},
	{"<Leader>:", "commands", "Commands"},
}

// fileList is the files under a directory, walked off the loop: the first walk's files show as they're found, and
// a list older than fileFresh is walked again in the background, the old one shown until the new is done.
type fileList struct {
	redraw func()
	dir    string // (mu)

	mu      sync.Mutex
	at      time.Time // when the last walk finished
	files   []string
	walking bool
	done    bool // a walk has finished
	busy    kit.Busy
}

const (
	maxFiles  = 50000
	fileFresh = 5 * time.Second
)

// list is the files found so far, and whether the first walk is still going.
func (l *fileList) list() (files []string, loading bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.walking && (!l.done || time.Since(l.at) >= fileFresh) {
		l.walking = true
		dir, first := l.dir, !l.done
		l.busy.Go(func() { l.walk(dir, first) })
	}
	return l.files, !l.done
}

func (l *fileList) loading() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.done
}

// setDir lists dir's files instead, as the window moves onto it: what was found under the last is dropped.
func (l *fileList) setDir(dir string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if dir != l.dir {
		l.dir, l.files, l.done = dir, nil, false
	}
}

func (l *fileList) walk(dir string, first bool) {
	shown := time.Now()
	found := walkFiles(dir, func(found []string) {
		if first && time.Since(shown) > 100*time.Millisecond {
			shown = time.Now()
			l.mu.Lock()
			if l.dir == dir {
				l.files = slices.Clone(found)
			}
			l.mu.Unlock()
			l.redraw()
		}
	})
	l.mu.Lock()
	l.walking = false
	if l.dir == dir { // a walk of the directory the window has left finds nothing to keep
		l.files, l.at, l.done = found, time.Now(), true
	}
	l.mu.Unlock()
	l.redraw()
}

// walkFiles is the files under dir, relative to it, at most maxFiles; each, when not nil, sees them as they're found.
func walkFiles(dir string, each func(found []string)) []string {
	var found []string
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
			found = append(found, filepath.ToSlash(rel))
		}
		if each != nil {
			each(found)
		}
		if len(found) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// filesKey is what the files source's items are made from: the files found, the status, and the buffers open.
type filesKey struct {
	first *string
	n     int
	st    *gitx.State
	open  string
}

// filesSource is the files of the editor's directory; open ones and shallower paths rank higher.
func (x *Editor) filesSource() uireg.PickerSource {
	files := &fileList{dir: x.dir, redraw: x.h.Redraw}
	x.listed = files
	x.Add(kit.OnDispose(files.busy.Wait))
	openNames := func() []string {
		var open []string
		for _, b := range x.e.BufferList() {
			open = append(open, b.Name)
		}
		return open
	}
	return uireg.PickerSource{ID: "files", Title: "Files", Prefix: "@", Order: 30, Loading: files.loading,
		Key: func() any {
			list, _ := files.list()
			k := filesKey{n: len(list), st: kit.GitOf(x.h).Tree("").State(), open: strings.Join(openNames(), "\x00")}
			if len(list) > 0 {
				k.first = &list[0]
			}
			return k
		},
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			open := openNames()
			tree := kit.GitOf(x.h).Tree("")
			st := tree.State()
			list, _ := files.list()
			var out []uireg.PickerItem
			for _, f := range list {
				it := uireg.PickerItem{ID: f, Label: f, IconStyle: "status.working", Boost: -strings.Count(f, "/"), Path: f}
				var notes []string
				if fs, ok := st.File(tree.Rel(f)); ok && fs.Code != 0 {
					it.Boost += 5
					notes = append(notes, string(fs.Code))
				}
				if slices.Contains(open, f) {
					it.Boost += 10
					notes = append(notes, "open")
				}
				it.Note = strings.Join(notes, " ")
				out = append(out, it)
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node { return x.filePreview(it.ID, 0) },
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			return x.h.Run(ctx, "editor.open", registry.Args{"path": it.ID})
		},
	}
}

// filePreview is a file's text from line, highlighted.
func (x *Editor) filePreview(name string, line int) ui.Node {
	return x.previewed.Node(x.h, x.dir, name, line)
}

// symbols is the outline of a buffer's text, kept until the text changes.
type symbols struct {
	version uint64
	units   []text.Unit
}

// symbolsSource is the functions, methods and types of the open buffers.
func (x *Editor) symbolsSource() uireg.PickerSource {
	cache := map[*text.Buffer]*symbols{}
	outline := func(name string, b *text.Buffer) []text.Unit {
		snap := b.Snapshot()
		if s, ok := cache[b]; ok && s.version == snap.Version() {
			return s.units
		}
		s := &symbols{version: snap.Version()}
		cache[b] = s
		lang, ok := kit.LanguageFor(x.h, name, snap.Bytes())
		if !ok {
			return nil
		}
		p, err := syntax.Borrow(lang)
		if err != nil {
			return nil
		}
		defer p.Release()
		p.ParseSnapshot(snap)
		s.units, _ = p.Outline()
		return s.units
	}
	return uireg.PickerSource{ID: "symbols", Title: "Symbols", Prefix: "#", Order: 40,
		Key: func() any {
			var k strings.Builder
			for _, id := range x.e.WindowIDs() {
				if p, ok := x.e.Pane(id); ok {
					fmt.Fprintf(&k, "%s\x00%d\x00", p.Name(), p.Buf().Version())
				}
			}
			return k.String()
		},
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			for _, id := range x.e.WindowIDs() {
				p, ok := x.e.Pane(id)
				if !ok || p.Name() == "" {
					continue
				}
				snap := p.Buf()
				for _, u := range outline(p.Name(), p.Buffer()) {
					if u.Name == "" {
						continue
					}
					line := snap.PosAt(u.Range.Start).Line
					icon, style := "ƒ", "@function"
					if u.Kind == "type" {
						icon, style = "T", "@type"
					}
					out = append(out, uireg.PickerItem{ID: fmt.Sprintf("%s:%d", p.Name(), line+1), Label: u.Name,
						Detail: fmt.Sprintf("%s:%d", p.Name(), line+1), Icon: icon, IconStyle: style, Note: u.Kind,
						Path: p.Name(), Line: line + 1})
				}
			}
			return dedupe(out)
		},
		// Preview and Run read the item's place, not a Data of the editor's own: a plugin that asks a language server
		// for the symbols in their place (the LSP's) keeps these.
		Preview: func(it uireg.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			return x.h.Run(ctx, "editor.open", registry.Args{"path": it.Path, "line": it.Line})
		},
	}
}

// dedupe drops items with an id seen before: the same buffer open in two windows.
func dedupe(items []uireg.PickerItem) []uireg.PickerItem {
	seen := map[string]bool{}
	out := items[:0]
	for _, it := range items {
		if !seen[it.ID] {
			seen[it.ID] = true
			out = append(out, it)
		}
	}
	return out
}
