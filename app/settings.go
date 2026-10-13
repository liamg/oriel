package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/toml"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// SettingsPoll is how often the settings files are read for hand edits.
const SettingsPoll = 250 * time.Millisecond

var (
	tSetModified = theme.Declare("settings.modified", "accent")
	tSetSource   = theme.Declare("settings.source", "fg.faint")
	tSetLua      = theme.Declare("settings.source.lua", "status.permission")
	tSetToggleOn = theme.Declare("settings.toggle.on", "accent")
)

// settingsFile is one settings.toml layer: the user's, or a project's, which applies only once trusted.
type settingsFile struct {
	path  string
	layer registry.Layer
	at    registry.At
	trust bool

	mu        sync.Mutex
	loaded    bool
	hash      [32]byte
	applied   map[string]bool // the options it set
	err       error           // why it doesn't parse
	unknown   []string        // keys no option has
	untrusted bool
}

// settingsExt is settings.toml storage (settings.md) and the settings page (:Settings, ⌘,): every option from the
// registry by section, with typed controls, where each value comes from, modified markers and reset. Edits write
// the one line they change.
type settingsExt struct {
	disposer
	sh *shell
	h  ext.Host

	user, project *settingsFile
	trustPath     string
	loadMu        sync.Mutex
	specs         int // how many options there were at the last read

	open    bool
	query   string
	section string
	scope   int // the layer the page edits: user, project, session
	asTOML  bool
	undo    []settingsUndo
	menu    string // the option whose values are dropped down
}

type settingsUndo struct {
	path string
	src  []byte
	had  bool
}

var scopeNames = []string{"USER", "PROJECT", "SESSION"}

func (s *settingsExt) Manifest() ext.Manifest { return ext.Manifest{ID: "settings"} }

func (s *settingsExt) Activate(_ context.Context, h ext.Host) error {
	s.h = h
	conf := s.sh.cfg.ConfigDir
	s.user = &settingsFile{path: filepath.Join(conf, "settings.toml"), layer: registry.LayerUser}
	s.trustPath = filepath.Join(conf, "trusted.toml")
	if root := s.sh.cfg.WorkDir; root != "" {
		s.project = &settingsFile{path: filepath.Join(root, ".oriel", "settings.toml"), layer: registry.LayerProject,
			at: registry.At{Project: root}, trust: true}
	}
	str := func(a registry.Args, k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }
	for _, c := range []registry.Command{
		{ID: "Settings", Title: "The settings page; :Settings {section or search}", Run: func(_ registry.Ctx, a registry.Args) error {
			s.show(str(a, "args"))
			return nil
		}},
		{ID: "settings.open", Title: "Open the settings page at a {section}, or a {query}", Run: func(_ registry.Ctx, a registry.Args) error {
			s.open, s.query = true, str(a, "query")
			if sec := str(a, "section"); sec != "" {
				s.section = sec
			}
			return nil
		}},
		{ID: "settings.set", Title: "Set an option: {name} = {value}, at {scope}: user, project or session", Run: func(_ registry.Ctx, a registry.Args) error {
			return s.set(str(a, "name"), a["value"], s.scopeArg(str(a, "scope")))
		}},
		{ID: "settings.reset", Title: "Reset an option at {scope}: its line leaves the file", Run: func(_ registry.Ctx, a registry.Args) error {
			return s.reset(str(a, "name"), s.scopeArg(str(a, "scope")))
		}},
		{ID: "settings.reset_section", Title: "Reset every option in a {section}", Run: func(_ registry.Ctx, a registry.Args) error {
			return s.resetWhere(func(sp registry.OptionSpec) bool { return sectionOf(sp) == str(a, "section") }, s.scope)
		}},
		{ID: "settings.reset_all", Title: "Reset every option", Run: func(registry.Ctx, registry.Args) error {
			return s.resetWhere(func(registry.OptionSpec) bool { return true }, s.scope)
		}},
		{ID: "settings.undo", Title: "Undo the last settings change", Run: func(registry.Ctx, registry.Args) error { return s.undoLast() }},
		{ID: "settings.trust", Title: "Trust a project's settings file, as it is now: {path}", Run: func(_ registry.Ctx, a registry.Args) error {
			return s.trustFile(str(a, "path"))
		}},
	} {
		s.add(h.Command(c))
	}
	s.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-,>", Command: "settings.open", Desc: "Settings"}))
	s.add(h.View(OverlayPrefix+"settings", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "settings", Fn: s.build}
	})))
	s.loadAll()
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(SettingsPoll)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if s.loadAll() {
					h.Redraw()
				}
			}
		}
	}()
	s.add(disposeFn(func() { close(stop) }))
	return nil
}

func (s *settingsExt) files() []*settingsFile {
	if s.project == nil {
		return []*settingsFile{s.user}
	}
	return []*settingsFile{s.user, s.project}
}

// loadAll reads each file that changed since it was last read, and reports whether any did.
func (s *settingsExt) loadAll() bool {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	// Options registered since (a plugin's) may now take keys that were unknown: read the files again.
	if n := len(s.h.Options().Specs()); n != s.specs {
		s.specs = n
		for _, f := range s.files() {
			f.mu.Lock()
			f.loaded = f.loaded && len(f.unknown) == 0
			f.mu.Unlock()
		}
	}
	changed := false
	for _, f := range s.files() {
		if s.load(f) {
			changed = true
		}
	}
	return changed
}

// fileKey is a key of a settings file: the option name it spells (dotted, from its tables) and where it is.
type fileKey struct {
	name string
	path []string
	v    any
}

func fileKeys(m map[string]any, prefix []string) []fileKey {
	var out []fileKey
	for _, k := range slices.Sorted(maps.Keys(m)) {
		p := append(slices.Clone(prefix), k)
		if sub, ok := m[k].(map[string]any); ok {
			out = append(out, fileKeys(sub, p)...)
			continue
		}
		out = append(out, fileKey{name: strings.Join(p, "."), path: p, v: m[k]})
	}
	return out
}

func (s *settingsExt) load(f *settingsFile) bool {
	src, err := os.ReadFile(f.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	h := sha256.Sum256(src)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loaded && h == f.hash {
		return false
	}
	f.loaded, f.hash = true, h
	o := s.h.Options()
	if f.trust && len(src) > 0 && !s.trusted(f.path, h) {
		f.untrusted = true
		s.apply(f, nil)
		s.h.Notify(core.Notice{ID: "settings.untrusted", Severity: core.SevWarning, Title: "Project settings",
			Text:    f.path + " isn't trusted yet, so it isn't applied. Trust it if you know what it does.",
			Actions: []core.Action{{Title: "Trust", Command: "settings.trust", Args: map[string]any{"path": f.path}}}})
		return true
	}
	f.untrusted = false
	doc, err := toml.Parse(src)
	if err != nil {
		f.err = err
		s.h.Notify(core.Notice{ID: "settings.parse", Severity: core.SevError, Title: "Settings", Location: f.path,
			Text: fmt.Sprintf("%s: %v; the last good values stay", f.path, err)})
		return true
	}
	f.err = nil
	var bad []string
	vals := map[string]any{}
	f.unknown = nil
	for _, e := range fileKeys(doc.Value(), nil) {
		sp, ok := o.Spec(e.name)
		if !ok {
			f.unknown = append(f.unknown, e.name)
			continue
		}
		v, err := fromTOML(sp, e.v)
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		vals[sp.Name] = v
	}
	bad = append(bad, s.apply(f, vals)...)
	if len(bad) > 0 {
		s.h.Notify(core.Notice{ID: "settings.value", Severity: core.SevWarning, Title: "Settings", Location: f.path,
			Text: f.path + ": " + strings.Join(bad, "; ")})
	}
	return true
}

// apply sets vals at f's layer and unsets what f set before and no longer does.
func (s *settingsExt) apply(f *settingsFile, vals map[string]any) (errs []string) {
	o := s.h.Options()
	next := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(vals)) {
		if err := o.SetFrom(name, f.layer, f.at, vals[name], f.path); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		next[name] = true
	}
	for name := range f.applied {
		if !next[name] {
			o.Unset(name, f.layer, f.at)
		}
	}
	f.applied = next
	return errs
}

// fromTOML is a file's value as an option holds it.
func fromTOML(sp registry.OptionSpec, v any) (any, error) {
	switch sp.Type {
	case registry.Int:
		if n, ok := v.(int64); ok {
			return int(n), nil
		}
	case registry.Float:
		switch n := v.(type) {
		case float64:
			return n, nil
		case int64:
			return float64(n), nil
		}
	case registry.Duration:
		if d, ok := v.(string); ok {
			return time.ParseDuration(d)
		}
	default:
		return v, nil
	}
	return nil, fmt.Errorf("%s: %v isn't a %s", sp.Name, v, typeName(sp))
}

func toTOML(sp registry.OptionSpec, v any) any {
	if d, ok := v.(time.Duration); ok {
		return d.String()
	}
	return v
}

func typeName(sp registry.OptionSpec) string {
	return [...]string{"bool", "int", "float", "string", "enum", "duration", "key"}[sp.Type]
}

// --- trust ---

func (s *settingsExt) trusted(path string, h [32]byte) bool {
	src, err := os.ReadFile(s.trustPath)
	if err != nil {
		return false
	}
	doc, err := toml.Parse(src)
	if err != nil {
		return false
	}
	v, _ := doc.Get("projects", path)
	return v == hex.EncodeToString(h[:])
}

func (s *settingsExt) trustFile(path string) error {
	f := s.project
	if f == nil || path != f.path {
		return fmt.Errorf("%s isn't this project's settings file", path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	h := sha256.Sum256(src)
	if err := s.edit(s.trustPath, false, func(doc *toml.Document) error {
		return doc.Set([]string{"projects", path}, hex.EncodeToString(h[:]))
	}); err != nil {
		return err
	}
	f.mu.Lock()
	f.loaded = false
	f.mu.Unlock()
	s.load(f)
	return nil
}

// --- writing ---

func (s *settingsExt) scopeArg(name string) int {
	switch name {
	case "user":
		return 0
	case "project":
		return 1
	case "session":
		return 2
	}
	return s.scope
}

// fileFor is the file a scope writes, or nil for the session, which is never written.
func (s *settingsExt) fileFor(scope int) (*settingsFile, error) {
	switch scope {
	case 0:
		return s.user, nil
	case 1:
		if s.project == nil {
			return nil, fmt.Errorf("no project is open")
		}
		return s.project, nil
	}
	return nil, nil
}

func (s *settingsExt) sessionAt() registry.At {
	return registry.At{Session: s.h.Here().Session}
}

// readOnly says why the page can't change name: a value set in Lua, which wins over the files.
func (s *settingsExt) readOnly(name string, at registry.At) error {
	if _, l, err := s.h.Options().Get(name, at); err == nil && l == registry.LayerLua {
		return fmt.Errorf("%s is set in %s; change it there", name, s.h.Options().Source(name, at))
	}
	return nil
}

func (s *settingsExt) set(name string, v any, scope int) error {
	o := s.h.Options()
	sp, ok := o.Spec(name)
	if !ok {
		return fmt.Errorf("no option %q", name)
	}
	if err := s.readOnly(sp.Name, s.atFor(scope)); err != nil {
		return err
	}
	if f, ok := v.(float64); ok && sp.Type == registry.Int && f == float64(int(f)) {
		v = int(f)
	}
	f, err := s.fileFor(scope)
	if err != nil {
		return err
	}
	if f == nil {
		return o.Set(sp.Name, registry.LayerSession, s.sessionAt(), v)
	}
	if err := checkValue(o, sp, f, v); err != nil {
		return err
	}
	return s.write(f, sp, func(doc *toml.Document, p []string) error { return doc.Set(p, toTOML(sp, v)) })
}

// checkValue validates v for sp at f's layer without leaving it set.
func checkValue(o registry.Options, sp registry.OptionSpec, f *settingsFile, v any) error {
	old, had := o.Lookup(sp.Name, f.layer, f.at)
	if err := o.SetFrom(sp.Name, f.layer, f.at, v, f.path); err != nil {
		return err
	}
	if had {
		return o.SetFrom(sp.Name, f.layer, f.at, old, f.path)
	}
	o.Unset(sp.Name, f.layer, f.at)
	return nil
}

func (s *settingsExt) reset(name string, scope int) error {
	o := s.h.Options()
	sp, ok := o.Spec(name)
	if !ok {
		return fmt.Errorf("no option %q", name)
	}
	f, err := s.fileFor(scope)
	if err != nil {
		return err
	}
	if f == nil {
		o.Unset(sp.Name, registry.LayerSession, s.sessionAt())
		return nil
	}
	return s.write(f, sp, func(doc *toml.Document, p []string) error { return doc.Delete(p...) })
}

func (s *settingsExt) resetWhere(keep func(registry.OptionSpec) bool, scope int) error {
	var errs []error
	for _, sp := range s.h.Options().Specs() {
		if keep(sp) {
			if _, set := s.h.Options().Lookup(sp.Name, s.layerFor(scope), s.atFor(scope)); set {
				errs = append(errs, s.reset(sp.Name, scope))
			}
		}
	}
	return errors.Join(errs...)
}

// write changes the line for sp in f with fn, at the key the file already spells it with (an alias, a dotted key)
// or as its name's tables.
func (s *settingsExt) write(f *settingsFile, sp registry.OptionSpec, fn func(doc *toml.Document, p []string) error) error {
	o := s.h.Options()
	err := s.edit(f.path, true, func(doc *toml.Document) error {
		p := strings.Split(sp.Name, ".")
		for _, e := range fileKeys(doc.Value(), nil) {
			if other, ok := o.Spec(e.name); ok && other.Name == sp.Name {
				p = e.path
				break
			}
		}
		return fn(doc, p)
	})
	if err != nil {
		return err
	}
	if f.trust {
		if err := s.trustFile(f.path); err != nil {
			return err
		}
	}
	s.load(f)
	return nil
}

// edit rewrites the TOML file at path with fn, refusing one that doesn't parse. With undo, the old file is kept.
func (s *settingsExt) edit(path string, undo bool, fn func(doc *toml.Document) error) error {
	src, err := os.ReadFile(path)
	had := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	doc, err := toml.Parse(src)
	if err != nil {
		return fmt.Errorf("%s doesn't parse (%v), so nothing was written", path, err)
	}
	if err := fn(doc); err != nil {
		return err
	}
	out := doc.Bytes()
	if string(out) == string(src) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if undo {
		s.undo = append(s.undo, settingsUndo{path: path, src: src, had: had})
	}
	return os.WriteFile(path, out, 0o644)
}

func (s *settingsExt) undoLast() error {
	n := len(s.undo)
	if n == 0 {
		return nil
	}
	u := s.undo[n-1]
	s.undo = s.undo[:n-1]
	var err error
	if u.had {
		err = os.WriteFile(u.path, u.src, 0o644)
	} else {
		err = os.Remove(u.path)
	}
	if err != nil {
		return err
	}
	for _, f := range s.files() {
		if f.path == u.path {
			if f.trust {
				s.trustFile(f.path)
			}
			s.load(f)
		}
	}
	return nil
}

func (s *settingsExt) layerFor(scope int) registry.Layer {
	return [...]registry.Layer{registry.LayerUser, registry.LayerProject, registry.LayerSession}[scope]
}

// atFor is where the page reads: where the user is, so a buffer's :set shows too.
func (s *settingsExt) atFor(int) registry.At { return s.h.Here() }

// --- the page ---

// sectionOf is an option's section: its Section, or its name's prefix.
func sectionOf(sp registry.OptionSpec) string {
	if sp.Section != "" {
		return sp.Section
	}
	sec, _, _ := strings.Cut(sp.Name, ".")
	return sec
}

func (s *settingsExt) show(arg string) {
	s.open = true
	if arg == "" {
		return
	}
	for _, sp := range s.h.Options().Specs() {
		if sectionOf(sp) == arg {
			s.section, s.query = arg, ""
			return
		}
	}
	s.query = arg
}

func formatValue(v any) string {
	switch v := v.(type) {
	case string:
		return strconv.Quote(v)
	case time.Duration:
		return strconv.Quote(v.String())
	}
	return fmt.Sprint(v)
}

// matches is the page's search: words of the name, help, aliases or value; @modified, @project and @plugin:id filter.
func (s *settingsExt) matches(sp registry.OptionSpec, at registry.At) bool {
	o := s.h.Options()
	v, l, _ := o.Get(sp.Name, at)
	for _, w := range strings.Fields(strings.ToLower(s.query)) {
		switch {
		case w == "@modified":
			if l == registry.LayerDefault {
				return false
			}
		case w == "@project":
			if l != registry.LayerProject {
				return false
			}
		case strings.HasPrefix(w, "@plugin:"):
			if !strings.Contains(strings.ToLower(sp.Source), strings.TrimPrefix(w, "@plugin:")) {
				return false
			}
		default:
			hay := strings.ToLower(sp.Name + " " + sp.Help + " " + strings.Join(sp.Aliases, " ") + " " + fmt.Sprint(v))
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	return true
}

func (s *settingsExt) build(c *ui.Ctx) {
	if !s.open || c.Theme().IsProbe() {
		return
	}
	th := c.Theme()
	size := c.Size()
	o := s.h.Options()
	specs := o.Specs()
	sections := map[string]int{}
	at := s.atFor(s.scope)
	for _, sp := range specs {
		sections[sectionOf(sp)] += 0
		if _, l, _ := o.Get(sp.Name, at); l != registry.LayerDefault {
			sections[sectionOf(sp)]++
		}
	}
	names := slices.Sorted(maps.Keys(sections))
	if s.section == "" && len(names) > 0 {
		s.section = names[0]
	}
	var rows []registry.OptionSpec
	for _, sp := range specs {
		if s.query != "" && s.matches(sp, at) || s.query == "" && sectionOf(sp) == s.section {
			rows = append(rows, sp)
		}
	}
	boxID := c.Next("settings")
	in := c.Interaction(boxID)
	for _, k := range in.Keys {
		if k.Key == "Esc" {
			s.open = false
			return
		}
	}
	top := render.Point{Y: titleH}
	c.Column("settings", ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y - titleH), Z: 900, Fill: th.C(theme.BgBase), Clip: true, Trap: true,
		Focusable: true, KeyMode: "settings", Float: &ui.Float{Point: &top}, Role: ui.RoleDialog, Label: "Settings", Help: "shell/settings"}, func(c *ui.Ctx) {
		if u := s.h.UI(); u != nil && !in.Focused && !strings.HasPrefix(u.Help(u.Focused()), "shell/settings") {
			c.Focus(boxID)
		}
		s.header(c, th)
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Row("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("sections", ui.Style{W: ui.Px(240), H: ui.Flex(1), Pad: ui.PadXY(8, 12), Gap: 2, Fill: th.C(tThemePanel), ScrollY: true}, func(c *ui.Ctx) {
				c.Text("t", "SECTIONS", capLabel(th))
				c.Box("g", ui.Style{H: ui.Px(6)}, nil)
				for _, name := range names {
					id := c.Next(name)
					hit := c.Interaction(id)
					if hit.Clicked {
						s.section, s.query = name, ""
					}
					on := s.query == "" && name == s.section
					fill, fg := render.Color{}, theme.FgMuted
					switch {
					case on:
						fill, fg = th.C(theme.AccentBg), theme.Fg
					case hit.Hovered:
						fill = th.C(theme.BgRaised)
					}
					c.Row(name, ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center, Fill: fill, Radius: 5,
						Interactive: true, Cursor: render.CursorPointer, Help: "shell/settings#sections", Role: ui.RoleListItem, Label: name, Selected: on}, func(c *ui.Ctx) {
						c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(fg)})
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if n := sections[name]; n > 0 {
							c.Text("m", fmt.Sprintf("● %d", n), monoAt(th, 10, tSetModified))
						}
					})
				}
			})
			c.Box("d", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(28, 16), Gap: 4, ScrollY: true}, func(c *ui.Ctx) {
				title := s.section
				if s.query != "" {
					title = fmt.Sprintf("%d matching %q", len(rows), s.query)
				}
				c.Row("title", ui.Style{W: ui.Flex(1), H: ui.Px(36), Cross: ui.Center, Gap: 12}, func(c *ui.Ctx) {
					c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 18, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					if s.query == "" && widget.Button(c, "resetsec", "Reset section", widget.ButtonOpts{Ghost: true, Help: "shell/settings#reset"}) {
						s.fail(s.resetWhere(func(sp registry.OptionSpec) bool { return sectionOf(sp) == s.section }, s.scope))
					}
				})
				for _, sp := range rows {
					s.row(c, th, sp, at)
				}
				for _, f := range s.files() {
					f.mu.Lock()
					unknown := slices.Clone(f.unknown)
					f.mu.Unlock()
					if len(unknown) > 0 && s.query == "" {
						widget.Wrap(c, "unknown"+f.path, fmt.Sprintf("%s also sets %s, which nothing has registered.", f.path, strings.Join(unknown, ", ")),
							700, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
					}
				}
			})
		})
		s.footer(c, th)
	})
}

func (s *settingsExt) header(c *ui.Ctx, th *theme.Resolved) {
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(16, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", "SETTINGS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.SemiBold, Tracking: .14}, Color: th.C(theme.Accent)})
		c.Box("q", ui.Style{W: ui.Px(420)}, func(c *ui.Ctx) {
			widget.Input(c, "in", &s.query, widget.InputOpts{Label: "Search settings", Placeholder: "search · @modified · @project · @plugin:id",
				Help: "shell/settings#search"})
		})
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("scope", "EDITING", capLabel(th))
		scope := s.scope
		if segmented(c, "scopes", scopeNames, &scope, "shell/settings#scope") {
			if _, err := s.fileFor(scope); err != nil {
				s.fail(err)
			} else {
				s.scope = scope
			}
		}
		toml := 0
		if s.asTOML {
			toml = 1
		}
		if segmented(c, "as", []string{"CONTROLS", "TOML"}, &toml, "shell/settings#toml") {
			s.asTOML = toml == 1
		}
	})
}

func (s *settingsExt) sourceLabel(name string, l registry.Layer, at registry.At) string {
	switch l {
	case registry.LayerDefault:
		return "default"
	case registry.LayerLua:
		return "set in " + s.h.Options().Source(name, at)
	case registry.LayerSession, registry.LayerFile, registry.LayerBuffer, registry.LayerWindow:
		if src := s.h.Options().Source(name, at); src != "" {
			return l.String() + " · " + src
		}
	}
	return l.String()
}

func (s *settingsExt) row(c *ui.Ctx, th *theme.Resolved, sp registry.OptionSpec, at registry.At) {
	o := s.h.Options()
	v, l, _ := o.Get(sp.Name, at)
	_, setHere := o.Lookup(sp.Name, s.layerFor(s.scope), at)
	lua := l == registry.LayerLua
	canScope := sp.Scopes&[...]registry.Scope{registry.ScopeUser, registry.ScopeProject, registry.ScopeSession}[s.scope] != 0
	disabled := lua || !canScope
	c.Row(sp.Name, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 10), Gap: 16, Cross: ui.Center, Help: "shell/settings#rows"}, func(c *ui.Ctx) {
		mark := render.Color{}
		if l != registry.LayerDefault {
			mark = th.C(tSetModified)
		}
		c.Box("mod", ui.Style{W: ui.Px(3), H: ui.Px(36), Radius: 2, Fill: mark}, nil)
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
			c.Row("name", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", sp.Name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12.5, Weight: typeset.Medium}, Color: th.C(theme.Fg)})
				if len(sp.Aliases) > 0 {
					c.Text("a", strings.Join(sp.Aliases, " · "), monoAt(th, 10, theme.FgGhost))
				}
			})
			if s.asTOML {
				sec, key, _ := strings.Cut(sp.Name, ".")
				c.Text("toml", fmt.Sprintf("[%s] %s = %s", sec, key, formatValue(toTOML(sp, v))), monoAt(th, 11, tThemeTOMLVal))
				c.Text("lua", fmt.Sprintf("L.o[%q] = %s", sp.Name, formatValue(toTOML(sp, v))), monoAt(th, 11, theme.FgFaint))
			} else if sp.Help != "" {
				widget.Wrap(c, "h", sp.Help, 560, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: th.C(theme.FgFaint)})
			}
		})
		srcTok := tSetSource
		if lua {
			srcTok = tSetLua
		}
		c.Text("src", s.sourceLabel(sp.Name, l, at), monoAt(th, 10.5, srcTok))
		c.Box("ctl", ui.Style{W: ui.Px(260), Main: ui.End}, func(c *ui.Ctx) { s.control(c, th, sp, v, disabled) })
		if widget.Button(c, "reset", "↺", widget.ButtonOpts{Ghost: true, Disabled: !setHere || disabled, Help: "shell/settings#reset"}) {
			s.fail(s.reset(sp.Name, s.scope))
		}
	})
}

// control is a row's typed control: a toggle, choices, or a field that's checked when entered.
func (s *settingsExt) control(c *ui.Ctx, th *theme.Resolved, sp registry.OptionSpec, v any, disabled bool) {
	switch sp.Type {
	case registry.Bool:
		on, _ := v.(bool)
		id := c.Next("toggle")
		if c.Interaction(id).Clicked && !disabled {
			s.fail(s.set(sp.Name, !on, s.scope))
		}
		track, side := th.C(theme.BgStrong), ui.Start
		if on {
			track, side = th.C(tSetToggleOn), ui.End
		}
		c.Row("toggle", ui.Style{W: ui.Px(32), H: ui.Px(18), Pad: ui.Pad(2), Main: side, Radius: 9, Fill: track, Interactive: true, Focusable: true,
			Disabled: disabled, Cursor: render.CursorPointer, KeyMode: "button", Help: "shell/settings#rows", Role: ui.RoleButton, Label: sp.Name,
			Selected: on}, func(c *ui.Ctx) {
			c.Box("knob", ui.Style{W: ui.Px(14), H: ui.Px(14), Radius: 7, Fill: th.C(theme.Fg)}, nil)
		})
	case registry.Enum:
		cur, _ := v.(string)
		if len(sp.Values) <= 4 {
			i := slices.Index(sp.Values, cur)
			if segmented(c, "choices", upper(sp.Values), &i, "shell/settings#rows") && !disabled {
				s.fail(s.set(sp.Name, sp.Values[i], s.scope))
			}
			return
		}
		id := c.Next("drop")
		if widget.Button(c, "drop", cur+" ▾", widget.ButtonOpts{Disabled: disabled, Help: "shell/settings#rows"}) {
			s.menu = sp.Name
		}
		open := s.menu == sp.Name
		widget.Popover(c, "menu", id, &open, widget.PopoverOpts{Label: sp.Name, Width: 220, Help: "shell/settings#rows"}, func(c *ui.Ctx) {
			items := make([]widget.MenuItem, len(sp.Values))
			for i, val := range sp.Values {
				items[i] = widget.MenuItem{Label: val, Help: "shell/settings#rows"}
			}
			if i, closed := widget.Menu(c, "m", items, widget.MenuOpts{Width: 220, Help: "shell/settings#rows"}); i >= 0 {
				s.fail(s.set(sp.Name, sp.Values[i], s.scope))
				open = false
			} else if closed {
				open = false
			}
		})
		if !open && s.menu == sp.Name {
			s.menu = ""
		}
	default:
		text := fieldText(v)
		fid := c.Next("field")
		st := ui.State[fieldState](c, fid)
		if !c.Interaction(ui.Child(fid, "in")).Focused && st.from != text {
			st.text, st.from = text, text
		}
		c.Box("field", ui.Style{W: ui.Px(200)}, func(c *ui.Ctx) {
			if widget.Input(c, "in", &st.text, widget.InputOpts{Label: sp.Name, Disabled: disabled, Help: "shell/settings#rows"}).Submitted {
				val, err := parseField(sp, st.text)
				if err == nil {
					err = s.set(sp.Name, val, s.scope)
				}
				s.fail(err)
			}
		})
	}
}

// fieldState is a field's text, and the value it was last filled from.
type fieldState struct{ text, from string }

func upper(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

func fieldText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// parseField reads what was typed into a field as sp's type.
func parseField(sp registry.OptionSpec, s string) (any, error) {
	switch sp.Type {
	case registry.Int:
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("%s is a whole number, not %q", sp.Name, s)
		}
		return n, nil
	case registry.Float:
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fmt.Errorf("%s is a number, not %q", sp.Name, s)
		}
		return f, nil
	case registry.Duration:
		return time.ParseDuration(strings.TrimSpace(s))
	}
	return s, nil
}

func (s *settingsExt) footer(c *ui.Ctx, th *theme.Resolved) {
	f, _ := s.fileFor(s.scope)
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(16, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tThemePanel), Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
		where := "this session: not written to disk"
		if f != nil {
			where = "writing to " + tildePath(f.path)
		}
		c.Text("w", where, monoAt(th, 10.5, theme.FgFaint))
		if f != nil {
			f.mu.Lock()
			err, untrusted := f.err, f.untrusted
			f.mu.Unlock()
			switch {
			case err != nil:
				c.Text("err", "✕ "+err.Error()+"; the last good values stay", monoAt(th, 10.5, theme.Error))
			case untrusted:
				c.Text("trust", "not trusted yet, so not applied", monoAt(th, 10.5, theme.Warn))
			}
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "undo", "Undo", widget.ButtonOpts{Ghost: true, Disabled: len(s.undo) == 0, Help: "shell/settings#reset"}) {
			s.fail(s.undoLast())
		}
		if widget.Button(c, "all", "Reset all", widget.ButtonOpts{Ghost: true, Help: "shell/settings#reset"}) {
			s.fail(s.resetWhere(func(registry.OptionSpec) bool { return true }, s.scope))
		}
		c.Text("keys", "esc close", monoAt(th, 10.5, theme.FgFaint))
	})
}

// tildePath shows a path under the home directory as ~/….
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func (s *settingsExt) fail(err error) {
	if err != nil {
		s.h.Notify(core.Notice{ID: "settings.set", Severity: core.SevError, Title: "Settings", Text: err.Error(), Key: true})
	}
}
