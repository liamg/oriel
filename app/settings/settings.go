// Package settings is the settings pages: the options, themes and their editor, prompts, secrets, sandboxes, plugins
// and their registry, the consent asked before a plugin runs, and About.
package settings

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/atomicfile"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/settingsfile"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/toml"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/watch"
)

// SettingsPoll is how often the settings files are read for hand edits when the OS can't watch them.
const SettingsPoll = time.Second

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
	sub   watch.Sub[[]byte] // its contents, from the app's freshness manager

	mu        sync.Mutex
	wrote     time.Time // when the page last wrote it: a reading begun before is older than what it applied
	loaded    bool
	hash      [32]byte
	applied   map[string]bool // the options it set
	err       error           // why it doesn't parse
	unknown   []string        // keys no option has
	untrusted bool
}

// Plugin is settings.toml storage (docs/configuration.md) and the settings page (:Settings, ⌘,): every option from
// the registry by section, with typed controls, where each value comes from, modified markers and reset. Edits write
// the one line they change.
type Plugin struct {
	kit.Disposer
	ConfigDir string // the user's config directory
	h         plugin.Host

	user, project *settingsFile // the user's, and the window's directory's project's (loadMu, othersMu)
	othersMu      sync.Mutex
	others        map[string]*settingsFile // other projects' files, by root: the repos of this machine's sessions
	trustPath     string
	files         *watch.Manager // reads the files, and again on hand edits
	filesDone     func()
	loadMu        sync.Mutex
	specs         int       // how many options there were at the last read
	work          work.Work // a move's swap of project files, off the loop

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

func (s *Plugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "settings"} }

func (s *Plugin) Activate(_ context.Context, h plugin.Host) error {
	s.Add(h.Contribute(registry.Busy{Name: "settings", Busy: s.work.Busy}))
	s.h = h
	conf := s.ConfigDir
	s.files, s.filesDone = kit.FileManager(h, "settings", SettingsPoll)
	s.trustPath = filepath.Join(conf, "trusted.toml")
	s.othersMu.Lock() // a file's first reading may load them all already
	s.user = s.track(&settingsFile{path: filepath.Join(conf, "settings.toml"), layer: registry.LayerUser})
	if root := kit.WindowDir(h); root != "" {
		s.project = s.projectFile(root)
	}
	s.othersMu.Unlock()
	s.Add(h.Contribute(kit.PagePlace("settings", "Settings", "settings.open", &s.open)))
	for _, c := range s.commands() {
		s.Add(h.Command(c))
	}
	s.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-,>", Command: "settings.open", Desc: "Settings"}))
	s.Add(h.View(kit.OverlayPrefix+"settings", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "settings", Fn: s.build}
	})))
	s.addOthers()
	s.Reload() // the plugins after it start with the values the files hold
	// The swap waits for a read under way, so it's off the loop; the window waits behind its cover for the new
	// project's values.
	s.Add(kit.OnDirChanged(h, "the project settings of the directory", func(kit.DirChanged) {
		release := func() {}
		if w, ok := kit.WindowOf(h); ok {
			release = w.Hold()
		}
		s.work.Go(func(ctx context.Context) {
			defer release()
			s.follow(ctx)
		})
	}))
	s.Add(registry.DisposeFunc(s.work.Stop))
	s.Add(h.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev.(type) {
		case core.SessionOpened, core.SessionSynced:
			s.kick() // a session's project may have a file of its own
		}
	}))
	s.Add(h.Options().Registered(s.kick)) // a new option may take a key the files had waiting
	s.Add(registry.DisposeFunc(s.untrack))
	return nil
}

// track subscribes to f's contents: each new reading loads the files again.
func (s *Plugin) track(f *settingsFile) *settingsFile {
	f.sub = watch.SubscribeFunc(s.files, watch.File(f.path), watch.Need{}, "settings", s.kick)
	return f
}

// untrack ends the files' subscriptions, once the loads under way are done.
func (s *Plugin) untrack() {
	s.work.Stop()
	for _, f := range s.allFiles() {
		f.sub.Close()
	}
	s.filesDone()
}

// kick loads the files again off the loop, redrawing when a value changed.
func (s *Plugin) kick() {
	s.work.Go(func(context.Context) {
		if s.loadAll() {
			s.h.Redraw()
		}
	})
}

// fileReadLongest is how long a read of the files waits for a file.
const fileReadLongest = time.Second

// fresh waits for a reading of each of files begun now.
func (s *Plugin) fresh(ctx context.Context, files ...*settingsFile) {
	ctx, cancel := context.WithTimeout(ctx, fileReadLongest)
	defer cancel()
	var wg sync.WaitGroup
	for _, f := range files {
		wg.Go(func() { f.sub.Fresh(ctx) })
	}
	wg.Wait()
}

func (s *Plugin) commands() []registry.Command {
	str := func(a registry.Args, k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }
	return []registry.Command{
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
		{ID: "settings.reload", Title: "Read the settings files again", Run: func(registry.Ctx, registry.Args) error { s.Reload(); return nil }},
		{ID: "settings.trust", Title: "Trust a project's settings file, as it is now: {path}",
			Run: func(_ registry.Ctx, a registry.Args) error { return s.trustFile(str(a, "path")) }},
	}
}

func (s *Plugin) allFiles() []*settingsFile {
	s.othersMu.Lock()
	defer s.othersMu.Unlock()
	out := []*settingsFile{s.user}
	if s.project != nil {
		out = append(out, s.project)
	}
	for _, root := range slices.Sorted(maps.Keys(s.others)) {
		out = append(out, s.others[root])
	}
	return out
}

// projectFile is a project's settings file, for the roots of this machine's sessions besides the window's own.
func (s *Plugin) projectFile(root string) *settingsFile {
	return s.track(&settingsFile{path: filepath.Join(root, ".oriel", "settings.toml"), layer: registry.LayerProject, at: registry.At{Project: root}, trust: true})
}

// follow makes the window's new directory's project file the project layer the page edits: the one it left stays
// with the other projects', its values still applying to it.
func (s *Plugin) follow(ctx context.Context) {
	root := kit.WindowDir(s.h)
	s.loadMu.Lock()
	s.othersMu.Lock()
	if s.others == nil {
		s.others = map[string]*settingsFile{}
	}
	if s.project != nil {
		s.others[s.project.at.Project] = s.project
	}
	s.project = nil
	tracked := false // its subscription has kept it up to date: no read to wait for
	if root != "" {
		if f, ok := s.others[root]; ok {
			s.project, tracked = f, true
			delete(s.others, root)
		} else {
			s.project = s.projectFile(root)
		}
	}
	project := s.project
	s.othersMu.Unlock()
	s.loadMu.Unlock()
	if project != nil && !tracked {
		s.fresh(ctx, project)
	}
	if s.loadAll() {
		s.h.Redraw()
	}
}

// addOthers reads the project files of repos sessions here work in, so their project values apply to them.
func (s *Plugin) addOthers() {
	s.othersMu.Lock()
	defer s.othersMu.Unlock()
	dir := kit.WindowDir(s.h)
	for _, root := range projectRoots(dir, s.h.State()) {
		if root == dir || s.others[root] != nil {
			continue
		}
		if s.others == nil {
			s.others = map[string]*settingsFile{}
		}
		s.others[root] = s.projectFile(root)
	}
}

// Reload reads the settings files again, as a change to one does; it says whether any value changed.
func (s *Plugin) Reload() bool {
	s.addOthers()
	s.fresh(context.Background(), s.allFiles()...)
	return s.loadAll()
}

// loadAll reads each file that changed since it was last read, and reports whether any did.
func (s *Plugin) loadAll() bool {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	s.addOthers()
	// Options registered since (a plugin's) may now take keys that were unknown: read the files again.
	if n := len(s.h.Options().Specs()); n != s.specs {
		s.specs = n
		for _, f := range s.allFiles() {
			f.mu.Lock()
			f.loaded = f.loaded && len(f.unknown) == 0
			f.mu.Unlock()
		}
	}
	changed := false
	for _, f := range s.allFiles() {
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

// load applies f's latest reading, unless it's the one applied already; it says whether it applied one.
func (s *Plugin) load(f *settingsFile) bool {
	f.mu.Lock()
	since := f.wrote
	f.mu.Unlock()
	src, err, ok := f.sub.Since(since)
	if !ok || err != nil {
		return false // not read yet, or it couldn't be: the last good values stay
	}
	return s.loadSrc(f, src)
}

// written applies src, just written to f by the page, without waiting for f's reading of it.
func (s *Plugin) written(f *settingsFile, src []byte) {
	f.mu.Lock()
	f.wrote = time.Now()
	f.mu.Unlock()
	s.loadSrc(f, src)
}

// loadSrc applies src as f's contents, unless they're what it applied already.
func (s *Plugin) loadSrc(f *settingsFile, src []byte) bool {
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
func (s *Plugin) apply(f *settingsFile, vals map[string]any) (errs []string) {
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
			return registry.ParseDuration(d)
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
		return registry.FormatDuration(d)
	}
	return v
}

func typeName(sp registry.OptionSpec) string {
	return [...]string{"bool", "int", "float", "string", "enum", "duration", "key", "list of strings", "map of strings", "table"}[sp.Type]
}

// --- trust ---

func (s *Plugin) trust() settingsfile.Trust { return settingsfile.Trust{Path: s.trustPath} }

func (s *Plugin) trustFile(path string) error {
	var f *settingsFile
	for _, x := range s.allFiles() {
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
	s.written(f, src)
	return nil
}

// --- writing ---

func (s *Plugin) scopeArg(name string) int {
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
func (s *Plugin) fileFor(scope int) (*settingsFile, error) {
	switch scope {
	case 0:
		return s.user, nil
	case 1:
		s.othersMu.Lock()
		project := s.project
		s.othersMu.Unlock()
		if project == nil {
			return nil, fmt.Errorf("no project is open")
		}
		return project, nil
	}
	return nil, nil
}

func (s *Plugin) sessionAt() registry.At {
	return registry.At{Session: s.h.Here().Session}
}

// shadow is the layer above scope's that decides name here, and where it was set: a change at scope wouldn't show.
func (s *Plugin) shadow(name string, scope int) (registry.Layer, string, bool) {
	at := s.atFor(scope)
	_, l, err := s.h.Options().Get(name, at)
	if err != nil || l <= s.layerFor(scope) {
		return 0, "", false
	}
	return l, s.h.Options().Source(name, at), true
}

// readOnly says why the page can't change name at scope: init.lua sets it, and wins over the user file. The
// project file and the session come after init.lua, so they can.
func (s *Plugin) readOnly(name string, scope int) error {
	if l, src, ok := s.shadow(name, scope); ok && l == registry.LayerLua {
		return fmt.Errorf("%s is set in %s, and init.lua wins over settings.toml; change it there, or set it for this project or session", name, src)
	}
	return nil
}

func (s *Plugin) set(name string, v any, scope int) error {
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
func (s *Plugin) setKey(name, key string, v any, scope int) error {
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

func (s *Plugin) reset(name string, scope int) error {
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

func (s *Plugin) resetWhere(keep func(registry.OptionSpec) bool, scope int) error {
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
func (s *Plugin) write(f *settingsFile, sp registry.OptionSpec, fn func(doc *toml.Document, p []string) error) error {
	o := s.h.Options()
	src, err := s.edit(f.path, true, func(doc *toml.Document) error {
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
	s.written(f, src)
	return nil
}

// edit rewrites the TOML file at path with fn, refusing one that doesn't parse, and gives what the file holds
// after. With undo, the old file is kept.
func (s *Plugin) edit(path string, undo bool, fn func(doc *toml.Document) error) ([]byte, error) {
	src, err := os.ReadFile(path)
	had := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	doc, err := toml.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%s doesn't parse (%v), so nothing was written", path, err)
	}
	if err := fn(doc); err != nil {
		return nil, err
	}
	out := doc.Bytes()
	if string(out) == string(src) {
		return src, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if undo {
		s.undo = append(s.undo, settingsUndo{path: path, src: src, had: had})
	}
	return out, atomicfile.Write(path, out, 0o644) // a crash, or a read, mid-write never finds the settings empty
}

func (s *Plugin) undoLast() error {
	n := len(s.undo)
	if n == 0 {
		return nil
	}
	u := s.undo[n-1]
	s.undo = s.undo[:n-1]
	var err error
	if u.had {
		err = atomicfile.Write(u.path, u.src, 0o644)
	} else {
		err = os.Remove(u.path)
	}
	if err != nil {
		return err
	}
	for _, f := range s.allFiles() {
		if f.path == u.path {
			if f.trust {
				s.trustFile(f.path)
			}
			if !u.had {
				u.src = nil
			}
			s.written(f, u.src)
		}
	}
	return nil
}

func (s *Plugin) layerFor(scope int) registry.Layer {
	return [...]registry.Layer{registry.LayerUser, registry.LayerProject, registry.LayerSession}[scope]
}

// atFor is where the page reads: where the user is, so a buffer's :set shows too.
func (s *Plugin) atFor(int) registry.At { return s.h.Here() }

// --- the page ---

// sectionOf is an option's section: its Section, or its name's prefix.
func sectionOf(sp registry.OptionSpec) string {
	if sp.Section != "" {
		return sp.Section
	}
	sec, _, _ := strings.Cut(sp.Name, ".")
	return sec
}

func (s *Plugin) show(arg string) {
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
	for _, sec := range uireg.SettingsSections(s.h) {
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
		return strconv.Quote(registry.FormatDuration(v))
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
func (s *Plugin) matches(sp registry.OptionSpec, at registry.At) bool {
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
