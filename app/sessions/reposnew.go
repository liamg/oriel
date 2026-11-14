package sessions

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The Add repo dialog: an existing directory, or a clone of a URL into a folder you can
// see before it's made.

var repoNewModes = []string{"Existing directory", "Clone from URL"}

// probeWait is how long a field rests before what it names is checked; probeShow, how long a check runs before it
// says so.
const (
	probeWait = 250 * time.Millisecond
	probeShow = 150 * time.Millisecond
)

type addRepo struct {
	mode                    int // into repoNewModes
	path, url, parent, name string
	note                    string // why it's asked, over the fields
	urlName                 string // the name the URL gave: the name field follows the URL while it holds this
	then                    []kit.Call
	focus                   bool

	key      string    // what the fields held when they last changed
	keyAt    time.Time // when
	probed   string    // the key last checked
	probing  bool
	probeAt  time.Time // when the check running started
	check    host.RepoCheck
	target   host.CloneTarget
	probeErr string

	busy     bool
	since    time.Time // when the add started
	progress string
	cancel   context.CancelFunc
	err      string
}

// expandTilde expands a leading ~.
func expandTilde(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func (x *Repos) newDialog(a registry.Args) error {
	then, err := kit.CallsOf(a["then"])
	if err != nil {
		return err
	}
	f := &addRepo{path: a.String("path"), url: a.String("url"), parent: a.String("parent"), name: a.String("name"), note: a.String("note"), then: then, focus: true}
	if a.String("mode") == "clone" || f.url != "" {
		f.mode = 1
	}
	if w, _ := kit.RepoCalls(x.h); f.parent == "" && w != nil {
		f.parent = kit.TildePath(w.CloneParent())
	}
	if f.name == "" {
		f.name = host.CloneName(f.url)
		f.urlName = f.name
	}
	if x.dialog != nil && x.dialog.cancel != nil {
		x.dialog.cancel()
	}
	x.dialog = f
	return nil
}

func (x *Repos) closeDialog() {
	if f := x.dialog; f != nil && f.cancel != nil {
		f.cancel()
	}
	x.dialog = nil
}

func (x *Repos) dialogView(c *ui.Ctx) {
	f := x.dialog
	if f == nil {
		return
	}
	open := true
	widget.Modal(c, "reposnew", &open, widget.ModalOpts{Title: "Add repo", Width: 620, Help: "agent/repos#add"}, func(c *ui.Ctx) { x.dialogBody(c, f) })
	if open {
		return
	}
	if f.busy && f.cancel != nil {
		f.cancel() // Escape stops a clone; it takes away what it made
		return
	}
	x.dialog = nil
}

// dialogField is a text field of the dialog's: Tab moves on, Escape closes, ⌘↵ adds.
func (x *Repos) dialogField(c *ui.Ctx, key string, text *string, label, placeholder string, focus bool, o widget.InputOpts) {
	if focus {
		c.RequestFocus(c.Next(key))
	}
	o.Label, o.Placeholder, o.Help, o.TabMoves, o.KeyMode = label, placeholder, "agent/repos#add", true, RepoNewKeyMode
	if r := widget.Input(c, key, text, o); r.Cancelled {
		x.closeDialog()
	} else if r.Submitted {
		x.submitDialog()
	}
}

// probeKey is what the fields name that needs checking.
func (f *addRepo) probeKey() string {
	if f.mode == 0 {
		return "dir\x00" + strings.TrimSpace(f.path)
	}
	return strings.Join([]string{"clone", strings.TrimSpace(f.url), strings.TrimSpace(f.parent), strings.TrimSpace(f.name)}, "\x00")
}

// probe checks what the fields name, once they've rested.
func (x *Repos) probe(c *ui.Ctx, f *addRepo) {
	now := c.Now()
	if n := host.CloneName(f.url); f.mode == 1 && f.name == f.urlName && n != f.urlName {
		f.name, f.urlName = n, n
	}
	if k := f.probeKey(); k != f.key {
		f.key, f.keyAt = k, now
	}
	w, _ := kit.RepoCalls(x.h)
	if w == nil || f.probing || f.key == f.probed {
		return
	}
	if wait := f.keyAt.Add(probeWait); now.Before(wait) {
		c.WakeAt(wait)
		return
	}
	key, mode, path := f.key, f.mode, expandTilde(f.path)
	req := host.CloneReq{URL: f.url, Parent: expandTilde(f.parent), Name: f.name}
	if mode == 0 && strings.TrimSpace(f.path) == "" || mode == 1 && strings.TrimSpace(f.url) == "" {
		f.probed, f.check, f.target, f.probeErr = key, host.RepoCheck{}, host.CloneTarget{}, ""
		return
	}
	f.probing, f.probeAt = true, now
	plugin.Go(x.h, "repos: the add dialog's check", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var (
			check  host.RepoCheck
			target host.CloneTarget
			err    error
		)
		if mode == 0 {
			check = w.CheckRepoPath(ctx, path)
		} else {
			target, err = w.CloneTarget(ctx, req)
		}
		x.h.Do(func() {
			f.probing, f.probed = false, key
			f.check, f.target, f.probeErr = check, target, ""
			if err != nil {
				f.probeErr = err.Error()
			}
		})
		x.h.Redraw()
	})
}

func (x *Repos) dialogBody(c *ui.Ctx, f *addRepo) {
	th := c.Theme()
	focus := f.focus
	f.focus = false
	if f.note != "" {
		widget.Wrap(c, "note", f.note, 580, kit.UIFont(12.5, th.C(theme.FgMuted)))
	}
	if widget.Segmented(c, "mode", repoNewModes, &f.mode, widget.SegmentedOpts{Label: "Add from", Help: "agent/repos#add"}) {
		f.err, focus = "", true
	}
	x.probe(c, f)
	if f.mode == 0 {
		x.dirMode(c, f, focus)
	} else {
		x.cloneMode(c, f, focus)
	}
	widget.Rule(c, "rule")
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		phase, frac, parsed := gitx.ParseProgress(f.progress)
		switch {
		case f.err != "":
			c.Text("err", kit.FirstLine(f.err), kit.MonoFont(11, th.C(theme.Error)))
		case f.busy && parsed:
			widget.ProgressBar(c, "progress", widget.ProgressOpts{Frac: frac, Height: 6, Label: phase, Help: "agent/repos#add"})
		case f.busy && f.mode == 1:
			widget.Busy(c, "progress", widget.LoadState{Phase: widget.Pending, Since: f.since}, widget.BusyOpts{Label: "Cloning", Help: "agent/repos#add"})
		case f.busy:
			widget.Busy(c, "progress", widget.LoadState{Phase: widget.Pending, Since: f.since}, widget.BusyOpts{Label: "Adding", Help: "agent/repos#add"})
		case f.probing && c.Now().Sub(f.probeAt) >= probeShow:
			label := "Checking the path"
			if f.mode == 1 {
				label = "Checking the URL"
			}
			widget.Busy(c, "checking", widget.LoadState{Phase: widget.Pending, Since: f.probeAt}, widget.BusyOpts{Label: label, Help: "agent/repos#add"})
		case f.probing:
			c.WakeAt(f.probeAt.Add(probeShow))
		case f.mode == 1:
			kit.Note(c, "creds", "clones with this machine's git credentials")
		}
		if !f.busy || !parsed {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		}
		cancel := "Cancel"
		if f.busy && f.mode == 1 {
			cancel = "Stop"
		}
		if widget.Button(c, "cancel", cancel, widget.ButtonOpts{Key: "esc", Help: "agent/repos#add"}) {
			if f.busy && f.cancel != nil {
				f.cancel()
			} else {
				x.closeDialog()
				return
			}
		}
		label, busyLabel := "Add", "Adding"
		if f.mode == 1 {
			label, busyLabel = "Clone", "Cloning"
		}
		if f.probing && !f.busy {
			busyLabel = "Checking"
		}
		if widget.Button(c, "submit", label, widget.ButtonOpts{Primary: true, Key: "⌘↵", Busy: f.busy || f.probing, BusyLabel: busyLabel,
			Disabled: f.blocked() != "", Help: "agent/repos#add"}) {
			x.submitDialog()
		}
	})
}

// dirMode is the Existing directory form: a path, completed as Open Directory does, and what it would add.
func (x *Repos) dirMode(c *ui.Ctx, f *addRepo, focus bool) {
	th := c.Theme()
	c.Column("dir", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		kit.Heading(c, "h", "Directory", "a checkout, or a worktree of one")
		c.Row("row", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			x.dialogField(c, "path", &f.path, "Directory", "~/src/my-project", focus, widget.InputOpts{})
			if kit.ReadsIn(c).HasChooseDir() && widget.Button(c, "browse", "Browse…", widget.ButtonOpts{Help: "agent/repos#add"}) {
				x.work.Loop(func(ctx context.Context) {
					dir, err := kit.ChooseDir(ctx)
					x.h.Do(func() {
						if err == nil && dir != "" {
							f.path = kit.TildePath(strings.TrimRight(dir, "/"))
						}
					})
					x.h.Redraw()
				})
			}
		})
		x.completions(c, f)
		ok, bad := th.C(theme.OK), th.C(theme.Error)
		switch ch := f.check; {
		case strings.TrimSpace(f.path) == "" || f.checking():
		case ch.Error != "":
			c.Text("check", "✗ "+ch.Error, kit.MonoFont(11, bad))
		case ch.Known != "":
			c.Text("check", "✓ already added as "+string(ch.Known), kit.MonoFont(11, ok))
		case ch.Worktree:
			c.Text("check", "✓ a worktree of "+kit.TildePath(ch.Path)+" — adds "+kit.TildePath(ch.Path), kit.MonoFont(11, ok))
		default:
			c.Text("check", "✓ git repo · "+strings.Join(nonEmpty(ch.Branch, remoteHost(ch.Remote)), " · "), kit.MonoFont(11, ok))
		}
	})
}

// completions are the directories the typed path could go on to, as Open Directory offers them.
func (x *Repos) completions(c *ui.Ctx, f *addRepo) {
	q := strings.TrimSpace(f.path)
	if q == "" || !f.checking() && f.check.Error == "" {
		return
	}
	home, _ := os.UserHomeDir()
	dirs := kit.ReadsIn(c).TypedDirs(q, home)
	if len(dirs) > 0 && dirs[0] == expandTilde(q) {
		dirs = dirs[1:]
	}
	if len(dirs) == 0 {
		return
	}
	dirs = dirs[:min(len(dirs), 5)]
	sel := -1
	res := widget.Rows(c, "completions", len(dirs), &sel, widget.RowsOpts{Label: "Directories", Help: "agent/repos#add",
		RowLabel: func(i int) string { return dirs[i] }}, func(c *ui.Ctx, i int, _ bool) {
		c.Row("r", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 3)}, func(c *ui.Ctx) {
			c.Text("t", kit.TildePath(dirs[i])+"/", kit.MonoFont(11, c.Theme().C(theme.FgMuted)))
		})
	})
	if res.Activated >= 0 {
		f.path = kit.TildePath(dirs[res.Activated]) + "/"
		f.focus = true
	}
}

// cloneMode is the Clone from URL form: the URL, the folder it goes in and its name there, and where that makes it.
func (x *Repos) cloneMode(c *ui.Ctx, f *addRepo, focus bool) {
	th := c.Theme()
	labelled := func(c *ui.Ctx, key, label string, body func(c *ui.Ctx)) {
		c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("l", ui.Style{W: ui.Px(110)}, func(c *ui.Ctx) { c.Text("t", label, kit.UIFont(12.5, th.C(theme.FgMuted))) })
			body(c)
		})
	}
	c.Column("clone", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		labelled(c, "url", "URL", func(c *ui.Ctx) {
			x.dialogField(c, "url", &f.url, "URL", "https://github.com/owner/repo", focus, widget.InputOpts{})
		})
		labelled(c, "parent", "Parent folder", func(c *ui.Ctx) {
			x.dialogField(c, "parent", &f.parent, "Parent folder", "~/src", false, widget.InputOpts{})
			if kit.ReadsIn(c).HasChooseDir() && widget.Button(c, "browse", "Browse…", widget.ButtonOpts{Help: "agent/repos#add"}) {
				x.work.Loop(func(ctx context.Context) {
					dir, err := kit.ChooseDir(ctx)
					x.h.Do(func() {
						if err == nil && dir != "" {
							f.parent = kit.TildePath(strings.TrimRight(dir, "/"))
						}
					})
					x.h.Redraw()
				})
			}
		})
		if w, _ := kit.RepoCalls(x.h); w != nil && expandTilde(f.parent) == w.CloneParent() && len(x.repos()) > 0 {
			labelled(c, "where", "", func(c *ui.Ctx) { kit.Note(c, "t", "where most of your repos are") })
		}
		if h := f.parentHint(); h != "" {
			labelled(c, "hint", "", func(c *ui.Ctx) { c.Text("t", h, kit.MonoFont(10.5, th.C(theme.Warn))) })
		}
		labelled(c, "name", "Folder name", func(c *ui.Ctx) {
			x.dialogField(c, "name", &f.name, "Folder name", "my-project", false, widget.InputOpts{})
		})
		into := ""
		if p, n := expandTilde(f.parent), host.Sanitise(f.name); p != "" && n != "" {
			into = kit.TildePath(filepath.Join(p, n))
		}
		labelled(c, "into", "Clones into", func(c *ui.Ctx) {
			c.Text("t", cmp.Or(into, "—"), kit.MonoFont(12, th.C(theme.Fg)))
		})
		labelled(c, "status", "", func(c *ui.Ctx) {
			st, tone := f.cloneStatus()
			if st != "" {
				c.Text("t", st, kit.MonoFont(10.5, th.C(tone)))
			}
		})
	})
}

// parentHint is a word about a parent folder that looks wrong: a URL, or a path that already ends in the name.
func (f *addRepo) parentHint() string {
	p := strings.TrimSpace(f.parent)
	switch {
	case strings.Contains(p, "://") || strings.HasPrefix(p, "git@"):
		return "that looks like a URL: it goes in the URL field"
	case p != "" && f.name != "" && filepath.Base(expandTilde(p)) == host.Sanitise(f.name):
		return "the folder name is added to this: it would clone into " + kit.TildePath(filepath.Join(expandTilde(p), host.Sanitise(f.name)))
	}
	return ""
}

func (f *addRepo) cloneStatus() (string, theme.TokenID) {
	switch {
	case strings.TrimSpace(f.url) == "" || f.checking():
		return "", theme.FgFaint
	case f.probeErr != "":
		return "✗ " + f.probeErr, theme.Error
	case f.target.Same:
		return "already a clone of this URL: it's added as it is", theme.OK
	case f.target.Exists:
		return "✗ already exists and isn't a clone of this URL: pick another name", theme.Error
	}
	return "new folder", theme.FgFaint
}

// checking reports whether what the fields name hasn't been checked yet.
func (f *addRepo) checking() bool { return f.probing || f.probed != f.probeKey() }

// blocked is why the dialog can't add what it holds yet, or "".
func (f *addRepo) blocked() string {
	if f.mode == 0 && strings.TrimSpace(f.path) != "" && f.checking() {
		return "a moment: checking the path"
	}
	if f.mode == 1 && strings.TrimSpace(f.url) != "" && f.checking() {
		return "a moment: checking the URL"
	}
	if f.mode == 0 {
		switch {
		case strings.TrimSpace(f.path) == "":
			return "pick a directory"
		case !f.checking() && f.check.Error != "":
			return f.check.Error
		}
		return ""
	}
	switch {
	case strings.TrimSpace(f.url) == "":
		return "paste the URL"
	case !f.checking() && (f.probeErr != "" || f.target.Exists && !f.target.Same):
		return "pick another name"
	}
	return ""
}

func (x *Repos) submitDialog() {
	f := x.dialog
	w, _ := kit.RepoCalls(x.h)
	if f == nil || f.busy || w == nil {
		return
	}
	if b := f.blocked(); b != "" {
		f.err = "To add: " + b + "."
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.busy, f.err, f.progress, f.cancel, f.since = true, "", "", cancel, x.h.UI().Now()
	mode, path := f.mode, expandTilde(f.path)
	req := host.CloneReq{URL: strings.TrimSpace(f.url), Parent: expandTilde(f.parent), Name: f.name,
		Progress: func(line string) {
			x.h.Do(func() { f.progress = line })
			x.h.Redraw()
		}}
	plugin.Go(x.h, "repos: add "+cmp.Or(req.URL, path), func() {
		defer cancel()
		var (
			r   core.Repo
			err error
		)
		if mode == 0 {
			r, err = w.AddRepoPath(ctx, path)
		} else {
			r, err = w.CloneRepo(ctx, req)
		}
		if errors.Is(ctx.Err(), context.Canceled) && err != nil {
			err = errors.New("stopped: nothing was cloned")
		}
		x.h.Do(func() {
			f.busy, f.cancel = false, nil
			if err != nil {
				f.err = err.Error()
				return
			}
			if x.dialog == f {
				x.dialog = nil
			}
			if f.then == nil && x.open {
				x.sel = r.ID
			}
			if err := x.then(context.Background(), f.then, r.ID); err != nil {
				x.err = err.Error()
				x.h.Log().Error("repos.new", "err", err)
			}
		})
		x.h.Redraw()
	})
}

// remoteHost is a remote URL as the dialog shows it: github.com/liamg/oriel.
func remoteHost(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	} else if at := strings.Index(u, "@"); at >= 0 {
		u = strings.Replace(u[at+1:], ":", "/", 1)
	}
	if at := strings.Index(u, "@"); at >= 0 {
		u = u[at+1:]
	}
	return u
}

// nonEmpty is the strings that aren't "".
func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
