package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

// ThreadsHiddenKeyMode is a key layer while the cursor is on a line whose threads are hidden: ⏎ shows them.
const ThreadsHiddenKeyMode = "threads.hidden"

// threadsShown is whether a window shows its file's threads, set for the file it had then.
type threadsShown struct {
	path   string
	hidden bool
}

// threadsOptions are the threads' options.
func threadsOptions() []registry.OptionSpec {
	return []registry.OptionSpec{{Name: "editor.threads", Type: registry.Enum, Values: []string{"shown", "hidden"}, Default: "shown",
		Scopes: registry.ScopeUser | registry.ScopeProject,
		Help:   "How files open: with their threads between the lines (shown), or behind speech bubbles in the gutter (hidden)."}}
}

// hiddenIn reports whether window hides path's threads: as threads.toggle set it there, else as editor.threads says.
func (x *requests) hiddenIn(window, path string) bool {
	if v, ok := x.shown[window]; ok && filepath.Clean(v.path) == filepath.Clean(path) {
		return v.hidden
	}
	v, _, err := x.h.Options().Get("editor.threads", registry.At{})
	return err == nil && v == "hidden"
}

// window is an editor window and its file: {window}, or the focused one.
func (x *requests) window(a registry.Args) (string, vim.Pane, error) {
	ed := x.sh.editor
	if ed == nil || ed.e == nil {
		return "", vim.Pane{}, errors.New("no editor")
	}
	id, err := ed.windowID(a.String("window"))
	if err != nil {
		return "", vim.Pane{}, err
	}
	p, _ := ed.e.Pane(id)
	if p.Name() == "" {
		return "", vim.Pane{}, errors.New("the window has no file")
	}
	return strconv.Itoa(int(id)), p, nil
}

// toggleThreads shows or hides a window's threads.
func (x *requests) toggleThreads(a registry.Args) error {
	w, p, err := x.window(a)
	if err != nil {
		return err
	}
	path := x.sh.editor.path(p)
	x.shown[w] = threadsShown{path: path, hidden: !x.hiddenIn(w, path)}
	return nil
}

// showThreads shows a window's threads, scrolled to {thread} (or the cursor line's) with its reply field ready.
func (x *requests) showThreads(a registry.Args) error {
	w, p, err := x.window(a)
	if err != nil {
		return err
	}
	id := core.ThreadID(a.String("thread"))
	if id == "" {
		id = x.bubbles[w][p.Cursor().Line]
	}
	x.shown[w] = threadsShown{path: x.sh.editor.path(p), hidden: false}
	x.reveal, x.focusReply = id, id
	return nil
}

// fileThreads is how many open threads the session in front has in path.
func (x *requests) fileThreads(path string) int {
	se, ok := x.front()
	if !ok {
		return 0
	}
	n := 0
	for _, t := range se.Threads {
		if t.Anchor != nil && t.Open() && !t.Btw && filepath.Clean(anchorPath(se, t.Anchor)) == filepath.Clean(path) {
			n++
		}
	}
	return n
}

// threadsAction is the 💬 n editor action: the file's threads, lit while they show.
func (x *requests) threadsAction() registry.EditorAction {
	return registry.EditorAction{Name: "threads", Icon: "comment", Command: "threads.toggle", Order: 90, Help: "editor/pane#threads",
		When: func(path string) bool { return x.fileThreads(path) > 0 },
		On:   func(w registry.EditorWindow) bool { return !x.hiddenIn(w.ID, w.Path) },
		Note: func(w registry.EditorWindow) string { return strconv.Itoa(x.fileThreads(w.Path)) }}
}

// hiddenLayer is active while the focused editor window hides its threads and its cursor is on a line with some.
func (x *requests) hiddenLayer() registry.KeyLayer {
	return registry.KeyLayer{Name: "threads.hidden", Mode: ThreadsHiddenKeyMode, Active: func() bool {
		ed := x.sh.editor
		f := x.h.Layout().Focus
		if ed == nil || ed.e == nil || f == nil || f.ID != EditorView || ed.e.Mode() != "normal" {
			return false
		}
		p := ed.current()
		if wp, _, ok := ed.presenting(p); ok || wp != nil || strconv.Itoa(int(p.ID())) != f.Instance {
			return false
		}
		_, ok := x.bubbles[f.Instance][p.Cursor().Line]
		return ok && x.hiddenIn(f.Instance, ed.path(p))
	}}
}

// bubble is a line's speech bubble while its threads are hidden: in the colour of the first, with their count when
// there are several; hovering it shows them, a click shows them in the window.
func (x *requests) bubble(se core.SessionState, window string, line int, ts []core.Thread) registry.Decoration {
	d := registry.Decoration{Kind: registry.Sign, Line: line, Icon: "comment", Style: groupTone(se, ts[0]).Name(), Priority: 20, Command: "threads.show",
		Args: registry.Args{"window": window, "thread": string(ts[0].ID)}, Tooltip: fmt.Sprintf("%d %s", len(ts), plural(len(ts), "thread", "threads"))}
	if len(ts) > 1 {
		d.Text = strconv.Itoa(len(ts))
	}
	d.Hover = &ui.Func{Key: "threads.pop", Fn: func(c *ui.Ctx) { threadsPopover(c, se, line, ts) }}
	return d
}

// threadsPopover is a line's threads, read-only: each one's title and state, and its last messages.
func threadsPopover(c *ui.Ctx, se core.SessionState, line int, ts []core.Thread) {
	th := c.Theme()
	c.Column("threads", ui.Style{W: ui.Flex(1), Gap: 12, Role: ui.RoleGroup, Label: fmt.Sprintf("Threads on line %d", line+1)}, func(c *ui.Ctx) {
		for i, t := range ts {
			c.Column(i, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
				word, tok := statusLabel(se, t)
				c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(groupTone(se, t))}, nil)
					c.Text("t", "Thread", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
					c.Text("l", fmt.Sprintf("· L%d", line+1), monoFont(10.5, th.C(theme.FgFaint)))
					if t.Title != "" {
						c.Text("title", truncate(t.Title, 32), uiFont(12, th.C(theme.FgMuted)))
					}
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					widget.Chip(c, "status", word, th.C(tok), "editor/pane#threads")
				})
				from := max(len(t.Comments)-2, 0)
				for j, cm := range t.Comments[from:] {
					name := cmpOr(cm.Name, "you")
					if cm.Author == core.AuthorAgent {
						name = providerName(cm.Name)
					}
					c.Column(j, ui.Style{W: ui.Flex(1), Gap: 2}, func(c *ui.Ctx) {
						c.Text("who", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 11.5, Weight: typeset.SemiBold}, Color: th.C(theme.FgMuted)})
						markdownText(c, "body", cm.Body, 340, uiFont(12, th.C(theme.FgMuted)))
					})
				}
			})
		}
	})
}
