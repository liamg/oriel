package agentui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/desktop"
	"github.com/liamg/oriel/app/kit/sessionkit"
	"github.com/liamg/oriel/app/kit/work"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// The pickers of a file on this machine: one to attach to the request being written, and where to save an
// attachment.
const (
	attachSource = "attach-file"
	saveSource   = "save-file"
)

// hostFiles are the request.file.* commands and their pickers.
type hostFiles struct {
	x       *Requests
	saving  struct{ path, name string } // the attachment save-file is for
	chooser work.Work                   // a system chooser, open while a person picks
}

func (f *hostFiles) commands() []registry.Command {
	inFront := func(registry.Args) bool { return f.x.h.State().Focus != "" }
	return []registry.Command{
		{ID: "request.file.attach", Grant: string(plugin.CapFSRead), GrantOn: func(a registry.Args) string { return a.String("path") },
			Title: "Attach a file on this machine, {path}, to the request being written: the agent gets a copy; with no {path}, pick one", Available: inFront,
			Args: []registry.Arg{registry.StringArg("path")}, Run: func(ctx registry.Ctx, a registry.Args) error {
				if p := a.String("path"); p != "" {
					return f.attach(p)
				}
				return f.x.h.Run(ctx, "picker.open", registry.Args{"source": attachSource, "query": "~/"})
			}},
		{ID: "request.file.save", Grant: string(plugin.CapFSWrite), GrantOn: func(a registry.Args) string { return a.String("to") },
			Title: "Save a copy of a request's attached file {path}, called {name}, to {to} (a file, or a folder to put it in); with no {to}, pick where",
			Args:  []registry.Arg{registry.StringArg("name"), registry.StringArg("path"), registry.StringArg("to")}, Run: func(ctx registry.Ctx, a registry.Args) error {
				path := a.String("path")
				if path == "" {
					return errors.New("say which {path} to save")
				}
				name := attachedName(path, a.String("name"))
				if to := a.String("to"); to != "" {
					return f.save(path, name, to)
				}
				f.saving.path, f.saving.name = path, name
				return f.x.h.Run(ctx, "picker.open", registry.Args{"source": saveSource, "query": downloads() + name})
			}},
	}
}

// attach copies path where the session in front's agent can read it, and links it in the request being written.
func (f *hostFiles) attach(path string) error {
	se, ok := f.x.front()
	if !ok {
		return errors.New("no session is in front")
	}
	md, err := attachPath(f.x.h, se.ID, se.Projects, path)
	if err != nil {
		return err
	}
	st := f.x.state(se.ID)
	if _, ok := se.Thread(st.sel); !ok {
		st.writing = true
	}
	d := f.x.draftFor(st, f.x.target(st))
	if d.text != "" && !strings.HasSuffix(d.text, " ") && !strings.HasSuffix(d.text, "\n") {
		d.text += " "
	}
	d.text += md + " "
	d.compose = true
	return f.x.openRequest(context.Background())
}

// save copies the attachment at path to to, a file or a folder to put it in as name, and says where it went.
func (f *hostFiles) save(path, name, to string) error {
	to = expandHome(to)
	if fi, err := os.Stat(to); err == nil && fi.IsDir() {
		to = filepath.Join(to, name)
	}
	if err := copyFile(path, to); err != nil {
		return fmt.Errorf("couldn't save %s: %w", name, err)
	}
	f.x.h.Notify(core.Notice{ID: "request.file.saved", Severity: core.SevInfo, Source: "requests", AutoClear: true,
		Text:    "Saved " + name + " to " + kit.TildePath(to),
		Actions: []core.Action{{Title: "Reveal", Command: "system.reveal", Args: map[string]any{"path": to}}}})
	return nil
}

// attachSource lists the files and folders a typed path names; a folder opens, a file is attached, and Browse… asks
// the system's chooser.
func (f *hostFiles) attachSource() uireg.PickerSource {
	return uireg.PickerSource{ID: attachSource, Title: "Attach a file", PickerOnly: true, Unlisted: true, Live: true, Source: "requests",
		Items: func(_ registry.Ctx, query string) []uireg.PickerItem {
			out := browseItem("the system's file chooser")
			for _, p := range kit.TypedPaths(typed(query), home()) {
				out = append(out, pathItem(p))
			}
			return out
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			if it.ID == "browse" {
				f.chooser.Loop(func(ctx context.Context) { // waits on a person
					p, err := desktop.ChooseFile(ctx, "Attach a file")
					f.x.h.Do(func() { f.done(p, err, f.attach) })
				})
				return nil
			}
			p, _ := uireg.PickerData[string](it)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return f.x.h.Run(ctx, "picker.open", registry.Args{"source": attachSource, "query": kit.TildePath(p) + "/"})
			}
			return f.attach(p)
		}}
}

// saveSource saves to the path typed, or opens a folder to save in; Browse… asks the system's chooser.
func (f *hostFiles) saveSource() uireg.PickerSource {
	return uireg.PickerSource{ID: saveSource, Title: "Save as", PickerOnly: true, Unlisted: true, Live: true, Source: "requests",
		Available: func() bool { return f.saving.path != "" },
		Items: func(_ registry.Ctx, query string) []uireg.PickerItem {
			out := browseItem("the system's save chooser")
			q := typed(query)
			to := expandHome(q)
			if fi, err := os.Stat(filepath.Dir(to)); err == nil && fi.IsDir() && !strings.HasSuffix(q, "/") && filepath.IsAbs(to) {
				it := uireg.PickerItem{ID: "save", Label: "Save as " + kit.TildePath(to), Icon: "↓", Boost: 60, Data: to}
				if fi, err := os.Stat(to); err == nil {
					if fi.IsDir() {
						it.Label = "Save in " + kit.TildePath(to)
					} else {
						it.Detail = "replaces it"
					}
				}
				out = append([]uireg.PickerItem{it}, out...)
			}
			for _, p := range kit.TypedDirs(q, home()) {
				out = append(out, pathItem(p))
			}
			return out
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			path, name := f.saving.path, f.saving.name
			switch it.ID {
			case "browse":
				f.chooser.Loop(func(ctx context.Context) {
					to, err := desktop.ChooseSave(ctx, name)
					f.x.h.Do(func() { f.done(to, err, func(to string) error { return f.save(path, name, to) }) })
				})
				return nil
			case "save":
				to, _ := uireg.PickerData[string](it)
				return f.save(path, name, to)
			}
			p, _ := uireg.PickerData[string](it)
			return f.x.h.Run(ctx, "picker.open", registry.Args{"source": saveSource, "query": kit.TildePath(p) + "/" + name})
		}}
}

// done hands what a system chooser picked to then; nothing when it was cancelled, and a failure as a notice.
func (f *hostFiles) done(p string, err error, then func(string) error) {
	if err == nil && p != "" {
		err = then(p)
	}
	if err != nil {
		kit.NotifyError(f.x.h, "requests.file", err, true)
	}
}

// browseItem is the item asking the system's chooser, when the desktop has one.
func browseItem(detail string) []uireg.PickerItem {
	if !desktop.ChooseDirAvailable() {
		return nil
	}
	return []uireg.PickerItem{{ID: "browse", Label: "Browse…", Detail: detail, Icon: "…", Boost: 30}}
}

func pathItem(p string) uireg.PickerItem {
	it := uireg.PickerItem{ID: p, Label: kit.TildePath(p), Icon: "▫", Data: p}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		it.Icon, it.Label = "▤", it.Label+"/"
	}
	return it
}

// attachPath is markdown for a file given to session id's agent: a link to it where it is, in one of projects, else
// to a copy kept where the agent can read it (hostapi.Attach).
func attachPath(h plugin.Host, id core.SessionID, projects []core.Project, path string) (string, error) {
	name := filepath.Base(path)
	if !inProjects(projects, path) {
		w, err := sessionkit.SessionCalls(h)
		if err != nil {
			return "", err
		}
		kept, err := w.Attach(hostapi.AttachReq{Session: id, Path: path})
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		path = kept
	}
	return mdFile(name, path), nil
}

// attachedName is what an attachment kept at path is called: name if given, else its kept name without the id
// hostapi.Attach put before it.
func attachedName(path, name string) string {
	if name != "" {
		return filepath.Base(name)
	}
	base := filepath.Base(path)
	if i := strings.IndexByte(base, '-'); i == 6 && strings.Contains(filepath.ToSlash(path), "/attachments/") {
		return base[i+1:]
	}
	return base
}

// typed is a picker's query as a path: ~/ when empty.
func typed(q string) string {
	if q = strings.TrimSpace(q); q == "" {
		return "~/"
	}
	return q
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func expandHome(p string) string { return kit.ExpandHome(p, home()) }

// downloads is where a save starts: ~/Downloads/ when there is one, else ~/.
func downloads() string {
	if fi, err := os.Stat(filepath.Join(home(), "Downloads")); err == nil && fi.IsDir() {
		return "~/Downloads/"
	}
	return "~/"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
