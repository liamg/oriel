package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

// editorView is the pane view: one instance per window.
type editorView struct{ x *Editor }

func (v editorView) EdgeToEdge() {}

func (v editorView) Build(vc uireg.ViewCtx) ui.Node {
	n, err := strconv.Atoi(vc.Instance)
	return &ui.Func{Key: "editor", Fn: func(c *ui.Ctx) {
		if err != nil {
			c.Text("bad", "no editor window "+vc.Instance, ui.TextStyle{})
			return
		}
		v.x.paneView(c, vim.WindowID(n), vc.Focused)
	}}
}

// Location is the file in a window, and whether the window has a Visual selection, for the right-click menu.
func (v editorView) Location(instance string) (string, bool) {
	n, err := strconv.Atoi(instance)
	if err != nil {
		return "", false
	}
	p, ok := v.x.e.Pane(vim.WindowID(n))
	if !ok || p.Name() == "" {
		return "", false
	}
	path := p.Name()
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.x.dir, path)
	}
	return path, p.Current() && strings.HasPrefix(v.x.e.Mode(), "visual")
}

// editorState is an editor window as the layout keeps it across restarts: its file and cursor.
type editorState struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Present string `json:"present,omitempty"` // the presenter drawing it
}

func (v editorView) Persist(instance string) (json.RawMessage, bool) {
	path, _ := v.Location(instance)
	n, err := strconv.Atoi(instance)
	if path == "" || err != nil {
		return nil, false
	}
	p, ok := v.x.e.Pane(vim.WindowID(n))
	if !ok {
		return nil, false
	}
	c := p.Cursor()
	es := editorState{Path: path, Line: c.Line, Col: c.Col}
	if wp := v.x.present[p.ID()]; wp != nil {
		es.Present = wp.name
	}
	b, _ := json.Marshal(es)
	return b, true
}

// Revive opens the file again in a new window, at its cursor.
func (v editorView) Revive(state json.RawMessage) (string, error) {
	var s editorState
	if err := json.Unmarshal(state, &s); err != nil {
		return "", err
	}
	if _, err := os.Stat(s.Path); err != nil {
		return "", err
	}
	id, err := v.x.window(v.x.relName(s.Path))
	if err != nil {
		return "", err
	}
	v.x.e.EnterWindow(id)
	v.x.e.SetCursor(vim.Pos{Line: s.Line, Col: s.Col})
	v.x.conv.refresh()
	if s.Present != "" {
		if st, err := v.x.newPresent(s.Line); err == nil {
			v.x.present[id] = &windowPresent{name: s.Present, st: st}
		}
	}
	return strconv.Itoa(int(id)), nil
}

// Mark is the window's file's git status letter, for its tab.
func (v editorView) Mark(instance string) (string, string) {
	path, _ := v.Location(instance)
	if path == "" {
		return "", ""
	}
	t := kit.GitOf(v.x.h).Tree("")
	f, ok := t.State().File(t.Rel(path))
	if !ok || f.Code == 0 {
		return "", ""
	}
	letter, _ := kit.GitCodeTokens(f.Code)
	return string(f.Code), letter.Name()
}

func (v editorView) Title(instance string) string {
	n, err := strconv.Atoi(instance)
	if err != nil {
		return ""
	}
	p, ok := v.x.e.Pane(vim.WindowID(n))
	if !ok {
		return ""
	}
	name := filepath.Base(p.Name())
	if p.Name() == "" {
		name = "[No Name]"
	}
	if p.Modified() {
		name += " ●"
	}
	return name
}
