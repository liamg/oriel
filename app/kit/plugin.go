package kit

import (
	"context"
	"encoding/base64"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// OnLoop runs fn on the app's loop and waits for it: an agent tool's way in from the session host's goroutine.
func OnLoop(ctx context.Context, h plugin.Host, fn func()) error {
	done := make(chan struct{})
	h.Do(func() { fn(); close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Disposer is the usual way for a plugin to undo what it registered.
type Disposer []registry.Disposable

func (d *Disposer) Add(x registry.Disposable) { *d = append(*d, x) }

// Lifetime is a context that ends when d is disposed: for what a plugin waits on off the loop.
func (d *Disposer) Lifetime() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	d.Add(registry.DisposeFunc(cancel))
	return ctx
}

func (d *Disposer) Deactivate(context.Context) error {
	for _, x := range *d {
		x.Dispose()
	}
	*d = nil
	return nil
}

// PagePlace is a page shown while *open, as a place in the window's history.
func PagePlace(name, title, command string, open *bool) uireg.Place {
	return uireg.Place{Name: name, Title: title, Command: command, Shown: func() bool { return *open }, Close: func() { *open = false }}
}

// WhileShown holds what a node wants for as long as it's drawn with the window in focus: its build calls keep each
// frame, and a frame drawn without it, or with the window behind another app, lets go.
type WhileShown struct {
	Want  func() (release func())
	Focus interface {
		WindowFocus() (bool, <-chan struct{})
	} // the window, whose focus it's kept with
	release func()
	kept    bool // since the last frame
}

// Keep is called from the build of a node that wants it.
func (w *WhileShown) Keep() {
	if on, _ := w.Focus.WindowFocus(); !on {
		w.Drop()
		return
	}
	w.kept = true
	if w.release == nil {
		w.release = w.Want()
	}
}

// EndFrame lets go if no build kept it this frame.
func (w *WhileShown) EndFrame() {
	if !w.kept {
		w.Drop()
	}
	w.kept = false
}

func (w *WhileShown) Drop() {
	if w.release != nil {
		w.release()
		w.release = nil
	}
}

// OpenURL opens url in the OS's default app for it (system.open).
func OpenURL(h plugin.Host, url string) error {
	return h.Run(context.Background(), "system.open", registry.Args{"url": url})
}

// CopyImage puts a picture, as PNG, on the system clipboard (system.copy).
func CopyImage(h plugin.Host, png []byte) error {
	return h.Run(context.Background(), "system.copy", registry.Args{"image": base64.StdEncoding.EncodeToString(png)})
}

// CopyText puts s on the system clipboard (system.copy).
func CopyText(h plugin.Host, s string) {
	if err := h.Run(context.Background(), "system.copy", registry.Args{"text": s}); err != nil {
		h.Log().Warn("copy", "err", err)
	}
}
