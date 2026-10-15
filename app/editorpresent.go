package app

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

// windowPresent is an editor window a presenter draws, and where that presentation is.
type windowPresent struct {
	name string
	st   *presentState
}

// windowID is a window by its id, the current one for "".
func (x *editorPlugin) windowID(s string) (vim.WindowID, error) {
	if s == "" {
		return x.e.WindowID(), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("no editor window %q", s)
	}
	if _, ok := x.e.Pane(vim.WindowID(n)); !ok {
		return 0, fmt.Errorf("no editor window %q", s)
	}
	return vim.WindowID(n), nil
}

// path is a window's file, absolute.
func (x *editorPlugin) path(p vim.Pane) string {
	path := p.Name()
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(x.dir, path)
	}
	return path
}

// presentIn shows window with the presenter name, or as text again when it shows it already.
func (x *editorPlugin) presentIn(window, name string) error {
	id, err := x.windowID(window)
	if err != nil {
		return err
	}
	p, _ := x.e.Pane(id)
	if wp := x.present[id]; wp != nil && (name == "" || wp.name == name) {
		delete(x.present, id)
		return nil
	}
	if name == "" {
		return nil
	}
	pr, ok := registry.PresenterNamed(x.h, name)
	if !ok {
		return fmt.Errorf("no presenter %q", name)
	}
	if !presents(pr, x.path(p)) {
		return fmt.Errorf("%s doesn't present %s", name, p.Name())
	}
	st := newPresentState()
	st.top, st.focus = p.Top(), p.Current()
	x.present[id] = &windowPresent{name: name, st: st}
	return nil
}

// presents reports whether pr takes path.
func presents(pr registry.BufferPresenter, path string) bool {
	ok := pr.When == nil
	if !ok {
		registry.Call(func() error { ok = pr.When(path); return nil })
	}
	return ok
}

// presenting is the presenter drawing window id, if one is and it still takes the window's file.
func (x *editorPlugin) presenting(p vim.Pane) (*windowPresent, registry.BufferPresenter, bool) {
	wp := x.present[p.ID()]
	if wp == nil {
		return nil, registry.BufferPresenter{}, false
	}
	pr, ok := registry.PresenterNamed(x.h, wp.name)
	if !ok || !presents(pr, x.path(p)) {
		return nil, registry.BufferPresenter{}, false
	}
	return wp, pr, true
}

// presentPane draws a window as its presenter does, keeping the window's top line with what's at the top of it.
func (x *editorPlugin) presentPane(c *ui.Ctx, p vim.Pane, wp *windowPresent, pr registry.BufferPresenter, focused bool, width float32) {
	id := p.ID()
	root := c.Next("present")
	c.Column("present", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		top, moved := drawPresentation(c, x.h, x.sh, wp.st, presentation{presenter: pr, path: x.path(p), name: p.Name(), buf: p.Buffer(),
			window: strconv.Itoa(int(id)), kicker: filepath.Base(p.Name()), focused: focused, root: root,
			source: func(line int) {
				delete(x.present, id)
				x.e.EnterWindow(id)
				x.e.SetCursor(vim.Pos{Line: line})
			}}, width)
		if moved {
			x.e.ScrollTo(id, top)
		}
	})
}
