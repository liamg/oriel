package agentui

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
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
func (x *Requests) hiddenIn(window, path string) bool {
	if v, ok := x.shown[window]; ok && filepath.Clean(v.path) == filepath.Clean(path) {
		return v.hidden
	}
	v, _, err := x.h.Options().Get("editor.threads", registry.At{})
	return err == nil && v == "hidden"
}

// window is an editor window with a file: {window}, or the focused one.
func (x *Requests) window(a registry.Args) (registry.EditorWindow, error) {
	eds, ok := services.EditorsOf(x.h)
	if !ok {
		return registry.EditorWindow{}, errors.New("no editor")
	}
	w, ok := eds.Window(a.String("window"))
	if !ok {
		return registry.EditorWindow{}, fmt.Errorf("no editor window %q", a.String("window"))
	}
	if w.Path == "" {
		return registry.EditorWindow{}, errors.New("the window has no file")
	}
	return w, nil
}

// toggleThreads shows or hides a window's threads.
func (x *Requests) toggleThreads(a registry.Args) error {
	w, err := x.window(a)
	if err != nil {
		return err
	}
	x.shown[w.ID] = threadsShown{path: w.Path, hidden: !x.hiddenIn(w.ID, w.Path)}
	return nil
}

// showThreads shows a window's threads, scrolled to {thread} (or the cursor line's) with its reply field ready.
func (x *Requests) showThreads(a registry.Args) error {
	w, err := x.window(a)
	if err != nil {
		return err
	}
	id := core.ThreadID(a.String("thread"))
	if id == "" {
		id = x.bubbles[w.ID][w.Cursor.Line]
	}
	x.shown[w.ID] = threadsShown{path: w.Path, hidden: false}
	x.reveal, x.focusReply = id, id
	return nil
}

// fileThreads is how many open threads the session in front has in path.
func (x *Requests) fileThreads(path string) int {
	se, ok := x.front()
	if !ok {
		return 0
	}
	n := 0
	for _, t := range se.Threads {
		if t.Anchor != nil && t.Open() && !t.Btw && filepath.Clean(kit.AnchorPath(se, t.Anchor)) == filepath.Clean(path) {
			n++
		}
	}
	return n
}

// threadsAction is the 💬 n editor action: the file's threads, lit while they show.
func (x *Requests) threadsAction() registry.EditorAction {
	return registry.EditorAction{Name: "threads", Icon: "comment", Command: "threads.toggle", Order: 90, Help: "editor/pane#threads",
		When: func(path string) bool { return x.fileThreads(path) > 0 },
		On:   func(w registry.EditorWindow) bool { return !x.hiddenIn(w.ID, w.Path) },
		Note: func(w registry.EditorWindow) string { return strconv.Itoa(x.fileThreads(w.Path)) }}
}

// hiddenLayer is active while the focused editor window hides its threads and its cursor is on a line with some.
func (x *Requests) hiddenLayer() registry.KeyLayer {
	return registry.KeyLayer{Name: "threads.hidden", Mode: ThreadsHiddenKeyMode, Active: func() bool {
		eds, ok := services.EditorsOf(x.h)
		f := x.h.Layout().Focus
		if !ok || f == nil || f.ID != kit.EditorView {
			return false
		}
		w, ok := eds.Window("")
		if !ok || eds.Mode() != "normal" || w.Presenter != "" || w.ID != f.Instance {
			return false
		}
		_, ok = x.bubbles[f.Instance][w.Cursor.Line]
		return ok && x.hiddenIn(f.Instance, w.Path)
	}}
}

// bubble is a line's speech bubble while its threads are hidden: in the colour of the first, with their count when
// there are several; hovering it shows them, a click shows them in the window.
func (x *Requests) bubble(se core.SessionState, window string, line int, ts []core.Thread) uireg.Decoration {
	d := uireg.Decoration{Kind: uireg.Sign, Line: line, Icon: "comment", Style: groupTone(x.h, se, ts[0]).Name(), Priority: 20, Command: "threads.show",
		Args: registry.Args{"window": window, "thread": string(ts[0].ID)}, Tooltip: fmt.Sprintf("%d %s", len(ts), kit.Plural(len(ts), "thread", "threads"))}
	if len(ts) > 1 {
		d.Text = strconv.Itoa(len(ts))
	}
	d.Hover = &ui.Func{Key: "threads.pop", Fn: func(c *ui.Ctx) { threadsPopover(c, x.h, se, line, ts) }}
	return d
}

// threadsPopover is a line's threads, read-only: each one's title and state, and its last messages.
func threadsPopover(c *ui.Ctx, h plugin.Host, se core.SessionState, line int, ts []core.Thread) {
	th := c.Theme()
	c.Column("threads", ui.Style{W: ui.Flex(1), Gap: 12, Role: ui.RoleGroup, Label: fmt.Sprintf("Threads on line %d", line+1)}, func(c *ui.Ctx) {
		for i, t := range ts {
			c.Column(i, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
				word, tok := statusLabel(h, se, t)
				c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(groupTone(h, se, t))}, nil)
					c.Text("t", "Thread", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
					c.Text("l", fmt.Sprintf("· L%d", line+1), kit.MonoFont(10.5, th.C(theme.FgFaint)))
					if t.Title != "" {
						c.Text("title", t.Title, kit.UIFont(12, th.C(theme.FgMuted)))
					}
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					widget.Chip(c, "status", word, th.C(tok), "editor/pane#threads")
				})
				from := max(len(t.Comments)-2, 0)
				for j, cm := range t.Comments[from:] {
					name := cmp.Or(cm.Name, "you")
					if cm.Author == core.AuthorAgent {
						name = kit.ProviderNameIn(c, cm.Name)
					}
					c.Column(j, ui.Style{W: ui.Flex(1), Gap: 2}, func(c *ui.Ctx) {
						c.Text("who", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 11.5, Weight: typeset.SemiBold}, Color: th.C(theme.FgMuted)})
						kit.MarkdownText(c, h, "body", cm.Body, 340, kit.UIFont(12, th.C(theme.FgMuted)), kit.ThreadMarkdown(se, t))
					})
				}
			})
		}
	})
}
