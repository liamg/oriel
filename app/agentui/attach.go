package agentui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/ui/widget"
)

// pastedFiles is what a field writing to session id's agent makes of pasted and dropped files and pictures
// (widget.InputOpts.Files): markdown the thread shows and the agent reads. A picture is an image; a file in one of the
// session's projects is a link to it; any other file is copied where the agent can read it (host.Attach) first.
func pastedFiles(h plugin.Host, id core.SessionID) func(widget.Pasted) string {
	return func(p widget.Pasted) string {
		w, err := kit.SessionCalls(h)
		if err != nil {
			return ""
		}
		fail := func(err error) {
			h.Notify(core.Notice{ID: "paste.files", Severity: core.SevError, Source: "paste", Text: err.Error(), Time: time.Now()})
		}
		if len(p.Image) > 0 {
			path, err := w.Attach(host.AttachReq{Session: id, Name: "pasted-" + time.Now().Format("150405") + ".png", Data: p.Image})
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
			name := filepath.Base(path)
			if !inProjects(projects, path) {
				kept, err := w.Attach(host.AttachReq{Session: id, Path: path})
				if err != nil {
					fail(fmt.Errorf("%s: %w", name, err))
					continue
				}
				path = kept
			}
			out = append(out, mdFile(name, path))
		}
		if len(out) == 0 {
			return ""
		}
		return strings.Join(out, " ") + " "
	}
}

func inProjects(ps []core.Project, path string) bool {
	return slices.ContainsFunc(ps, func(p core.Project) bool { return kit.Inside(path, p.Dir) })
}

// mdFile is a file as markdown: an image for a picture, else a link named name.
func mdFile(name, path string) string {
	if kit.IsImage(path) {
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
