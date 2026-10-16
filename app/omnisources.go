package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
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
				if ks := keysFor(c.ID); len(ks) > 0 {
					it.Key = ks[0].Keys
				}
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

// fileList is the files under a directory, listed again when it's older than a few seconds.
type fileList struct {
	dir   string
	at    time.Time
	files []string
}

// skipDirs are directories the files source doesn't walk into.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true}

const maxFiles = 50000

func (l *fileList) list() []string {
	if time.Since(l.at) < 5*time.Second && l.files != nil {
		return l.files
	}
	l.at, l.files = time.Now(), l.files[:0]
	filepath.WalkDir(l.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != l.dir && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(l.dir, p); err == nil {
			l.files = append(l.files, filepath.ToSlash(rel))
		}
		if len(l.files) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return l.files
}

// filesSource is the files of the editor's directory; open ones and shallower paths rank higher.
func (x *editorPlugin) filesSource() registry.PickerSource {
	files := &fileList{dir: x.dir}
	return registry.PickerSource{ID: "files", Title: "Files", Prefix: "@", Order: 30,
		Items: func(registry.Ctx, string) []registry.PickerItem {
			open := map[string]bool{}
			for _, b := range x.e.BufferList() {
				open[b.Name] = true
			}
			st := x.sh.git.state()
			var out []registry.PickerItem
			for _, f := range files.list() {
				it := registry.PickerItem{ID: f, Label: f, IconStyle: "status.working", Boost: -strings.Count(f, "/"), Path: f}
				var notes []string
				if fs, ok := st.File(x.sh.git.rel(f)); ok && fs.Code != 0 {
					it.Boost += 5
					notes = append(notes, string(fs.Code))
				}
				if open[f] {
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
		src, err := os.ReadFile(path)
		if err != nil {
			c.Text("err", err.Error(), ui.TextStyle{Color: c.Theme().C(theme.FgFaint)})
			return
		}
		f := highlightAround(x.h, path, src, line+1, 3, 30)
		f.mark, f.title = line > 0, name
		f.build(c, nil)
	}}
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
