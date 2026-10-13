package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/regex"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/worker/search"
)

// buffersSource is the open buffers, as :ls lists them; it's what :ls opens.
func (x *editorExt) buffersSource() registry.PickerSource {
	return registry.PickerSource{ID: "buffers", Title: "Buffers", Order: 32, PickerOnly: true,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			var out []registry.PickerItem
			for _, b := range x.e.BufferList() {
				flag := ""
				switch {
				case b.Current:
					flag = "%"
				case b.Alternate:
					flag = "#"
				}
				it := registry.PickerItem{ID: fmt.Sprint(b.Num), Label: b.Name, Detail: fmt.Sprintf("%d%s", b.Num, flag), IconStyle: "status.working",
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
		Preview: func(it registry.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			x.e.Run("buffer " + it.ID)
			return nil
		},
	}
}

// quickfixSource is the quickfix list; :copen opens it.
func (x *editorExt) quickfixSource() registry.PickerSource {
	return registry.PickerSource{ID: "quickfix", Title: "Quickfix", Order: 60, PickerOnly: true,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			items, _, cur := x.e.Quickfix()
			var out []registry.PickerItem
			for i, q := range items {
				it := registry.PickerItem{ID: fmt.Sprint(i), Label: q.Text, Detail: fmt.Sprintf("%s:%d", q.Name, q.Line), Path: q.Name, Line: q.Line, Col: q.Col, Data: i}
				if i == cur {
					it.Note = "current"
				}
				out = append(out, it)
			}
			return out
		},
		Preview: func(it registry.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
		Run:     func(ctx registry.Ctx, it registry.PickerItem) error { return x.e.QuickfixJump(it.Data.(int)) },
	}
}

// fileSymbolsSource is the current buffer's functions, methods and types.
func (x *editorExt) fileSymbolsSource(all registry.PickerSource) registry.PickerSource {
	s := all
	s.ID, s.Title, s.Prefix, s.Order, s.PickerOnly = "symbols.file", "Symbols here", "", 41, true
	s.Items = func(ctx registry.Ctx, q string) []registry.PickerItem {
		name := x.e.Name()
		var out []registry.PickerItem
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
	results []registry.PickerItem
	cancel  context.CancelFunc
}

const maxGrep = 1000

// grepSource searches the work dir's files as you type, with project search's engine (ripgrep, streamed), leaving
// out generated and vendored files unless the query starts "@all ". The query is a PCRE2 pattern, or a literal
// when it isn't one.
func (x *editorExt) grepSource() registry.PickerSource {
	g := &grep{}
	run := func(q string) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.query == q {
			return
		}
		if g.cancel != nil {
			g.cancel()
		}
		g.query, g.results = q, nil
		if q == "" {
			return
		}
		pat, all := strings.CutPrefix(q, "@all ")
		if _, err := regex.Compile(pat, regex.Flags{}); err != nil {
			pat = `\Q` + pat + `\E`
		}
		sq := search.Query{Pattern: pat, Dir: x.dir, Max: maxGrep}
		if !all {
			sq.Exclude = x.unsearchable()
		}
		ctx, cancel := context.WithCancel(context.Background())
		g.cancel = cancel
		go search.Run(ctx, sq, func(ms []search.Match) {
			g.mu.Lock()
			if g.query == q {
				for _, m := range ms {
					col := 1
					if len(m.Spans) > 0 {
						col = m.Spans[0][0] + 1
					}
					g.results = append(g.results, registry.PickerItem{ID: fmt.Sprintf("%s:%d", m.Path, m.Line), Label: strings.TrimSpace(m.Text),
						Detail: fmt.Sprintf("%s:%d", m.Path, m.Line), IconStyle: "status.working", Path: m.Path, Line: m.Line, Col: col, Boost: -len(g.results)})
				}
			}
			g.mu.Unlock()
			x.h.Redraw()
		})
	}
	return registry.PickerSource{ID: "grep", Title: "Grep", Order: 31, Live: true, PickerOnly: true,
		Items: func(_ registry.Ctx, q string) []registry.PickerItem {
			run(q)
			g.mu.Lock()
			defer g.mu.Unlock()
			return slices.Clone(g.results)
		},
		Preview: func(it registry.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
	}
}

// keymapsSource is every keymap, with its mode, keys and source: Enter runs it, ⌃e opens where it's defined.
func keymapsSource(h ext.Host, configDir string) registry.PickerSource {
	return registry.PickerSource{ID: "keymaps", Title: "Keymaps", Order: 50, PickerOnly: true,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			var out []registry.PickerItem
			for i, k := range h.Keymaps() {
				label := k.Desc
				if label == "" {
					label = k.Command
				}
				out = append(out, registry.PickerItem{ID: fmt.Sprint(i), Label: label + " " + k.Keys, Detail: k.Mode + " · " + k.Command, Note: k.Source,
					Icon: "⌨", Data: k})
			}
			return out
		},
		Preview: func(it registry.PickerItem) ui.Node {
			k := it.Data.(registry.Keymap)
			return &ui.Func{Key: "keymap", Fn: func(c *ui.Ctx) {
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					previewHead(c, k.Mode, ui.TextStyle{Color: c.Theme().C(tOmniMatch)}, k.Keys)
					previewField(c, "command", k.Command)
					if len(k.Args) > 0 {
						previewField(c, "args", fmt.Sprint(k.Args))
					}
					previewField(c, "source", k.Source)
					if k.Desc != "" {
						previewField(c, "desc", k.Desc)
					}
				})
			}}
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			k := it.Data.(registry.Keymap)
			return h.Run(ctx, k.Command, k.Args)
		},
		Actions: []registry.PickerAction{{Key: "<C-e>", Title: "go to definition", Run: func(ctx registry.Ctx, items []registry.PickerItem) error {
			k := items[0].Data.(registry.Keymap)
			file, line := script.SplitLoc(k.Source)
			if line == 0 {
				return fmt.Errorf("%s isn't a place in a file", k.Source)
			}
			path := file
			if !filepath.IsAbs(path) {
				path = filepath.Join(configDir, file)
			}
			return h.Run(ctx, "editor.open", registry.Args{"path": path, "line": line})
		}}},
	}
}

// pickerKeys are the default keys (ui.md 02b), with the leader.
var pickerKeys = []struct{ keys, source, desc string }{
	{"<Leader><Leader>", "files", "Find files"},
	{"<Leader>/", "grep", "Grep"},
	{"<Leader>,", "buffers", "Buffers"},
	{"<Leader>s", "symbols.file", "Symbols here"},
	{"<Leader>S", "symbols", "Symbols"},
	{"<Leader>k", "keymaps", "Keymaps"},
	{"<Leader>:", "commands", "Commands"},
}
