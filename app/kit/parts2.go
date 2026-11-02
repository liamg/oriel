package kit

import (
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Section is one of the side's blocks: a kicker with a note at its right, then body.
func Section(c *ui.Ctx, key, title, note string, body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 9, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", strings.ToUpper(title), KickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if note != "" {
				c.Text("n", note, MonoFont(9.5, th.C(theme.FgGhost)))
			}
		})
		body(c)
	})
	c.Box(key+".rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

// Stat is a row of a side section: a name, and its value at the right in tone.
func Stat(c *ui.Ctx, key, name, value string, tone render.Color) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Role: ui.RoleText, Label: name + " " + value}, func(c *ui.Ctx) {
		c.Text("n", name, MonoFont(11.5, th.C(theme.FgMuted)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("v", value, MonoFont(11.5, tone))
	})
}

// StatAgo is a Stat whose value is how long ago at was, then suffix.
func StatAgo(c *ui.Ctx, key, name string, at time.Time, suffix string, tone render.Color) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Role: ui.RoleText, Label: name + " " + Ago(c.Now(), at) + suffix}, func(c *ui.Ctx) {
		c.Text("n", name, MonoFont(11.5, th.C(theme.FgMuted)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		widget.Ago(c, "v", at, widget.AgoOpts{Text: MonoFont(11.5, tone), Suffix: suffix})
	})
}

// LoadState is a load as the toolkit shows it.
func LoadState(l registry.Load) widget.LoadState {
	switch {
	case l.Done:
		return widget.LoadState{Phase: widget.Ready}
	case l.Err != nil:
		return widget.LoadState{Phase: widget.Failed, Err: l.Err}
	}
	return widget.LoadState{Phase: widget.Pending, Since: l.Since}
}

// TypedDirs are the directory a typed path names, and the directories in it that start with its last part.
func TypedDirs(q, home string) []string {
	p := typedPath(q, home)
	p, _ = filepath.Abs(p)
	return typedDirs(q, p, func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && st.IsDir()
	}, func(p string) []fs.DirEntry {
		ents, _ := os.ReadDir(p)
		return ents
	})
}

// TypedDirs is kit.TypedDirs read through the cache, for a frame: none until the folders are read, and none for a
// relative path.
func (r *Reads) TypedDirs(q, home string) []string {
	p := typedPath(q, home)
	if !filepath.IsAbs(p) {
		return nil
	}
	return typedDirs(q, filepath.Clean(p), func(p string) bool {
		st, _ := r.Stat(p)
		return st != nil && st.IsDir()
	}, func(p string) []fs.DirEntry {
		ents, _ := r.Dir(p)
		return ents
	})
}

func typedPath(q, home string) string {
	if strings.HasPrefix(q, "~") {
		return filepath.Join(home, q[1:])
	}
	return q
}

func typedDirs(q, p string, isDir func(string) bool, readDir func(string) []fs.DirEntry) []string {
	var out []string
	if isDir(p) {
		out = append(out, p)
	}
	parent, prefix := p, ""
	if !strings.HasSuffix(q, "/") {
		parent, prefix = filepath.Dir(p), filepath.Base(p)
	}
	for _, e := range readDir(parent) {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) && !strings.HasPrefix(e.Name(), ".") {
			if c := filepath.Join(parent, e.Name()); c != p {
				out = append(out, c)
			}
		}
	}
	return out
}
