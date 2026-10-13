package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/edit/input/emacs"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui/layout"
)

// running is the app on a headless window, with the files in dir open in its editor.
type running struct {
	t     *testing.T
	win   *headless.Window
	store core.Store
	dir   string
	done  chan error
	host  ext.Host
	once  sync.Once
}

// stop closes the window and waits for the app to exit; it's safe to call again.
func (r *running) stop() {
	r.once.Do(func() {
		r.win.Close()
		<-r.done
	})
}

// grabHost hands a test its host. It runs commands only through keymaps, on the app's loop.
type grabHost struct {
	h     ext.Host
	keys  []registry.Keymap
	setup func(ext.Host)
}

func (g *grabHost) Manifest() ext.Manifest { return ext.Manifest{ID: "test.grab"} }
func (g *grabHost) Activate(_ context.Context, h ext.Host) error {
	g.h = h
	for _, k := range g.keys {
		h.Keymap(k)
	}
	if g.setup != nil {
		g.setup(h)
	}
	return nil
}
func (g *grabHost) Deactivate(context.Context) error { return nil }

func runEditor(t *testing.T, files map[string]string, open []string, keys ...registry.Keymap) *running {
	t.Helper()
	return runEditorWith(t, files, open, nil, keys...)
}

func runEditorWith(t *testing.T, files map[string]string, open []string, exts []ext.Extension, keys ...registry.Keymap) *running {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	be := headless.New()
	g := &grabHost{keys: keys}
	r := &running{t: t, store: eventstore.New(), dir: dir, done: make(chan error, 1)}
	cfg := Config{Store: r.store, ConfigDir: t.TempDir(), WorkDir: dir, Files: open, Extensions: append(exts, g)}
	go func() { r.done <- Run(ctx, be, cfg) }()
	select {
	case r.win = <-be.Windows():
	case err := <-r.done:
		t.Fatalf("app exited: %v", err)
	}
	r.host = g.h
	t.Cleanup(func() {
		r.win.Close()
		<-r.done
	})
	return r
}

// typeKeys sends keys in vim notation, one key press each.
func (r *running) typeKeys(keys string) {
	for _, k := range keymap.Parse(keys) {
		r.win.Send(fromNotation(k))
	}
}

func (r *running) waitFrame(what string, ok func(string) bool) string {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f, err := r.win.WaitFrame(ctx, func(f headless.Frame) bool { return ok(f.Text()) })
	if err != nil {
		fs := r.win.Frames()
		last := ""
		if len(fs) > 0 {
			last = fs[len(fs)-1].Text()
		}
		var ns []string
		for _, n := range r.store.State().Notifications {
			ns = append(ns, n.ID+": "+n.Text)
		}
		r.t.Fatalf("no frame with %s (%d frames); notices %q; last frame:\n%s", what, len(fs), ns, last)
	}
	return f.Text()
}

func (r *running) waitText(s string) string {
	r.t.Helper()
	return r.waitFrame(`"`+s+`"`, func(t string) bool { return strings.Contains(t, s) })
}

func (r *running) waitFile(name, want string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		got, _ = os.ReadFile(filepath.Join(r.dir, name))
		if string(got) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("%s = %q, want %q", name, got, want)
}

func (r *running) waitNotice(id string) core.Notification {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range r.store.State().Notifications {
			if n.ID == id {
				return n
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("no notice %s: %+v", id, r.store.State().Notifications)
	return core.Notification{}
}

// ED-12: a file opens, takes keys, and :w writes it.
func TestEditorOpensEditsAndWrites(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\r\ntwo\r\n"}, []string{"a.txt"})
	r.waitText("two")
	r.typeKeys("ddGofour<Esc>")
	r.waitText("four")
	r.typeKeys(":w<CR>")
	r.waitFile("a.txt", "two\r\nfour\r\n")
	r.waitNotice("vim.written")
}

// errors.md: the engine's notices are the IDE's. :q on changes is vim.unsaved, an error notice, where nvim gives E37.
func TestEditorQuitWithChangesNotifies(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\n"}, []string{"a.txt"})
	r.waitText("one")
	r.typeKeys("x:q<CR>")
	n := r.waitNotice("vim.unsaved")
	if n.Severity != core.SevError || n.Source != "vim" {
		t.Errorf("notice = %+v", n)
	}
	r.typeKeys("u:q<CR>")
	r.waitFrame("the editor closed", func(s string) bool { return !strings.Contains(s, "NORMAL") })
}

// "yy in the editor puts the line on the system clipboard, and p pastes what another app copied."
func TestEditorClipboard(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\ntwo\n"}, []string{"a.txt"})
	r.waitText("two")
	r.typeKeys("yy")
	deadline := time.Now().Add(5 * time.Second)
	for r.win.Clipboard() != "one\n" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.win.Clipboard() != "one\n" {
		t.Fatalf("clipboard = %q", r.win.Clipboard())
	}
	r.win.SetClipboard("pasted")
	r.typeKeys("jp:w<CR>")
	r.waitFile("a.txt", "one\ntpastedwo\n")
}

// ":ls, :copen and :messages open the IDE's switcher, results panel and notification history, not text."
func TestEditorIDEViews(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\n", "b.txt": "two\n"}, []string{"a.txt", "b.txt"})
	r.waitText("two")
	r.typeKeys(":ls<CR>")
	r.waitFrame("the buffer switcher", func(s string) bool { return strings.Contains(s, "a.txt") && strings.Contains(s, "%") })
	r.typeKeys("<Esc>")
	r.waitFrame("the switcher closed", func(s string) bool { return !strings.Contains(s, "PICK") })
	r.typeKeys(":cexpr 'a.txt:1:oops'<CR>:copen<CR>")
	r.waitText("oops")
	r.typeKeys("<Esc>")
	r.waitFrame("the quickfix picker closed", func(s string) bool { return !strings.Contains(s, "PICK") })
	r.typeKeys(":messages<CR>")
	r.waitText("Copy report")
}

// "A change on disk reloads an unchanged buffer and prompts for a changed one."
func TestEditorReloadsFromDisk(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "one\n"}, []string{"a.txt"})
	r.waitText("one")
	os.WriteFile(filepath.Join(r.dir, "a.txt"), []byte("changed outside\n"), 0o644)
	r.waitText("changed outside")
	r.typeKeys("x")
	r.waitText("hanged outside")
	os.WriteFile(filepath.Join(r.dir, "a.txt"), []byte("again\n"), 0o644)
	n := r.waitNotice("editor.changed-on-disk.a.txt")
	if n.Severity != core.SevQuestion || len(n.Actions) != 2 || n.Actions[0].Command != "editor.reload" {
		t.Errorf("notice = %+v", n)
	}
}

// ":split and CTRL-W commands and a drag in FW-9's layout change the same tree."
func TestEditorWindowsAreLayoutPanes(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "alpha\n"}, []string{"a.txt"},
		registry.Keymap{Mode: "global", Keys: "<C-p>", Command: "layout.move", Args: registry.Args{"place": "split", "side": "right"}})
	r.waitText("alpha")
	r.typeKeys(":split<CR>")
	r.waitFrame("two panes", func(s string) bool { return strings.Count(s, "alpha") == 2 })
	shown := r.shown()
	if len(shown) != 2 || shown[0].ID != EditorView || shown[1].ID != EditorView {
		t.Fatalf("layout shows %v", shown)
	}
	r.typeKeys("<C-w>jx")
	r.waitFrame("the lower window edited", func(s string) bool { return strings.Count(s, "lpha") == 2 && !strings.Contains(s, "alpha") })
	r.typeKeys(":vsplit<CR>")
	r.waitFrame("three panes", func(s string) bool { return strings.Count(s, "lpha") == 3 })
	if n := len(r.shown()); n != 3 {
		t.Fatalf("after :vsplit the layout shows %d panes", n)
	}
	r.typeKeys(":close<CR>")
	r.waitFrame("two panes again", func(s string) bool { return strings.Count(s, "lpha") == 2 })
	if n := len(r.shown()); n != 2 {
		t.Fatalf("after :close the layout shows %d panes", n)
	}
}

// shown is the layout's main panes, read on the app's loop.
func (r *running) shown() []layout.ViewRef {
	r.t.Helper()
	got := make(chan []layout.ViewRef, 1)
	r.host.Command(registry.Command{ID: "test.shown", Run: func(registry.Ctx, registry.Args) error {
		got <- r.host.Layout().Shown()
		return nil
	}})
	r.host.Keymap(registry.Keymap{Mode: "global", Keys: "<F12>", Command: "test.shown"})
	r.win.Send(render.KeyEvent{Key: "F12"})
	select {
	case s := <-got:
		return s
	case <-time.After(5 * time.Second):
		r.t.Fatal("the layout never answered")
	}
	return nil
}

// ED-5: "an engine swap at runtime per buffer; a sample emacs plugin passes its tests".
func TestEditorInputEnginePerBuffer(t *testing.T) {
	r := runEditorWith(t, map[string]string{"a.txt": "alpha\n", "b.txt": "bravo\n"}, []string{"a.txt", "b.txt"},
		[]ext.Extension{&emacs.Plugin{}},
		registry.Keymap{Mode: "global", Keys: "<F2>", Command: "editor.input", Args: registry.Args{"engine": "plain"}},
		registry.Keymap{Mode: "global", Keys: "<F3>", Command: "editor.input", Args: registry.Args{"engine": "emacs"}},
		registry.Keymap{Mode: "global", Keys: "<F4>", Command: "editor.input", Args: registry.Args{"engine": "vim"}},
		registry.Keymap{Mode: "global", Keys: "<F5>", Command: "editor.open", Args: registry.Args{"path": "a.txt"}})
	r.waitText("bravo")
	r.typeKeys("<F2>")
	r.waitText("EDIT")
	r.typeKeys("<D-a>plain<D-s>")
	r.waitFile("b.txt", "plain\n")
	r.typeKeys("<F5>")
	r.waitText("alpha")
	r.waitText("NORMAL")
	r.typeKeys("x:w<CR>")
	r.waitFile("a.txt", "lpha\n")
	r.typeKeys("<F3>")
	r.waitText("EMACS")
	r.typeKeys("<C-k>gone<C-x><C-s>")
	r.waitFile("a.txt", "gone\n")
	r.typeKeys("<F4>")
	r.waitText("NORMAL")
}

// SH-4: ⌘K, a query and Enter open what it found.
func TestOmniboxOpensAFile(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "alpha\n", "deep/tiers.go": "package deep\n\nfunc PriceForTier() {}\n"}, []string{"a.txt"})
	r.waitText("alpha")
	r.typeKeys("<D-k>")
	r.waitText("SESSIONS")
	r.typeKeys("@tiers<CR>")
	r.waitFrame("tiers.go open", func(s string) bool { return strings.Contains(s, "PriceForTier") && !strings.Contains(s, "FILES") })
}

// SH-14: every built-in view opened in the layout closes from the keyboard with ⌃W c.
func TestEveryViewClosesWithCtrlWC(t *testing.T) {
	r := runEditor(t, map[string]string{"a.txt": "alpha\n"}, nil)
	var views []string
	for _, id := range r.host.Views() {
		if !strings.HasPrefix(id, "app.") {
			views = append(views, id)
		}
	}
	for _, id := range views {
		t.Run(id, func(t *testing.T) {
			if err := r.do(func() error { return r.host.Run(context.Background(), "view.open", registry.Args{"id": id}) }); err != nil {
				t.Skipf("doesn't open on its own: %v", err)
			}
			if len(r.shown()) == 0 {
				t.Skip("opens nothing in the layout")
			}
			r.typeKeys("<C-w>c")
			deadline := time.Now().Add(5 * time.Second)
			for len(r.shown()) > 0 {
				if time.Now().After(deadline) {
					t.Fatalf("%s is still open after ⌃W c: %v", id, r.shown())
				}
			}
		})
	}
}

// do runs fn on the app's loop and returns its error.
func (r *running) do(fn func() error) error {
	r.t.Helper()
	got := make(chan error, 1)
	r.host.Do(func() { got <- fn() })
	select {
	case err := <-got:
		return err
	case <-time.After(5 * time.Second):
		r.t.Fatal("the loop never ran it")
	}
	return nil
}
