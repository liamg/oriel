package shell_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

// keeper keeps a line of text per directory in a StateFile.
type keeper struct {
	kit.Disposer
	files string // where each directory's file goes
	f     *kit.StateFile

	mu     sync.Mutex
	text   string
	loads  []string
	leaves int
}

func (p *keeper) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test/keeper"} }
func (p *keeper) Activate(_ context.Context, h plugin.Host) error {
	p.f = kit.NewStateFile(h, kit.StateFileOpts{Name: "keeper",
		Path: func() string {
			if d := kit.WindowDir(h); d != "" {
				return filepath.Join(p.files, filepath.Base(d)+".txt")
			}
			return ""
		},
		Load: func(b []byte) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.text = string(b)
			p.loads = append(p.loads, string(b))
		},
		Leave: func() []byte {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.leaves++
			return []byte(p.text + " left")
		}})
	p.Add(registry.DisposeFunc(p.f.Close))
	return nil
}

// waitFile waits for the file at path to hold want.
func waitFile(t *testing.T, path, want string) {
	t.Helper()
	apptest.Poll(t, fmt.Sprintf("%s to hold %q", path, want), func() bool {
		got, _ := os.ReadFile(path)
		return string(got) == want
	})
}

func (p *keeper) loaded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.loads...)
}

// A state file per directory is read off the loop as the window starts and moves, its own writes aren't handed back
// as changes, something else's are, and the directory left keeps what Leave says.
func TestAStateFileFollowsTheWindow(t *testing.T) {
	files, a, b := t.TempDir(), filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, d := range []string{a, b} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(files, "a.txt"), []byte("kept in a"), 0o644)
	os.WriteFile(filepath.Join(files, "b.txt"), []byte("kept in b"), 0o644)
	p := &keeper{files: files}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: a, Plugins: []plugin.Plugin{p}})
	r.WaitFor("a's file loaded", func() bool { return strings.Join(p.loaded(), "|") == "kept in a" })

	r.Do(func() error { p.text = "written"; p.f.Write([]byte("written")); return nil })
	waitFile(t, filepath.Join(files, "a.txt"), "written")
	os.WriteFile(filepath.Join(files, "a.txt"), []byte("edited by hand"), 0o644)
	r.WaitFor("the edit loaded, the write not", func() bool { return strings.Join(p.loaded(), "|") == "kept in a|edited by hand" })

	if err := r.Do(func() error { w, _ := kit.WindowOf(r.Host); return w.Reopen(b, "") }); err != nil {
		t.Fatal(err)
	}
	r.WaitFor("b's file loaded", func() bool { l := p.loaded(); return len(l) == 3 && l[2] == "kept in b" })
	waitFile(t, filepath.Join(files, "a.txt"), "edited by hand left")
	r.WaitGone("Opening")
}

// Straight back on the directory it just left, the window reads what Leave wrote for it, not what was there before.
func TestAStateFileBackOnADirectoryReadsItsWrite(t *testing.T) {
	files, a, b := t.TempDir(), filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, d := range []string{a, b} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(files, "a.txt"), []byte("kept in a"), 0o644)
	p := &keeper{files: files}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: a, Plugins: []plugin.Plugin{p}})
	r.WaitFor("a's file loaded", func() bool { return strings.Join(p.loaded(), "|") == "kept in a" })
	if err := r.Do(func() error {
		w, _ := kit.WindowOf(r.Host)
		if err := w.Reopen(b, ""); err != nil {
			return err
		}
		return w.Reopen(a, "")
	}); err != nil {
		t.Fatal(err)
	}
	r.WaitFor("a's write loaded", func() bool {
		l := p.loaded()
		return len(l) > 1 && strings.HasPrefix(l[len(l)-1], "kept in a left")
	})
	r.WaitGone("Opening")
}

// plainView is a view with instances of its own that it can't keep in a file.
type plainView struct{ kit.Disposer }

func (p *plainView) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test/plain"} }
func (p *plainView) Activate(_ context.Context, h plugin.Host) error {
	p.Add(h.View("test/plain", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("plain") })))
	return nil
}

// Back on a directory, the window has its views as it left them, kept as they are rather than read back from its
// file: one that can't be kept in a file is there too.
func TestBackOnADirectoryKeepsItsViews(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, d := range []string{a, b} {
		os.MkdirAll(d, 0o755)
	}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: a, Plugins: []plugin.Plugin{&plainView{}}})
	r.WaitGone("Opening")
	if err := r.Do(func() error {
		return r.Host.Run(context.Background(), "view.open", registry.Args{"id": "test/plain", "instance": "one", "place": "tab"})
	}); err != nil {
		t.Fatal(err)
	}
	plain := func() int { // on the loop
		n := 0
		for _, v := range r.Host.Layout().All() {
			if v.ID == "test/plain" && v.Instance == "one" {
				n++
			}
		}
		return n
	}
	r.WaitFor("the view", func() bool { return plain() == 1 })
	move := func(dir string) {
		t.Helper()
		if err := r.Do(func() error { w, _ := kit.WindowOf(r.Host); return w.Reopen(dir, "") }); err != nil {
			t.Fatal(err)
		}
		r.WaitFor("the window on "+dir, func() bool { return kit.WindowDir(r.Host) == dir })
		r.WaitGone("Opening")
	}
	move(b)
	if r.Do(func() error {
		if plain() != 0 {
			return errors.New("b has a's view")
		}
		return nil
	}) != nil {
		t.Fatal("b has a's view")
	}
	move(a)
	r.WaitFor("a's view back", func() bool { return plain() == 1 })
}

// Back on a directory, Load has what was kept for it at once, then the file as something else changed it meanwhile.
func TestAStateFileBackOnADirectoryReadsAnEditSince(t *testing.T) {
	files, a, b := t.TempDir(), filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	for _, d := range []string{a, b} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(files, "a.txt"), []byte("kept in a"), 0o644)
	p := &keeper{files: files}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: a, Plugins: []plugin.Plugin{p}})
	r.WaitFor("a's file loaded", func() bool { return strings.Join(p.loaded(), "|") == "kept in a" })
	move := func(dir string) {
		t.Helper()
		if err := r.Do(func() error { w, _ := kit.WindowOf(r.Host); return w.Reopen(dir, "") }); err != nil {
			t.Fatal(err)
		}
		r.WaitFor("the window on "+dir, func() bool { return kit.WindowDir(r.Host) == dir })
	}
	move(b)
	waitFile(t, filepath.Join(files, "a.txt"), "kept in a left")
	os.WriteFile(filepath.Join(files, "a.txt"), []byte("edited by hand"), 0o644)
	move(a)
	r.WaitFor("the edit loaded", func() bool { l := p.loaded(); return l[len(l)-1] == "edited by hand" })
}
