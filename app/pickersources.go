package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/regex"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
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

// grep is the live grep's state: the query it last ran, its results, and whether one is running.
type grep struct {
	mu      sync.Mutex
	query   string
	results []registry.PickerItem
	cancel  context.CancelFunc
}

const maxGrep = 1000

// grepSource searches the work dir's files as you type, leaving out generated and vendored ones unless the query
// starts "@all ": the query is a PCRE2 pattern (a literal when it
// isn't one), and the search runs off the UI's goroutine, its results showing when it's done.
func (x *editorExt) grepSource() registry.PickerSource {
	g := &grep{}
	files := &fileList{dir: x.dir}
	run := func(q string) {
		g.mu.Lock()
		if g.query == q {
			g.mu.Unlock()
			return
		}
		if g.cancel != nil {
			g.cancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		g.query, g.cancel = q, cancel
		g.mu.Unlock()
		if q == "" {
			g.mu.Lock()
			g.results = nil
			g.mu.Unlock()
			return
		}
		pat, all := strings.CutPrefix(q, "@all ")
		re, err := regex.Compile(pat, regex.Flags{})
		if err != nil {
			re = regex.MustCompile(`\Q`+pat+`\E`, regex.Flags{})
		}
		list := append([]string(nil), files.list()...)
		if !all {
			list = x.conv.searchable(list)
		}
		go func() {
			out := searchFiles(ctx, x.dir, list, re)
			g.mu.Lock()
			if g.query == q {
				g.results = out
			}
			g.mu.Unlock()
			x.h.Redraw()
		}()
	}
	return registry.PickerSource{ID: "grep", Title: "Grep", Order: 31, Live: true, PickerOnly: true,
		Items: func(_ registry.Ctx, q string) []registry.PickerItem {
			run(q)
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.results
		},
		Preview: func(it registry.PickerItem) ui.Node { return x.filePreview(it.Path, it.Line-1) },
	}
}

// searchFiles is every line of files that re matches, as picker items, up to maxGrep.
func searchFiles(ctx context.Context, dir string, files []string, re *regex.Regex) []registry.PickerItem {
	var out []registry.PickerItem
	for _, f := range files {
		if ctx.Err() != nil || len(out) >= maxGrep {
			break
		}
		fh, err := os.Open(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for n := 1; sc.Scan(); n++ {
			b := sc.Bytes()
			if n == 1 && strings.ContainsRune(string(b[:min(len(b), 512)]), 0) {
				break // binary
			}
			loc, err := re.FindIndex(string(b), 0)
			if loc == nil || err != nil {
				continue
			}
			text := strings.TrimSpace(string(b))
			out = append(out, registry.PickerItem{ID: fmt.Sprintf("%s:%d", f, n), Label: text, Detail: fmt.Sprintf("%s:%d", f, n),
				IconStyle: "status.working", Path: f, Line: n, Col: loc[0] + 1, Boost: -len(out)})
			if len(out) >= maxGrep {
				break
			}
		}
		fh.Close()
	}
	return out
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
			file, line := splitLoc(k.Source)
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
