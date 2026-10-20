package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// previewHead is a preview's heading: a chip, then a title.
func previewHead(c *ui.Ctx, chip string, tone ui.TextStyle, title string) {
	th := c.Theme()
	if chip != "" {
		widget.Chip(c, "chip", chip, tone.Color, "shell/omnibox")
	}
	c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 17, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
}

// previewField is a label and its value, as the preview lists them.
func previewField(c *ui.Ctx, label, value string) {
	th := c.Theme()
	c.Row(label, ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		c.Box("l", ui.Style{W: ui.Px(88)}, func(c *ui.Ctx) {
			c.Text(nil, strings.ToUpper(label), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Tracking: .12}, Color: th.C(theme.FgFaint)})
		})
		c.Text("v", value, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: th.C(theme.Fg)})
	})
}

// sessionsSource is the sessions, the ones that need you first.
func sessionsSource(h plugin.Host) registry.PickerSource {
	boost := map[core.Status]int{core.StatusNeedsYou: 40, core.StatusPermission: 30, core.StatusReviewer: 10}
	return registry.PickerSource{ID: "sessions", Title: "Sessions", Order: 10,
		Key: func() any { return h.State().Seq },
		Items: func(registry.Ctx, string) []registry.PickerItem {
			var out []registry.PickerItem
			tabs := map[core.SessionID]int{}
			for i, s := range openTabs(h.State()) {
				tabs[s.ID()] = i
			}
			for _, s := range h.Sessions() {
				it := registry.PickerItem{ID: string(s.ID()), Label: s.Name(), Detail: string(s.Kind()), Note: statusWord(s),
					IconStyle: "status." + string(s.Status()), Boost: boost[s.Status()], Data: s}
				if i, ok := tabs[s.ID()]; ok && i < 9 {
					it.Key = fmt.Sprintf("⌘%d", i+1)
				}
				out = append(out, it)
			}
			return out
		},
		Preview: func(it registry.PickerItem) ui.Node {
			s := it.Data.(core.Session)
			return &ui.Func{Key: "session", Fn: func(c *ui.Ctx) {
				th := c.Theme()
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					tone := ui.TextStyle{Color: th.C(statusTokens[s.Status()])}
					previewHead(c, strings.ReplaceAll(string(s.Status()), "_", " "), tone, s.Name())
					previewField(c, "kind", string(s.Kind()))
					previewField(c, "session", string(s.ID()))
					if n := s.Unseen(); n > 0 {
						previewField(c, "errors", fmt.Sprintf("%d unseen", n))
					}
				})
			}}
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			return h.Run(ctx, "session.focus", registry.Args{"id": it.ID})
		},
	}
}

// commandsSource is every command, by its title, with the keys bound to it.
func commandsSource(h plugin.Host) registry.PickerSource {
	keysFor := func(id string) []registry.Keymap {
		var out []registry.Keymap
		for _, k := range h.Keymaps() {
			if k.Command == id {
				out = append(out, k)
			}
		}
		return out
	}
	return registry.PickerSource{ID: "commands", Title: "Commands", Prefix: ">", Order: 20,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			keys := map[string]string{}
			for _, k := range h.Keymaps() {
				if _, ok := keys[k.Command]; !ok {
					keys[k.Command] = k.Keys
				}
			}
			var out []registry.PickerItem
			for _, c := range h.Commands() {
				if !h.Available(c.ID, nil) {
					continue
				}
				label := c.Title
				if label == "" {
					label = c.ID
				}
				it := registry.PickerItem{ID: c.ID, Label: label, Detail: c.ID, Icon: "›", Data: c}
				it.Key = keys[c.ID]
				out = append(out, it)
			}
			return out
		},
		Preview: func(it registry.PickerItem) ui.Node {
			cmd := it.Data.(registry.Command)
			return &ui.Func{Key: "command", Fn: func(c *ui.Ctx) {
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					previewHead(c, "command", ui.TextStyle{Color: c.Theme().C(theme.Accent)}, it.Label)
					previewField(c, "id", cmd.ID)
					previewField(c, "source", cmd.Source)
					for i, k := range keysFor(cmd.ID) {
						previewField(c, fmt.Sprint("keys ", i+1), k.Mode+" "+k.Keys)
					}
				})
			}}
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error { return h.Run(ctx, it.ID, nil) },
	}
}

// fileList is the files under a directory, walked off the loop: the first walk's files show as they're found, and
// a list older than fileFresh is walked again in the background, the old one shown until the new is done.
type fileList struct {
	dir    string
	redraw func()

	mu      sync.Mutex
	at      time.Time // when the last walk finished
	files   []string
	walking bool
	done    bool // a walk has finished
	busy    sync.WaitGroup
}

// skipDirs are directories the files source doesn't walk into.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true}

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
		l.busy.Add(1)
		go l.walk(!l.done)
	}
	return l.files, !l.done
}

func (l *fileList) loading() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.done
}

func (l *fileList) walk(first bool) {
	defer l.busy.Done()
	shown := time.Now()
	found := walkFiles(l.dir, func(found []string) {
		if first && time.Since(shown) > 100*time.Millisecond {
			shown = time.Now()
			l.mu.Lock()
			l.files = slices.Clone(found)
			l.mu.Unlock()
			l.redraw()
		}
	})
	l.mu.Lock()
	l.files, l.at, l.done, l.walking = found, time.Now(), true, false
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
			if p != dir && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
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
func (x *editorPlugin) filesSource() registry.PickerSource {
	files := &fileList{dir: x.dir, redraw: x.h.Redraw}
	x.add(disposeFn(files.busy.Wait))
	openNames := func() []string {
		var open []string
		for _, b := range x.e.BufferList() {
			open = append(open, b.Name)
		}
		return open
	}
	return registry.PickerSource{ID: "files", Title: "Files", Prefix: "@", Order: 30, Loading: files.loading,
		Key: func() any {
			list, _ := files.list()
			k := filesKey{n: len(list), st: x.sh.git.state(), open: strings.Join(openNames(), "\x00")}
			if len(list) > 0 {
				k.first = &list[0]
			}
			return k
		},
		Items: func(registry.Ctx, string) []registry.PickerItem {
			open := openNames()
			st := x.sh.git.state()
			list, _ := files.list()
			var out []registry.PickerItem
			for _, f := range list {
				it := registry.PickerItem{ID: f, Label: f, IconStyle: "status.working", Boost: -strings.Count(f, "/"), Path: f}
				var notes []string
				if fs, ok := st.File(x.sh.git.rel(f)); ok && fs.Code != 0 {
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
		Preview: func(it registry.PickerItem) ui.Node { return x.filePreview(it.ID, 0) },
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			return x.h.Run(ctx, "editor.open", registry.Args{"path": it.ID})
		},
	}
}

// filePreview is a file's text from line, highlighted.
func (x *editorPlugin) filePreview(name string, line int) ui.Node {
	return &ui.Func{Key: "file", Fn: func(c *ui.Ctx) {
		path := filepath.Join(x.dir, name)
		if x.previewed == nil || x.previewed.path != path {
			x.previewed = &previewSrc{path: path}
		}
		src := x.previewed.load(bufferOn(x, path), path, true)
		if src.missing() {
			_, err := os.Stat(path)
			c.Text("err", fmt.Sprint(err), ui.TextStyle{Color: c.Theme().C(theme.FgFaint)})
			return
		}
		f := src.around(x.h, path, line+1, 3, 30)
		f.mark, f.title = line > 0, name
		f.build(c, nil)
	}}
}

// previewSrc is the file a picker last previewed.
type previewSrc struct {
	path string
	fileSrc
}

// symbols is the outline of a buffer's text, kept until the text changes.
type symbols struct {
	version uint64
	units   []text.Unit
}

// symbolsSource is the functions, methods and types of the open buffers.
func (x *editorPlugin) symbolsSource() registry.PickerSource {
	cache := map[*text.Buffer]*symbols{}
	outline := func(name string, b *text.Buffer) []text.Unit {
		snap := b.Snapshot()
		if s, ok := cache[b]; ok && s.version == snap.Version() {
			return s.units
		}
		s := &symbols{version: snap.Version()}
		cache[b] = s
		lang, ok := languageFor(x.h, name, snap.Bytes())
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
	type sym struct {
		file string
		line int
	}
	return registry.PickerSource{ID: "symbols", Title: "Symbols", Prefix: "#", Order: 40,
		Key: func() any {
			var k strings.Builder
			for _, id := range x.e.WindowIDs() {
				if p, ok := x.e.Pane(id); ok {
					fmt.Fprintf(&k, "%s\x00%d\x00", p.Name(), p.Buf().Version())
				}
			}
			return k.String()
		},
		Items: func(registry.Ctx, string) []registry.PickerItem {
			var out []registry.PickerItem
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
					out = append(out, registry.PickerItem{ID: fmt.Sprintf("%s:%d", p.Name(), line+1), Label: u.Name,
						Detail: fmt.Sprintf("%s:%d", p.Name(), line+1), Icon: icon, IconStyle: style, Note: u.Kind,
						Path: p.Name(), Line: line + 1, Data: sym{p.Name(), line}})
				}
			}
			return dedupe(out)
		},
		Preview: func(it registry.PickerItem) ui.Node {
			s := it.Data.(sym)
			return x.filePreview(s.file, s.line)
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			s := it.Data.(sym)
			return x.h.Run(ctx, "editor.open", registry.Args{"path": s.file, "line": s.line + 1})
		},
	}
}

// dedupe drops items with an id seen before: the same buffer open in two windows.
func dedupe(items []registry.PickerItem) []registry.PickerItem {
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
