package agentui

import (
	"cmp"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/sessionkit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/internal/imagex"
	"github.com/liamg/oriel/internal/pathx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// pastedFiles is what a field writing to session id's agent makes of pasted and dropped files and pictures
// (widget.InputOpts.Files): markdown the thread shows and the agent reads. A picture is an image; a file in one of the
// session's projects is a link to it; any other file is copied where the agent can read it (hostapi.Attach) first.
func pastedFiles(h plugin.Host, id core.SessionID) func(widget.Pasted) string {
	return func(p widget.Pasted) string {
		fail := func(err error) {
			kit.NotifyError(h, "paste.files", err, true)
		}
		if len(p.Image) > 0 {
			w, err := sessionkit.SessionCalls(h)
			if err != nil {
				return ""
			}
			path, err := w.Attach(hostapi.AttachReq{Session: id, Name: "pasted-" + time.Now().Format("150405") + ".png", Data: p.Image})
			if err != nil {
				fail(err)
				return ""
			}
			return "![image](" + path + ") "
		}
		var projects []core.Project
		if se, ok := h.Session(id); ok {
			projects = se.Projects()
		}
		var out []string
		for _, path := range p.Paths {
			md, err := attachPath(h, id, projects, path)
			if err != nil {
				fail(err)
				continue
			}
			out = append(out, md)
		}
		if len(out) == 0 {
			return ""
		}
		return strings.Join(out, " ") + " "
	}
}

func inProjects(ps []core.Project, path string) bool {
	return slices.ContainsFunc(ps, func(p core.Project) bool { return pathx.Inside(path, p.Dir) })
}

// mdFile is a file as markdown: an image for a picture, else a link named name.
func mdFile(name, path string) string {
	if imagex.IsImage(path) {
		return "![" + name + "](" + mdDest(path) + ")"
	}
	return "[" + name + "](" + mdDest(path) + ")"
}

// mdDest is a path as a link's destination: in <…> when it has a space or a parenthesis.
func mdDest(path string) string {
	if strings.ContainsAny(path, " ()") {
		return "<" + path + ">"
	}
	return path
}

var mdImage = regexp.MustCompile(`!\[[^\]]*\]\(\s*(<[^>]+>|[^)\s]+)`)

// localImages are the pictures on this machine a markdown message shows: its images with absolute paths.
func localImages(md string) []string {
	var out []string
	for _, m := range mdImage.FindAllStringSubmatch(md, -1) {
		p := strings.TrimPrefix(strings.Trim(m[1], "<>"), "file://")
		if filepath.IsAbs(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// localFile is a file on this machine a markdown message links to, other than a picture: an attachment.
type localFile struct{ name, path string }

var mdLink = regexp.MustCompile(`(^|[^!])\[([^\]]*)\]\(\s*(<[^>]+>|[^)\s]+)`)

// localFiles are the files on this machine a markdown message links to, pictures aside: its links with absolute
// paths.
func localFiles(md string) []localFile {
	var out []localFile
	for _, m := range mdLink.FindAllStringSubmatch(md, -1) {
		p := strings.TrimPrefix(strings.Trim(m[3], "<>"), "file://")
		if !filepath.IsAbs(p) || imagex.IsImage(p) || slices.ContainsFunc(out, func(f localFile) bool { return f.path == p }) {
			continue
		}
		out = append(out, localFile{name: cmp.Or(m[2], filepath.Base(p)), path: p})
	}
	return out
}

// fileCards are a message's attached files, each a card: click it to open the file in a tab, or save a copy.
func (x *Requests) fileCards(c *ui.Ctx, md string) {
	th := c.Theme()
	for i, f := range localFiles(md) {
		fi, st := kit.ReadsIn(c).Stat(f.path)
		size := ""
		switch {
		case fi != nil:
			size = kit.Bytes(fi.Size())
		case !st.Loading():
			size = "missing"
		}
		c.Row(fmt.Sprint("file", i), ui.Style{Gap: 4, Pad: ui.PadXY(4, 2), Radius: 6, Stroke: th.C(tBlockLine), Border: 1, Cross: ui.Center},
			func(c *ui.Ctx) {
				if widget.Clickable(c, "open", ui.Style{Dir: ui.Row, Pad: ui.PadXY(6, 4), Gap: 8, Cross: ui.Center, Radius: 4, Focusable: true,
					Label: "Open " + f.name, Help: "agent/requests#attach"}, func(c *ui.Ctx) {
					c.Text("i", "▫", widget.MonoFont(12, th.C(theme.FgFaint)))
					c.Text("n", f.name, widget.MonoFont(11.5, th.C(theme.Accent)))
					c.Text("s", size, widget.MonoFont(10.5, th.C(theme.FgFaint)))
				}).Clicked {
					x.run("editor.open", registry.Args{"path": f.path, "place": "tab"})
				}
				if widget.IconButton(c, "save", "↓", widget.IconButtonOpts{Label: "Save " + f.name + " as…", Help: "agent/requests#attach"}) {
					x.run("request.file.save", registry.Args{"path": f.path, "name": f.name})
				}
			})
	}
}
