package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/settingsfile"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
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

// settingsPlugin is settings.toml storage (settings.md) and the settings page (:Settings, ⌘,): every option from the
// registry by section, with typed controls, where each value comes from, modified markers and reset. Edits write
// the one line they change.
type settingsPlugin struct {
	disposer
	sh *shell
	h  plugin.Host

	user, project *settingsFile
	othersMu      sync.Mutex
	others        map[string]*settingsFile // other projects' files, by root: the repos of this machine's sessions
	trustPath     string
	loadMu        sync.Mutex
	specs         int // how many options there were at the last read

	open    bool
	query   string
	section string
	scope   int // the layer the page edits: user, project, session
	asTOML  bool
	undo    []settingsUndo
	saved   map[string]time.Time // when the page last set each option
}

// settingsSavedFor is how long a row says it was saved.
const settingsSavedFor = 1500 * time.Millisecond

type settingsUndo struct {
	path string
	src  []byte
	had  bool
}

var scopeNames = []string{"USER", "PROJECT", "SESSION"}

func (s *settingsPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "settings"} }

func (s *settingsPlugin) Activate(_ context.Context, h plugin.Host) error {
	s.h = h
	conf := s.sh.cfg.ConfigDir
	s.user = &settingsFile{path: filepath.Join(conf, "settings.toml"), layer: registry.LayerUser}
	s.trustPath = filepath.Join(conf, "trusted.toml")
	if root := s.sh.cfg.WorkDir; root != "" {
		s.project = projectFile(root)
	}
	s.add(h.Contribute(pagePlace("settings", "Settings", "settings.open", &s.open)))
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
		{ID: "settings.set", Title: "Set an option: {name} = {value}, at {scope}: user, project or session; with {key}, that key of a table option alone",
			Run: func(_ registry.Ctx, a registry.Args) error {
				if k := str(a, "key"); k != "" {
					return s.setKey(str(a, "name"), k, a["value"], s.scopeArg(str(a, "scope")))
				}
				return s.set(str(a, "name"), a["value"], s.scopeArg(str(a, "scope")))
			}},
		{ID: "settings.reset", Title: "Reset an option at {scope}: its line leaves the file; with {key}, that key of a table option alone",
			Run: func(_ registry.Ctx, a registry.Args) error {
				if k := str(a, "key"); k != "" {
					return s.setKey(str(a, "name"), k, nil, s.scopeArg(str(a, "scope")))
				}
				return s.reset(str(a, "name"), s.scopeArg(str(a, "scope")))
			}},
		{ID: "settings.reset_section", Title: "Reset every option in a {section}", Run: func(_ registry.Ctx, a registry.Args) error {
			return s.resetWhere(func(sp registry.OptionSpec) bool { return sectionOf(sp) == str(a, "section") }, s.scope)
		}},
		{ID: "settings.reset_all", Title: "Reset every option", Run: func(registry.Ctx, registry.Args) error {
			return s.resetWhere(func(registry.OptionSpec) bool { return true }, s.scope)
		}},
		{ID: "settings.undo", Title: "Undo the last settings change", Run: func(registry.Ctx, registry.Args) error { return s.undoLast() }},
		{ID: "settings.trust", Title: "Trust a project's settings file, as it is now: {path}",
			Run: func(_ registry.Ctx, a registry.Args) error { return s.trustFile(str(a, "path")) }},
	} {
		s.add(h.Command(c))
	}
	s.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-,>", Command: "settings.open", Desc: "Settings"}))
	s.add(h.View(OverlayPrefix+"settings", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "settings", Fn: s.build}
	})))
	s.loadAll()
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
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
	s.add(disposeFn(func() { close(stop); <-done }))
	return nil
}

func (s *settingsPlugin) files() []*settingsFile {
	out := []*settingsFile{s.user}
	if s.project != nil {
		out = append(out, s.project)
	}
	s.othersMu.Lock()
	defer s.othersMu.Unlock()
	for _, root := range slices.Sorted(maps.Keys(s.others)) {
		out = append(out, s.others[root])
	}
	return out
}

// projectFile is a project's settings file, for the roots of this machine's sessions besides the window's own.
func projectFile(root string) *settingsFile {
	return &settingsFile{path: filepath.Join(root, ".oriel", "settings.toml"), layer: registry.LayerProject, at: registry.At{Project: root}, trust: true}
}

// addOthers reads the project files of repos sessions here work in, so their project values apply to them.
func (s *settingsPlugin) addOthers() {
	s.othersMu.Lock()
	defer s.othersMu.Unlock()
	for _, root := range projectRoots(s.sh.cfg.WorkDir, s.h.State()) {
		if root == s.sh.cfg.WorkDir || s.others[root] != nil {
			continue
		}
		if s.others == nil {
			s.others = map[string]*settingsFile{}
		}
		s.others[root] = projectFile(root)
	}
}

// loadAll reads each file that changed since it was last read, and reports whether any did.
func (s *settingsPlugin) loadAll() bool {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	s.addOthers()
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

// fileKeys are a file's keys as option names; a map option's table is its value.
func fileKeys(o registry.Options, m map[string]any) []settingsfile.Key {
	return settingsfile.Keys(m, func(name string) bool {
		sp, ok := o.Spec(name)
		return ok && (sp.Type == registry.Map || sp.Type == registry.Table)
	})
}

func (s *settingsPlugin) load(f *settingsFile) bool {
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
	if f.trust && len(src) > 0 && !s.trust().Trusted(f.path, src) {
		f.untrusted = true
		s.apply(f, nil)
		id := "settings.untrusted"
		if f != s.project {
			id += ":" + f.path
		}
		s.h.Notify(core.Notice{ID: id, Severity: core.SevWarning, Title: "Project settings",
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
	for _, e := range fileKeys(o, doc.Value()) {
		sp, ok := o.Spec(e.Name)
		if !ok {
			f.unknown = append(f.unknown, e.Name)
			continue
		}
		v, err := fromTOML(sp, e.Value)
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
func (s *settingsPlugin) apply(f *settingsFile, vals map[string]any) (errs []string) {
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
	case registry.List:
		if l, ok := settingsfile.Strings(v); ok {
			return l, nil
		}
	case registry.Map:
		if m, ok := settingsfile.StringMap(v); ok {
			return m, nil
		}
	case registry.Table:
		if m, ok := v.(map[string]any); ok {
			return m, nil
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
	return [...]string{"bool", "int", "float", "string", "enum", "duration", "key", "list of strings", "map of strings", "table"}[sp.Type]
}

// --- trust ---

func (s *settingsPlugin) trust() settingsfile.Trust { return settingsfile.Trust{Path: s.trustPath} }

func (s *settingsPlugin) trustFile(path string) error {
	var f *settingsFile
	for _, x := range s.files() {
		if x.trust && x.path == path {
			f = x
		}
	}
	if f == nil {
		return fmt.Errorf("%s isn't the settings file of a project here", path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := s.trust().Add(path, src); err != nil {
		return err
	}
	f.mu.Lock()
	f.loaded = false
	f.mu.Unlock()
	s.load(f)
	return nil
}

// --- writing ---

func (s *settingsPlugin) scopeArg(name string) int {
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
func (s *settingsPlugin) fileFor(scope int) (*settingsFile, error) {
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

func (s *settingsPlugin) sessionAt() registry.At {
	return registry.At{Session: s.h.Here().Session}
}

// shadow is the layer above scope's that decides name here, and where it was set: a change at scope wouldn't show.
func (s *settingsPlugin) shadow(name string, scope int) (registry.Layer, string, bool) {
	at := s.atFor(scope)
	_, l, err := s.h.Options().Get(name, at)
	if err != nil || l <= s.layerFor(scope) {
		return 0, "", false
	}
	return l, s.h.Options().Source(name, at), true
}

// readOnly says why the page can't change name at scope: init.lua sets it, and wins over the user file. The
// project file and the session come after init.lua, so they can.
func (s *settingsPlugin) readOnly(name string, scope int) error {
	if l, src, ok := s.shadow(name, scope); ok && l == registry.LayerLua {
		return fmt.Errorf("%s is set in %s, and init.lua wins over settings.toml; change it there, or set it for this project or session", name, src)
	}
	return nil
}

func (s *settingsPlugin) set(name string, v any, scope int) error {
	o := s.h.Options()
	sp, ok := o.Spec(name)
	if !ok {
		return fmt.Errorf("no option %q", name)
	}
	if err := s.readOnly(sp.Name, scope); err != nil {
		return err
	}
	if f, ok := v.(float64); ok && sp.Type == registry.Int && f == float64(int(f)) {
		v = int(f)
	}
	if l, ok := settingsfile.Strings(v); ok && sp.Type == registry.List {
		v = l
	}
	if m, ok := settingsfile.StringMap(v); ok && sp.Type == registry.Map {
		v = m
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
	return s.write(f, sp, func(doc *toml.Document, p []string) error {
		if err := doc.Set(p, toTOML(sp, v)); err == nil || sp.Type != registry.Map {
			return err
		}
		// A map written by hand as its own [table] is replaced by the inline one.
		if err := doc.Delete(p...); err != nil {
			return err
		}
		return doc.Set(p, toTOML(sp, v))
	})
}

// setKey sets one key of a table option in the user or project file, or removes it when v is nil: one line.
func (s *settingsPlugin) setKey(name, key string, v any, scope int) error {
	o := s.h.Options()
	sp, ok := o.Spec(name)
	if !ok {
		return fmt.Errorf("no option %q", name)
	}
	if sp.Type != registry.Table {
		return fmt.Errorf("%s isn't a table: set it whole", sp.Name)
	}
	f, err := s.fileFor(scope)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("%s is kept in a settings file: set it at the user or project scope", sp.Name)
	}
	return s.write(f, sp, func(doc *toml.Document, p []string) error {
		if v == nil {
			if _, ok := doc.Get(append(p, key)...); !ok {
				return nil
			}
			return doc.Delete(append(p, key)...)
		}
		return doc.Set(append(slices.Clone(p), key), v)
	})
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

func (s *settingsPlugin) reset(name string, scope int) error {
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

func (s *settingsPlugin) resetWhere(keep func(registry.OptionSpec) bool, scope int) error {
	var errs []error
	for _, sp := range s.h.Options().Specs() {
		// A table is a list its owner manages ([plugins]): resetting settings doesn't empty it.
		if keep(sp) && sp.Type != registry.Table {
			if _, set := s.h.Options().Lookup(sp.Name, s.layerFor(scope), s.atFor(scope)); set {
				errs = append(errs, s.reset(sp.Name, scope))
			}
		}
	}
	return errors.Join(errs...)
}

// write changes the line for sp in f with fn, at the key the file already spells it with (an alias, a dotted key)
// or as its name's tables.
func (s *settingsPlugin) write(f *settingsFile, sp registry.OptionSpec, fn func(doc *toml.Document, p []string) error) error {
	o := s.h.Options()
	err := s.edit(f.path, true, func(doc *toml.Document) error {
		p := strings.Split(sp.Name, ".")
		for _, e := range fileKeys(o, doc.Value()) {
			if other, ok := o.Spec(e.Name); ok && other.Name == sp.Name {
				p = e.Path
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
func (s *settingsPlugin) edit(path string, undo bool, fn func(doc *toml.Document) error) error {
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

func (s *settingsPlugin) undoLast() error {
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

func (s *settingsPlugin) layerFor(scope int) registry.Layer {
	return [...]registry.Layer{registry.LayerUser, registry.LayerProject, registry.LayerSession}[scope]
}

// atFor is where the page reads: where the user is, so a buffer's :set shows too.
func (s *settingsPlugin) atFor(int) registry.At { return s.h.Here() }

// --- the page ---

// sectionOf is an option's section: its Section, or its name's prefix.
func sectionOf(sp registry.OptionSpec) string {
	if sp.Section != "" {
		return sp.Section
	}
	sec, _, _ := strings.Cut(sp.Name, ".")
	return sec
}

func (s *settingsPlugin) show(arg string) {
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
	for _, sec := range registry.SettingsSections(s.h) {
		if sec.Name == arg {
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
	case []string:
		q := make([]string, len(v))
		for i, s := range v {
			q[i] = strconv.Quote(s)
		}
		return "[" + strings.Join(q, ", ") + "]"
	case map[string]string:
		return "{ " + strings.Join(mapEntries(v, tomlKey), ", ") + " }"
	}
	return fmt.Sprint(v)
}

// luaValue is a value as init.lua writes it.
func luaValue(v any) string {
	switch v := v.(type) {
	case []string:
		return "{" + strings.TrimSuffix(strings.TrimPrefix(formatValue(v), "["), "]") + "}"
	case map[string]string:
		return "{ " + strings.Join(mapEntries(v, luaKey), ", ") + " }"
	}
	return formatValue(v)
}

// mapEntries are a map's "key = value"s in key order, each key written by key.
func mapEntries(m map[string]string, key func(string) string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, key(k)+" = "+strconv.Quote(m[k]))
	}
	return out
}

var (
	bareTOMLKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	luaName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func tomlKey(k string) string {
	if bareTOMLKey.MatchString(k) {
		return k
	}
	return strconv.Quote(k)
}

func luaKey(k string) string {
	if luaName.MatchString(k) {
		return k
	}
	return "[" + strconv.Quote(k) + "]"
}

// matches is the page's search: words of the name, help, aliases or value; @modified, @project and @plugin:id filter.
func (s *settingsPlugin) matches(sp registry.OptionSpec, at registry.At) bool {
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

func (s *settingsPlugin) build(c *ui.Ctx) {
	if !s.open || c.Theme().IsProbe() {
		return
	}
	th := c.Theme()
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
	own := map[string]registry.SettingsSection{}
	for _, sec := range registry.SettingsSections(s.h) {
		own[sec.Name] = sec
		n := 0
		if sec.Count != nil {
			registry.Call(func() error { n = sec.Count(); return nil })
		}
		sections[sec.Name] += n
	}
	names := slices.Sorted(maps.Keys(sections))
	if s.section == "" && len(names) > 0 {
		s.section = names[0]
	}
	sec, isOwn := own[s.section]
	isOwn = isOwn && s.query == ""
	var rows []registry.OptionSpec
	for _, sp := range specs {
		if s.query != "" && s.matches(sp, at) || s.query == "" && sectionOf(sp) == s.section {
			rows = append(rows, sp)
		}
	}
	widget.Page(c, "settings", &s.open, widget.PageOpts{Top: titleH, KeyMode: "settings", Label: "Settings", Help: "shell/settings"}, func(c *ui.Ctx) {
		s.header(c, th)
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Row("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("sections", ui.Style{W: ui.Px(240), H: ui.Flex(1), Pad: ui.PadXY(8, 12), Gap: 2, Fill: th.C(tThemePanel), ScrollY: true}, func(c *ui.Ctx) {
				c.Text("t", "SECTIONS", capLabel(th))
				c.Box("g", ui.Style{H: ui.Px(6)}, nil)
				for _, name := range names {
					on := s.query == "" && name == s.section
					fill, fg := render.Color{}, theme.FgMuted
					if on {
						fill, fg = th.C(theme.AccentBg), theme.Fg
					}
					if widget.Clickable(c, name, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center, Fill: fill,
						Radius: 5, Help: "shell/settings#sections", Role: ui.RoleListItem, Label: name, Selected: on}, func(c *ui.Ctx) {
						c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}, Color: th.C(fg)})
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if n := sections[name]; n > 0 {
							c.Text("m", fmt.Sprintf("● %d", n), monoAt(th, 10, tSetModified))
						}
					}).Clicked {
						s.section, s.query = name, ""
					}
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
					if s.query == "" && !isOwn && widget.Button(c, "resetsec", "Reset section", widget.ButtonOpts{Ghost: true, Help: "shell/settings#reset"}) {
						s.fail(s.resetWhere(func(sp registry.OptionSpec) bool { return sectionOf(sp) == s.section }, s.scope))
					}
				})
				if isOwn {
					c.Column("own", ui.Style{W: ui.Flex(1), Gap: 4}, sec.Build)
				}
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

func (s *settingsPlugin) header(c *ui.Ctx, th *theme.Resolved) {
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(16, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", "SETTINGS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.SemiBold, Tracking: .14}, Color: th.C(theme.Accent)})
		c.Box("q", ui.Style{W: ui.Size{Kind: ui.Grow, Value: 1, Max: 420}}, func(c *ui.Ctx) {
			widget.Input(c, "in", &s.query, widget.InputOpts{Label: "Search settings", Placeholder: "search · @modified · @project · @plugin:id",
				Help: "shell/settings#search"})
		})
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("scope", "EDITING", capLabel(th))
		scope := s.scope
		if widget.Segmented(c, "scopes", scopeNames, &scope, widget.SegmentedOpts{Help: "shell/settings#scope"}) {
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
		if widget.Segmented(c, "as", []string{"CONTROLS", "TOML"}, &toml, widget.SegmentedOpts{Help: "shell/settings#toml"}) {
			s.asTOML = toml == 1
		}
	})
}

func (s *settingsPlugin) sourceLabel(name string, l registry.Layer, at registry.At) string {
	switch l {
	case registry.LayerDefault:
		if sp, ok := s.h.Options().Spec(name); ok && sp.Follows != "" {
			return "follows " + sp.Follows
		}
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

func (s *settingsPlugin) row(c *ui.Ctx, th *theme.Resolved, sp registry.OptionSpec, at registry.At) {
	o := s.h.Options()
	v, l, _ := o.Get(sp.Name, at)
	_, setHere := o.Lookup(sp.Name, s.layerFor(s.scope), at)
	lua := l == registry.LayerLua
	canScope := sp.Scopes&[...]registry.Scope{registry.ScopeUser, registry.ScopeProject, registry.ScopeSession}[s.scope] != 0
	disabled := s.readOnly(sp.Name, s.scope) != nil || !canScope
	shadowL, shadowSrc, shadowed := s.shadow(sp.Name, s.scope)
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
				c.Text("lua", fmt.Sprintf("oriel.option[%q] = %s", sp.Name, luaValue(toTOML(sp, v))), monoAt(th, 11, theme.FgFaint))
			} else if sp.Help != "" {
				widget.Wrap(c, "h", sp.Help, 560, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: th.C(theme.FgFaint)})
			}
			if shadowed {
				s.shadowNote(c, th, shadowL, shadowSrc)
			}
		})
		srcTok, src := tSetSource, s.sourceLabel(sp.Name, l, at)
		if lua {
			srcTok = tSetLua
		}
		if t, ok := s.saved[sp.Name]; ok && c.Now().Before(t.Add(settingsSavedFor)) {
			srcTok, src = tSetModified, "✓ saved"
			c.WakeAt(t.Add(settingsSavedFor))
		}
		c.Text("src", src, monoAt(th, 10.5, srcTok))
		fade := float32(0)
		if disabled {
			fade = .55
		}
		ctlW := float32(260)
		if sp.Type == registry.List && sp.Choices == nil {
			ctlW = 340
		}
		c.Box("ctl", ui.Style{W: ui.Px(ctlW), Main: ui.End, Fade: fade}, func(c *ui.Ctx) { s.control(c, th, sp, v, disabled) })
		if widget.Button(c, "reset", "↺", widget.ButtonOpts{Ghost: true, Disabled: !setHere || disabled, Help: "shell/settings#reset"}) {
			s.fail(s.reset(sp.Name, s.scope))
		}
	})
}

// shadowNote says what decides a row's value over the layer the page edits: init.lua, with a link to the line, or
// a later layer.
func (s *settingsPlugin) shadowNote(c *ui.Ctx, th *theme.Resolved, l registry.Layer, src string) {
	c.Row("shadow", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		if l != registry.LayerLua {
			c.Text("t", "overridden here by "+strings.TrimSuffix(s.sourceLabel("", l, registry.At{})+" · "+src, " · "), monoAt(th, 10.5, theme.FgFaint))
			return
		}
		c.Text("t", "init.lua wins over settings.toml", monoAt(th, 10.5, tSetLua))
		file, line := script.SplitLoc(src)
		if line == 0 {
			return
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(s.sh.cfg.ConfigDir, file)
		}
		if widget.Clickable(c, "open", ui.Style{Dir: ui.Row, Label: "Open " + src, Help: "shell/settings#rows"}, func(c *ui.Ctx) {
			c.Text("t", "Open "+src, monoAt(th, 10.5, theme.Accent))
		}).Clicked {
			s.fail(s.h.Run(context.Background(), "editor.open", registry.Args{"path": file, "line": line}))
		}
	})
}

// control is a row's typed control: a toggle, choices, a list or map editor, or a field that's checked when entered.
func (s *settingsPlugin) control(c *ui.Ctx, th *theme.Resolved, sp registry.OptionSpec, v any, disabled bool) {
	const help = "shell/settings#rows"
	switch sp.Type {
	case registry.List:
		items, _ := v.([]string)
		if sp.Choices != nil {
			if l, ok := widget.MultiSelect(c, "multi", items, sp.Choices, widget.MultiSelectOpts{Label: sp.Name, Disabled: disabled, Help: help}); ok {
				s.save(sp.Name, l)
			}
			return
		}
		if l, ok := widget.ListEditor(c, "list", items, widget.ListEditorOpts{Label: sp.Name, Disabled: disabled, Help: help, Width: 340,
			Placeholder: "add one, ⏎", Validate: itemCheck(sp)}); ok {
			s.save(sp.Name, l)
		}
		return
	case registry.Map:
		m, _ := v.(map[string]string)
		if nm, ok := widget.MapField(c, "map", m, widget.MapEditorOpts{Title: sp.Name, KeyLabel: sp.KeyLabel, ValueLabel: sp.ValueLabel,
			Disabled: disabled, Help: help, Validate: entryCheck(sp)}); ok {
			s.save(sp.Name, nm)
		}
		return
	case registry.Table:
		m, _ := v.(map[string]any)
		c.Text("table", fmt.Sprintf("%d %s · in settings.toml", len(m), plural(len(m), "entry", "entries")), monoFont(11, th.C(theme.FgMuted)))
		return
	case registry.Bool:
		on, _ := v.(bool)
		if widget.Toggle(c, "toggle", on, widget.ToggleOpts{Label: sp.Name, Disabled: disabled, Help: "shell/settings#rows"}) {
			s.save(sp.Name, !on)
		}
	case registry.Enum:
		cur, _ := v.(string)
		if len(sp.Values) <= 4 {
			i := slices.Index(sp.Values, cur)
			if widget.Segmented(c, "choices", upper(sp.Values), &i, widget.SegmentedOpts{Help: "shell/settings#rows"}) && !disabled {
				s.save(sp.Name, sp.Values[i])
			}
			return
		}
		s.dropdown(c, sp, cur, sp.Values, disabled)
	case registry.String:
		if sp.Choices != nil {
			cur, _ := v.(string)
			s.dropdown(c, sp, cur, sp.Choices(), disabled)
			return
		}
		s.field(c, sp, v, disabled)
	default:
		s.field(c, sp, v, disabled)
	}
}

// save sets an option from the page at the scope it edits, and marks its row saved.
func (s *settingsPlugin) save(name string, v any) {
	if err := s.set(name, v, s.scope); err != nil {
		s.fail(err)
		return
	}
	if s.saved == nil {
		s.saved = map[string]time.Time{}
	}
	if u := s.h.UI(); u != nil {
		s.saved[name] = u.Now()
	}
}

// itemCheck is a list option's Validate asked about one item.
func itemCheck(sp registry.OptionSpec) func(string) error {
	if sp.Validate == nil {
		return nil
	}
	return func(item string) error { return sp.Validate([]string{item}) }
}

// entryCheck is a map option's Validate asked about one entry.
func entryCheck(sp registry.OptionSpec) func(k, v string) error {
	if sp.Validate == nil {
		return nil
	}
	return func(k, v string) error { return sp.Validate(map[string]string{k: v}) }
}

// dropdown is a menu of values for a row.
func (s *settingsPlugin) dropdown(c *ui.Ctx, sp registry.OptionSpec, cur string, values []string, disabled bool) {
	if v, ok := widget.Select(c, "drop", cur, values, widget.SelectOpts{Label: sp.Name, Disabled: disabled, Help: "shell/settings#rows"}); ok {
		s.save(sp.Name, v)
	}
}

// field is a text field for a value, checked as it's typed and set when entered, when it loses focus or once
// typing pauses. A number with a range and a step has a slider beside it too, set when it's let go.
func (s *settingsPlugin) field(c *ui.Ctx, sp registry.OptionSpec, v any, disabled bool) {
	text := fieldText(v)
	fid := c.Next("field")
	st := ui.State[fieldState](c, fid)
	line := ui.Child(fid, "line")
	in := c.Interaction(ui.Child(line, "in"))
	if !in.Focused && !st.sliding && st.from != text {
		st.text, st.from, st.typing = text, text, false
	}
	var accept func(rune) bool
	switch sp.Type {
	case registry.Int:
		accept = widget.Digits
	case registry.Float:
		accept = widget.Decimal
	}
	slider := sp.Step > 0 && sp.Max > sp.Min
	w := float32(200)
	if slider {
		w = 260
	}
	c.Column("field", ui.Style{W: ui.Px(w), Gap: 4}, func(c *ui.Ctx) {
		var res widget.InputResult
		c.Row("line", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			if slider {
				sid := c.Next("slide")
				if !st.sliding {
					st.slide, _ = toFloat(v)
				}
				if widget.Slider(c, "slide", &st.slide, widget.SliderOpts{Min: sp.Min, Max: sp.Max, Step: sp.Step, Width: ui.Flex(1),
					Label: sp.Name, Help: "shell/settings#rows"}) && !disabled {
					st.sliding, st.text = true, fieldText(numberAs(sp, st.slide))
				}
				if st.sliding && !c.Interaction(sid).Held {
					st.sliding = false
					s.save(sp.Name, numberAs(sp, st.slide))
				}
			}
			iw := ui.Flex(1)
			if slider {
				iw = ui.Px(64)
			}
			before := st.text
			res = widget.Input(c, "in", &st.text, widget.InputOpts{Label: sp.Name, Width: iw, Disabled: disabled, Help: "shell/settings#rows",
				Accept: accept, Invalid: st.bad != nil})
			if st.text != before {
				st.typing, st.typed = true, c.Now()
			}
		})
		val, bad := parseField(sp, st.text)
		if bad == nil {
			bad = sp.Check(val)
		}
		st.bad = bad
		blurred := st.focused && !in.Focused
		st.focused = in.Focused
		switch {
		case res.Cancelled:
			st.text, st.typing = text, false
		case res.Submitted:
			st.typing = false
			if bad != nil {
				s.fail(bad)
			} else if st.text != text {
				s.save(sp.Name, val)
			}
		case st.typing && (blurred || !c.Now().Before(st.typed.Add(settingsTypingPause))):
			st.typing = false
			if bad == nil && st.text != text {
				s.save(sp.Name, val)
			}
		case st.typing:
			c.WakeAt(st.typed.Add(settingsTypingPause))
		}
		if bad != nil && st.text != text {
			widget.Wrap(c, "bad", bad.Error(), w, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 11}, Color: c.Theme().C(theme.Error)})
		}
	})
}

// settingsTypingPause is how long a field waits after the last key before it sets what was typed.
const settingsTypingPause = 800 * time.Millisecond

// fieldState is a field's text, the value it was last filled from, and its slider's value while it's moved.
type fieldState struct {
	text, from string
	typing     bool      // text was typed and isn't set yet
	typed      time.Time // the last key that changed it
	focused    bool
	bad        error
	slide      float64
	sliding    bool
}

// toFloat is an Int's or a Float's value as a float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// numberAs is f as sp's type: an Int rounds.
func numberAs(sp registry.OptionSpec, f float64) any {
	if sp.Type == registry.Int {
		return int(math.Round(f))
	}
	return f
}

func upper(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

// fieldText is a value as its field shows it.
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

func (s *settingsPlugin) footer(c *ui.Ctx, th *theme.Resolved) {
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

func (s *settingsPlugin) fail(err error) {
	if err != nil {
		s.h.Notify(core.Notice{ID: "settings.set", Severity: core.SevError, Title: "Settings", Text: err.Error(), Key: true})
	}
}
