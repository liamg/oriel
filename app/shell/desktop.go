package shell

import (
	"cmp"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Desktop is what Oriel asks of the OS: the clipboard, the browser and the file manager.
type Desktop interface {
	SetClipboard(text string)
	ReadClipboard() (string, bool)
	OpenURL(url string) error
	Reveal(path string) error // shows path in the file manager: selected in Finder, its folder elsewhere
}

// desktopPlugin is the OS's side of the IDE as commands: system.open opens a file or URL in its default app, system.copy
// copies text.
type desktopPlugin struct {
	kit.Disposer
	desktop *Desktop // the window's, once it's open
}

func (x *desktopPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "desktop"} }

func (x *desktopPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Command(registry.Command{ID: "system.open", Title: "Open a file {path} (relative to the work dir) or a {url} in the OS's default app for it",
		Run: func(_ registry.Ctx, a registry.Args) error {
			target := cmp.Or(a.String("url"), a.String("path"))
			if target == "" {
				return errors.New("system.open needs a path or a url")
			}
			if a.String("url") == "" && !filepath.IsAbs(target) {
				target = filepath.Join(h.Here().Project, target)
			}
			if *x.desktop == nil {
				return errors.New("there's no desktop to open it on")
			}
			return (*x.desktop).OpenURL(target)
		}}))
	x.Add(h.Command(registry.Command{ID: "system.reveal", Title: "Show a file or folder {path} in the file manager: Finder on macOS, its folder elsewhere",
		Run: func(_ registry.Ctx, a registry.Args) error {
			p := a.String("path")
			if p == "" {
				return errors.New("system.reveal needs a path")
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(h.Here().Project, p)
			}
			if *x.desktop == nil {
				return errors.New("there's no file manager to show it in")
			}
			return (*x.desktop).Reveal(p)
		}}))
	x.Add(h.Command(registry.Command{ID: "system.copy", Title: "Copy {text} to the system clipboard",
		Run: func(_ registry.Ctx, a registry.Args) error {
			if *x.desktop == nil {
				return errors.New("there's no clipboard to copy to")
			}
			(*x.desktop).SetClipboard(a.String("text"))
			return nil
		}}))
	return nil
}

type osDesktop struct{ clip render.Clipboard }

func windowDesktop(w render.Window) Desktop {
	c, _ := w.(render.Clipboard)
	return osDesktop{c}
}

func (d osDesktop) SetClipboard(text string) {
	if d.clip != nil {
		d.clip.SetClipboard(text)
	}
}

func (d osDesktop) ReadClipboard() (string, bool) {
	if d.clip == nil {
		return "", false
	}
	return d.clip.ReadClipboard()
}

func (d osDesktop) ReadClipboardFiles() []string {
	if f, ok := d.clip.(render.ClipboardFiles); ok {
		return f.ReadClipboardFiles()
	}
	return nil
}

func (d osDesktop) ReadClipboardImage() ([]byte, bool) {
	if f, ok := d.clip.(render.ClipboardFiles); ok {
		return f.ReadClipboardImage()
	}
	return nil, false
}

func (d osDesktop) Reveal(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		path = filepath.Dir(path)
	}
	return d.OpenURL(path)
}

func (d osDesktop) OpenURL(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, url).Start()
}

// desktopClipboard is the system clipboard as the editor's registers see it. Platforms without a primary
// selection use the clipboard for "* too.
type desktopClipboard struct{ d *Desktop }

func (c desktopClipboard) Read(bool) (string, bool) {
	if *c.d == nil {
		return "", false
	}
	return (*c.d).ReadClipboard()
}

func (c desktopClipboard) Write(_ bool, s string) {
	if *c.d != nil {
		(*c.d).SetClipboard(s)
	}
}

// dropped is files dropped on the window: to the node under them that takes files, else opened in the editor.
func (sh *shell) dropped(ev render.DropEvent) {
	if u := sh.ui.Load(); u != nil && u.DropFiles(ev.Pos, ev.Paths) {
		return
	}
	for _, p := range ev.Paths {
		if err := sh.cfg.Registry.Run(context.Background(), "editor.open", registry.Args{"path": p}); err != nil {
			sh.store.Apply(core.Notified{Notice: core.Notice{ID: "drop.open", Severity: core.SevWarning, Source: "core", Text: err.Error(), Time: sh.now()}})
		}
	}
}
