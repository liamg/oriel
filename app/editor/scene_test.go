package editor_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/vim"
)

func editorOf(s *apptest.Scene) *editor.Editor { return apptest.PluginOf[*editor.Editor](s, "editor") }
func engine(s *apptest.Scene) *vim.Engine      { return editorOf(s).Engine() }
func signsOf(s *apptest.Scene) *editor.GitSigns {
	return apptest.PluginOf[*editor.GitSigns](s, "gitsigns")
}
func searchOf(s *apptest.Scene) *editor.Search { return apptest.PluginOf[*editor.Search](s, "search") }

// The editor column of 01-ide: from the left edge of the editor to the minimap, from the tabs to the agent dock.
var editorColumn = image.Rect(294, kit.TitleH, 1150, kit.TitleH+735)

// editorScene is the editor on round.go, in a window the size of the mockup's editor column, with the cursor where
// the mockup has it: line 46, on the ':' of "case 0:".
func editorScene(t *testing.T) *apptest.Scene {
	dir := t.TempDir()
	p := filepath.Join(dir, "internal", "pricing", "round.go")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(rig.RoundGo()), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: float32(editorColumn.Dx()), Y: float32(editorColumn.Dy())}})
	s.Registry().View(kit.TitleBarView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.ColumnNode() })) // the column starts under it
	s.Registry().View(kit.RailView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.ColumnNode() }))     // and right of the rail
	s.Run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.Frame()
	s.Frame()
	keys(s, "46Gzt38zt46G$")
	waitHighlights(s)
	return s
}

// keys types into the editor's engine, a frame after each key, as the window would.
func keys(s *apptest.Scene, k string) {
	for _, key := range keymap.Parse(k) {
		engine(s).Key(key)
		s.Frame()
	}
}

// waitHighlights waits for every buffer's parse, and its parse to catch up.
func waitHighlights(s *apptest.Scene) {
	s.Frame() // asks for each buffer's parser
	x := editorOf(s)
	x.Wait()
	s.Quiet()
	x.WaitDocs()
	s.Frame()
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// drew says the frame has text.
func drew(s *apptest.Scene, text string) bool { return strings.Contains(s.Text(), text) }

// open opens name in the editor, and answers where options are read for it.
func open(s *apptest.Scene, t *testing.T, name string) registry.At {
	t.Helper()
	s.Run("editor.open", registry.Args{"path": name})
	return s.Host().Here()
}

// option is an option's value at at, and where it came from.
func option(s *apptest.Scene, t *testing.T, name string, at registry.At) (any, string) {
	t.Helper()
	v, _, err := s.Registry().Options().Get(name, at)
	if err != nil {
		t.Fatal(err)
	}
	return v, s.Registry().Options().Source(name, at)
}

// framesUntil draws frames, running the work handed to the loop, until ok; check sees each frame.
func framesUntil(s *apptest.Scene, t *testing.T, what string, check func(frame int), ok func() bool) {
	t.Helper()
	s.UntilEach(what, check, ok)
}

func signsScene(t *testing.T) (*apptest.Scene, *gittest.Repo) { return apptest.SignsScene(t) }

// waitGit waits for the window's git work off the loop.
func waitGit(s *apptest.Scene) { s.WaitGit() }

func settle(s *apptest.Scene) { s.SettleSigns() }

func cursorTo(s *apptest.Scene, line int) { s.CursorTo(line) }

func searchScene(t *testing.T) (*apptest.Scene, string) { return apptest.SearchScene(t) }

func waitSearch(s *apptest.Scene, t *testing.T, want string) string {
	t.Helper()
	return s.WaitSearch(want)
}

// threadsScene is a session on dir with open threads anchored in it: each {file, line, text, body}.
func threadsScene(t *testing.T, dir string, threads ...[4]string) *apptest.Scene {
	t.Helper()
	return threadsSceneWith(t, apptest.Options{WorkDir: dir}, threads...)
}

// threadsSceneWith is threadsScene with options of its own.
func threadsSceneWith(t *testing.T, o apptest.Options, threads ...[4]string) *apptest.Scene {
	t.Helper()
	return apptest.ThreadsScene(t, o, threads...)
}

// running is the app on a headless window, with the files in dir open in its editor.
type running struct {
	*apptest.Window
	dir string
}

func runEditor(t *testing.T, files map[string]string, open []string, keys ...registry.Keymap) *running {
	t.Helper()
	return runEditorWith(t, files, open, nil, keys...)
}

func runEditorWith(t *testing.T, files map[string]string, open []string, plugins []plugin.Plugin, keys ...registry.Keymap) *running {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w := apptest.NewWindow(t, apptest.WindowOpts{WorkDir: dir, Files: open, Plugins: plugins, Setup: func(h plugin.Host) {
		for _, k := range keys {
			h.Keymap(k)
		}
	}})
	return &running{Window: w, dir: dir}
}

func (r *running) waitFile(name, want string) {
	r.T.Helper()
	r.WaitFile(name, want)
}

func (r *running) waitNotice(id string) core.Notification {
	r.T.Helper()
	return r.WaitNotice(id)
}

// shown is the layout's main panes, read on the app's loop.
func (r *running) shown() []layout.ViewRef {
	r.T.Helper()
	got := make(chan []layout.ViewRef, 1)
	r.Host.Command(registry.Command{ID: "test.shown", Run: func(registry.Ctx, registry.Args) error {
		got <- r.Host.Layout().Shown()
		return nil
	}})
	r.Host.Keymap(registry.Keymap{Mode: "global", Keys: "<F12>", Command: "test.shown"})
	r.Win.Send(render.KeyEvent{Key: "F12"}) // behind the keys typed before it
	return apptest.Recv(r.T, "the layout", got)
}
