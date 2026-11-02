package kit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// DirMax is the most of a folder's entries a DirTree shows; one more row counts the rest.
const DirMax = 500

// DirMore marks the row standing for what a folder holds past DirMax, or before it's read.
const DirMore = "\x00more"

// DirEntry is a folder's entry.
type DirEntry struct {
	Name string
	Dir  bool
	Size int64 // -1 when it couldn't be read
}

// DirTree is folders browsed as a tree, each read off the loop the first time it shows, and again when it changes on
// disk. Its methods are called on the loop.
type DirTree struct {
	h       plugin.Host
	cache   *plugin.Cache[string, []DirEntry]
	entries map[string][]DirEntry // the folders read so far
	wanted  map[string]bool       // folders being read
	reads   int                   // moves each time entries does
	built   struct {
		root, prefix string
		reads        int
		th           *theme.Resolved
		items        []widget.TreeItem
	}
}

// NewDirTree is a DirTree; Close stops its reads and watches.
func NewDirTree(h plugin.Host) *DirTree {
	d := &DirTree{h: h, entries: map[string][]DirEntry{}, wanted: map[string]bool{}}
	d.cache = plugin.NewCache(h, func(_ context.Context, dir string) ([]DirEntry, error) { return ReadDirEntries(dir), nil },
		plugin.CacheOpts[string, []DirEntry]{MaxEntries: 64, Path: func(dir string) string { return dir }})
	return d
}

func (d *DirTree) Close() { d.cache.Close() }

// Items are root's entries as tree items, folders first, each id prefix and its path; a folder not read yet holds one
// placeholder, so it shows its caret. Loading says root itself is still being read.
func (d *DirTree) Items(root, prefix string) (items []widget.TreeItem, loading bool) {
	for dir := range d.wanted {
		d.read(dir)
	}
	for dir, es := range d.entries {
		if now, ls := d.cache.Get(dir); ls.Phase == ui.Ready && !slices.Equal(now, es) {
			d.entries[dir] = now
			d.reads++
		}
	}
	th := d.h.CurrentTheme()
	b := &d.built
	if b.items == nil || b.root != root || b.prefix != prefix || b.reads != d.reads || b.th != th {
		b.root, b.prefix, b.th = root, prefix, th
		b.items = d.items(root, prefix, 0)
		b.reads = d.reads
	}
	return b.items, d.wanted[root]
}

// Opened reads the folder of a row the tree's Toggled said opened.
func (d *DirTree) Opened(id, prefix string) {
	if p, ok := strings.CutPrefix(id, prefix); ok && p != "" {
		d.read(p)
	}
}

// Path is a row's file, or "" for a row that isn't one (the count of the rest).
func (d *DirTree) Path(id, prefix string) string {
	p, ok := strings.CutPrefix(id, prefix)
	if !ok || strings.HasSuffix(p, DirMore) {
		return ""
	}
	return p
}

// Refresh reads every folder shown again.
func (d *DirTree) Refresh() {
	for dir := range d.entries {
		d.cache.Invalidate(dir)
	}
	clear(d.entries)
	d.reads++
}

func (d *DirTree) items(dir, prefix string, depth int) []widget.TreeItem {
	es, ok := d.entries[dir]
	if !ok {
		if depth > 0 {
			return []widget.TreeItem{{ID: prefix + dir + DirMore, Label: "…"}}
		}
		es = d.read(dir)
	}
	th := d.h.CurrentTheme()
	var out []widget.TreeItem
	for i, e := range es {
		if i == DirMax {
			out = append(out, widget.TreeItem{ID: prefix + dir + DirMore, Label: fmt.Sprintf("%d more", len(es)-DirMax)})
			break
		}
		p := filepath.Join(dir, e.Name)
		it := widget.TreeItem{ID: prefix + p, Label: e.Name}
		if e.Dir {
			it.Children = d.items(p, prefix, depth+1)
			if len(it.Children) == 0 {
				it.Note = "empty"
			}
		} else if e.Size >= 0 {
			it.Note = Bytes(e.Size)
		}
		if th != nil && strings.HasPrefix(e.Name, ".") {
			it.Tone = th.C(theme.FgFaint)
		}
		out = append(out, it)
	}
	return out
}

// read takes dir's entries once they're read, off the loop; until then it's wanted.
func (d *DirTree) read(dir string) []DirEntry {
	es, ls := d.cache.Get(dir)
	if ls.Phase != ui.Ready && ls.Phase != ui.Failed {
		d.wanted[dir] = true
		return nil
	}
	delete(d.wanted, dir)
	d.entries[dir] = es
	d.reads++
	return es
}

// ReadDirEntries is dir's entries, folders first, the sizes of the first DirMax with them.
func ReadDirEntries(dir string) []DirEntry {
	des, _ := os.ReadDir(dir)
	slices.SortStableFunc(des, func(a, b os.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})
	es := make([]DirEntry, len(des))
	for i, de := range des {
		es[i] = DirEntry{Name: de.Name(), Dir: de.IsDir(), Size: -1}
		if fi, err := de.Info(); err == nil && !de.IsDir() && i < DirMax {
			es[i].Size = fi.Size()
		}
	}
	return es
}
