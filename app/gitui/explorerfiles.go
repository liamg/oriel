package gitui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Explorer's file actions: new file and folder, rename, duplicate, delete, copy path and reveal, as commands and
// on a row's right-click menu.

const filesHelp = "files/explorer#files"

// fileAsk is the name the Explorer's dialog asks for: a new file's or folder's in dir, or path's new one.
type fileAsk struct {
	kind string // "file", "folder" or "rename"
	path string
	name string
	err  string
	busy bool // saving, off the loop
}

// pathOf is a row's path from what it stands for: absolute, a folder's ending in "/".
func pathOf(subject string) string { return strings.TrimPrefix(subject, kit.PathSubject) }

// subject is what the row for rel stands for to its right-click menu.
func (e *Explorer) subject(rel string) string {
	if e.git().root() == "" {
		return ""
	}
	p := e.git().abs(rel)
	if strings.HasSuffix(rel, "/") {
		p += string(filepath.Separator)
	}
	return kit.PathSubject + p
}

// readOnly says why path can't be changed: it's in a project the session only reads.
func (e *Explorer) readOnly(path string) string {
	se, ok := e.h.State().Session(e.session())
	if !ok {
		return ""
	}
	for _, p := range se.Snapshot().Projects {
		if p.ReadOnly && kit.Within(path, p.Dir) {
			return "the session only reads it"
		}
	}
	return ""
}

// absPath is a command's {path}: as given if absolute, else under the Explorer's repo.
func (e *Explorer) absPath(a registry.Args) (string, error) {
	p := strings.TrimSpace(a.String("path"))
	switch {
	case p == "" && e.git().root() == "":
		return "", errors.New("which path? {path}")
	case p == "":
		return e.git().root(), nil
	case !filepath.IsAbs(p):
		p = filepath.Join(e.git().root(), p)
	}
	return filepath.Clean(p), nil
}

// dirOf is the folder a new file goes in for path: path itself if it's a folder, else the one it's in.
func dirOf(path string) string {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return path
	}
	return filepath.Dir(path)
}

func (e *Explorer) fileCommands() []registry.Command {
	h := e.h
	editable := func(a registry.Args) (string, error) {
		p, err := e.absPath(a)
		if err != nil {
			return "", err
		}
		if why := e.readOnly(p); why != "" {
			return "", fmt.Errorf("%s is read-only: %s", filepath.Base(p), why)
		}
		return p, nil
	}
	newEntry := func(kind string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, a registry.Args) error {
			p, err := editable(a)
			if err != nil {
				return err
			}
			dir := dirOf(p)
			name := strings.TrimSpace(a.String("name"))
			if name == "" {
				e.ask = &fileAsk{kind: kind, path: dir}
				return nil
			}
			return e.create(ctx, kind, dir, name)
		}
	}
	return []registry.Command{
		{ID: "explorer.new-file", Title: "New file {name} in the folder {path} (a file's own folder for a file); with no {name}, ask for it",
			Run: newEntry("file")},
		{ID: "explorer.new-folder", Title: "New folder {name} in the folder {path} (a file's own folder for a file); with no {name}, ask for it",
			Run: newEntry("folder")},
		{ID: "explorer.rename", Title: "Rename the file or folder {path} to {name}, in the same folder; with no {name}, ask for it",
			Run: func(_ registry.Ctx, a registry.Args) error {
				p, err := editable(a)
				if err != nil {
					return err
				}
				name := strings.TrimSpace(a.String("name"))
				if name == "" {
					e.ask = &fileAsk{kind: "rename", path: p, name: filepath.Base(p)}
					return nil
				}
				return e.rename(p, name)
			}},
		{ID: "explorer.duplicate", Title: "Copy the file or folder {path} beside it, as \"name copy\"", Run: func(_ registry.Ctx, a registry.Args) error {
			p, err := editable(a)
			if err != nil {
				return err
			}
			return duplicate(p)
		}},
		{ID: "explorer.delete", Title: "Move the file or folder {path} to the trash once {confirm}ed; {permanent} deletes it instead",
			Run: func(_ registry.Ctx, a registry.Args) error {
				p, err := editable(a)
				if err != nil {
					return err
				}
				return e.remove(p, a.Bool("confirm"), a.Bool("permanent"))
			}},
		{ID: "explorer.copy-path", Title: "Copy the path of {path}: absolute, or with {relative} from its repo's root", Run: func(_ registry.Ctx, a registry.Args) error {
			p, err := e.absPath(a)
			if err != nil {
				return err
			}
			if a.Bool("relative") {
				if r, err := filepath.Rel(e.git().root(), p); err == nil && !strings.HasPrefix(r, "..") {
					p = filepath.ToSlash(r)
				}
			}
			kit.CopyText(h, p)
			return nil
		}},
	}
}

// fileMenu is a row's right-click menu items for the file actions.
func (e *Explorer) fileMenu() []registry.MenuItem {
	when := registry.MenuWhen{View: kit.ExplorerView, Subject: kit.PathSubject}
	ro := func(at registry.MenuCtx) string { return e.readOnly(pathOf(at.Subject)) }
	item := func(name, label, group string, order int, command string, args registry.Args, disabled func(registry.MenuCtx) string) registry.MenuItem {
		return registry.MenuItem{Name: name, Label: label, Group: group, Order: order, When: when, Source: "builtin:explorer", Disabled: disabled,
			Run: func(ctx registry.Ctx, at registry.MenuCtx) error {
				a := registry.Args{"path": strings.TrimSuffix(pathOf(at.Subject), string(filepath.Separator))}
				for k, v := range args {
					a[k] = v
				}
				return e.h.Run(ctx, command, a)
			}}
	}
	reveal := "Reveal in file manager"
	if runtime.GOOS == "darwin" {
		reveal = "Reveal in Finder"
	}
	return []registry.MenuItem{
		item("explorer.new-file", "New file…", "files.new", 1, "explorer.new-file", nil, ro),
		item("explorer.new-folder", "New folder…", "files.new", 2, "explorer.new-folder", nil, ro),
		item("explorer.rename", "Rename…", "files.edit", 3, "explorer.rename", nil, ro),
		item("explorer.duplicate", "Duplicate", "files.edit", 4, "explorer.duplicate", nil, ro),
		item("explorer.delete", "Delete…", "files.edit", 5, "explorer.delete", nil, ro),
		item("explorer.copy-path", "Copy path", "files.path", 6, "explorer.copy-path", nil, nil),
		item("explorer.copy-relative-path", "Copy relative path", "files.path", 7, "explorer.copy-path", registry.Args{"relative": true}, nil),
		item("explorer.reveal", reveal, "files.path", 8, "system.reveal", nil, nil),
	}
}

// create makes a new file or folder name in dir, and opens a new file in the editor.
func (e *Explorer) create(ctx registry.Ctx, kind, dir, name string) error {
	p, err := makeEntry(kind, dir, name)
	if err != nil {
		return err
	}
	return e.created(ctx, kind, p)
}

// created opens a new file in the editor.
func (e *Explorer) created(ctx registry.Ctx, kind, p string) error {
	if kind == "folder" {
		return nil
	}
	return e.h.Run(ctx, "editor.open", registry.Args{"path": p})
}

// makeEntry makes a new file or folder name in dir, and answers its path.
func makeEntry(kind, dir, name string) (string, error) {
	p, err := within(dir, name)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(p); err == nil {
		return "", fmt.Errorf("%s already exists", name)
	}
	if kind == "folder" {
		return p, os.MkdirAll(p, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	return p, f.Close()
}

// within is name under dir: a relative path that stays in it.
func within(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("%q isn't a name", name)
	}
	p := filepath.Join(dir, name)
	if !kit.Within(p, dir) || p == dir {
		return "", fmt.Errorf("%q isn't in %s", name, filepath.Base(dir))
	}
	return p, nil
}

// rename gives path a new name in its folder, and says so if the editor holds it.
func (e *Explorer) rename(path, name string) error {
	to, err := moveEntry(path, name)
	if err != nil {
		return err
	}
	e.renamed(path, to)
	return nil
}

func (e *Explorer) renamed(path, to string) {
	if to != path {
		name := filepath.Base(to)
		e.stillOpen(path, "was renamed "+name, []core.Action{{Title: "Open " + name, Command: "editor.open", Args: map[string]any{"path": to}}})
	}
}

// moveEntry gives path a new name in its folder, and answers its new path.
func moveEntry(path, name string) (string, error) {
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return "", fmt.Errorf("%q has a folder in it: a rename keeps the file where it is", name)
	}
	to, err := within(filepath.Dir(path), name)
	if err != nil || to == path {
		return to, err
	}
	if _, err := os.Lstat(to); err == nil {
		return "", fmt.Errorf("%s already exists", name)
	}
	return to, os.Rename(path, to)
}

// remove asks first, then moves path to the trash; permanent deletes it instead.
func (e *Explorer) remove(path string, confirm, permanent bool) error {
	name := filepath.Base(path)
	what := "the file"
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		what = "the folder and everything in it"
	}
	if !confirm {
		title, text, action := "Delete "+name+"?", "Move "+what+" to the trash.", "Move to trash"
		if permanent {
			title, text, action = "Delete "+name+" for good?", "Delete "+what+". It can't be undone.", "Delete"
		}
		e.h.Notify(core.Notice{ID: "explorer.delete", Severity: core.SevQuestion, Confirm: true, Source: "explorer", Title: title, Text: text,
			Actions: []core.Action{
				{Title: action, Command: "explorer.delete", Args: map[string]any{"path": path, "confirm": true, "permanent": permanent}},
				{Title: "Cancel", Command: "notice.dismiss", Args: map[string]any{"id": "explorer.delete"}},
			}})
		return nil
	}
	if permanent {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	} else if err := kit.Trash(path); err != nil {
		e.h.Notify(core.Notice{ID: "explorer.delete", Severity: core.SevError, Source: "explorer", Text: err.Error(),
			Actions: []core.Action{{Title: "Delete for good…", Command: "explorer.delete", Args: map[string]any{"path": path, "permanent": true}}}})
		return nil
	}
	e.stillOpen(path, "was deleted", nil)
	return nil
}

// stillOpen says so when the editor holds the file that was at path: its buffer keeps what was there.
func (e *Explorer) stillOpen(path, what string, actions []core.Action) {
	eds, ok := registry.EditorsOf(e.h)
	if !ok {
		return
	}
	if _, ok := eds.Buffer(path); !ok {
		return
	}
	e.h.Notify(core.Notice{ID: "explorer.open." + path, Severity: core.SevInfo, Source: "explorer", Actions: actions,
		Text: filepath.Base(path) + " " + what + ": its tab still shows what it held, and saving it writes it back."})
}

// duplicate copies path beside it as "name copy", or "name copy 2" when that's taken.
func duplicate(path string) error {
	ext := filepath.Ext(path)
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		ext = ""
	}
	stem := strings.TrimSuffix(path, ext)
	to := stem + " copy" + ext
	for i := 2; ; i++ {
		if _, err := os.Lstat(to); errors.Is(err, fs.ErrNotExist) {
			break
		}
		to = fmt.Sprintf("%s copy %d%s", stem, i, ext)
	}
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(path, p)
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, dst)
		}
		return copyFile(p, dst, info.Mode().Perm())
	})
}

func copyFile(from, to string, mode fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// askView is the dialog asking for a new file's, folder's or name's name.
func (e *Explorer) askView(c *ui.Ctx) {
	f := e.ask
	if f == nil {
		return
	}
	open := true
	title := map[string]string{"file": "New file", "folder": "New folder", "rename": "Rename " + filepath.Base(f.path)}[f.kind]
	widget.Modal(c, "explorer-ask", &open, title, widget.ModalOpts{Width: 460, Help: filesHelp}, func(c *ui.Ctx) {
		th := c.Theme()
		where := "in " + kit.TildePath(f.path)
		if f.kind == "rename" {
			where = "in " + kit.TildePath(filepath.Dir(f.path))
		}
		c.Text("where", where, kit.MonoFont(11, th.C(theme.FgFaint)))
		r := widget.Input(c, "name", &f.name, widget.InputOpts{Label: "Name", Placeholder: map[bool]string{true: "name.go or dir/name.go", false: "name"}[f.kind == "file"],
			Help: filesHelp, SelectOnFocus: f.kind == "rename", Invalid: f.err != "", DefaultFocus: true})
		if f.err != "" {
			widget.Wrap(c, "err", f.err, 420, kit.UIFont(12, th.C(theme.Error)))
		}
		switch {
		case r.Cancelled:
			open = false
		case r.Submitted:
			e.submitAsk()
		}
		c.Row("buttons", ui.Style{W: ui.Flex(1), Gap: 8, Main: ui.End}, func(c *ui.Ctx) {
			if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: filesHelp}) {
				open = false
			}
			label := map[string]string{"file": "Create", "folder": "Create", "rename": "Rename"}[f.kind]
			if widget.Button(c, "ok", label, widget.ButtonOpts{Primary: true, Key: "↵", Disabled: strings.TrimSpace(f.name) == "", Help: filesHelp}) {
				e.submitAsk()
			}
		})
	})
	if !open && e.ask == f {
		e.ask = nil
	}
}

func (e *Explorer) submitAsk() {
	f := e.ask
	name := strings.TrimSpace(f.name)
	if name == "" || f.busy {
		return
	}
	f.busy, f.err = true, ""
	type done struct {
		path string
		err  error
	}
	_ = plugin.RunSlow(context.Background(), e.h, plugin.Slow{ID: "explorer.ask", Doing: "Saving " + name},
		func(context.Context) (done, error) {
			if f.kind == "rename" {
				to, err := moveEntry(f.path, name)
				return done{to, err}, nil
			}
			p, err := makeEntry(f.kind, f.path, name)
			return done{p, err}, nil
		},
		func(ctx registry.Ctx, d done) error {
			f.busy = false
			if d.err != nil {
				f.err = d.err.Error()
				return nil
			}
			if e.ask == f {
				e.ask = nil
			}
			if f.kind == "rename" {
				e.renamed(f.path, d.path)
				return nil
			}
			return e.created(ctx, f.kind, d.path)
		})
}

func (e *Explorer) askOverlay() uireg.View {
	return uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "explorer-ask", Fn: e.askView} })
}
