package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// PluginsOption is the [plugins] table: the installed plugins, by id (catalog.md).
const PluginsOption = "plugins"

// catalogPlugin is builtin:catalog, the plugin manager (catalog.md, "Install by editing the file"): it installs
// what [plugins] lists in your settings.toml and a trusted project's, keeps each file's oriel-lock.toml, and loads
// each plugin the lockfile covers. One asking for more than its lock entry stays inactive behind an inbox card that
// opens the consent screen. A line taken out of [plugins] stops the plugin, deletes its files and drops its lock
// entry.
type catalogPlugin struct {
	disposer
	sh *shell
	h  plugin.Host

	mu       sync.Mutex
	syncing  bool
	again    bool
	statuses []catalog.Status         // the last sync's, every file's
	loaded   map[string]*loadedPlugin // what's running, by id
	waiting  map[string]catalog.Status
	failed   map[string]string // why, by id, as last noticed
	busy     bool              // the last sync had something: a plugin listed, loaded or waiting
	resolver catalog.Resolver
	synced   chan struct{} // closed and made again after each sync, for tests
}

type loadedPlugin struct {
	dir  string
	stop registry.Disposable
}

// pluginFile is a settings file's [plugins] with its manager.
type pluginFile struct {
	entries []catalog.Entry
	m       *catalog.Manager
	project string
}

func (x *catalogPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "catalog"} }

func (x *catalogPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.loaded, x.waiting, x.failed = map[string]*loadedPlugin{}, map[string]catalog.Status{}, map[string]string{}
	x.synced = make(chan struct{})
	x.add(h.Option(registry.OptionSpec{Name: PluginsOption, Type: registry.Table, Default: map[string]any{}, Section: "plugins",
		Scopes: registry.ScopeUser | registry.ScopeProject,
		Help:   "The installed plugins, by id: a version range from the registries, or { git = … } or { path = … }; editing it installs and uninstalls"}))
	str := func(a registry.Args, k string) string { v, _ := a[k].(string); return v }
	for _, c := range []registry.Command{
		{ID: "plugins.sync", Title: "Install and load what [plugins] lists now", Run: func(registry.Ctx, registry.Args) error {
			x.kick()
			return nil
		}},
		{ID: "plugins.install", Title: "Install {id}: one line in [plugins] at {scope} (user by default), a {version} range or a {git} or {path} source",
			Run: func(_ registry.Ctx, a registry.Args) error {
				var v any = cmpOr(str(a, "version"), "*")
				switch {
				case str(a, "git") != "":
					v = map[string]any{"git": str(a, "git")}
				case str(a, "path") != "":
					v = map[string]any{"path": str(a, "path")}
				}
				return h.Run(context.Background(), "settings.set", registry.Args{"name": PluginsOption, "key": str(a, "id"), "value": v,
					"scope": cmpOr(str(a, "scope"), "user")})
			}},
		{ID: "plugins.uninstall", Title: "Uninstall {id}: its line leaves [plugins] at {scope} (user by default)",
			Run: func(_ registry.Ctx, a registry.Args) error {
				return h.Run(context.Background(), "settings.reset", registry.Args{"name": PluginsOption, "key": str(a, "id"),
					"scope": cmpOr(str(a, "scope"), "user")})
			}},
		{ID: "plugins.review", Title: "Open the consent screen for {id}, which waits for it", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.review(str(a, "id"))
		}},
		{ID: "plugins.accept", Title: "Record consent to the plugin waiting in {dir}, and load it", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.accept(str(a, "dir"))
		}},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Contribute(registry.InboxSource{Name: "plugins", Cards: x.cards, Source: "builtin:catalog"}))
	cancel := h.Options().Watch(PluginsOption, x.kick)
	x.add(disposeFn(cancel))
	x.add(disposeFn(func() {
		x.mu.Lock()
		loaded := x.loaded
		x.loaded = map[string]*loadedPlugin{}
		x.mu.Unlock()
		for _, l := range loaded {
			l.stop.Dispose()
		}
	}))
	x.kick()
	return nil
}

// SetResolver is how the registry client resolves registry entries (PL-21).
func (x *catalogPlugin) setResolver(r catalog.Resolver) {
	x.mu.Lock()
	x.resolver = r
	x.mu.Unlock()
	x.kick()
}

// kick syncs off the loop, once at a time: a change while it runs syncs again after.
func (x *catalogPlugin) kick() {
	x.mu.Lock()
	if x.syncing {
		x.again = true
		x.mu.Unlock()
		return
	}
	x.syncing = true
	x.mu.Unlock()
	go func() {
		for {
			files := x.files()
			var sts []catalog.Status
			var removed []string
			for i, f := range files {
				others := files[:i:i]
				others = append(others, files[i+1:]...)
				keep := func(id string) bool {
					for _, o := range others {
						if slices.ContainsFunc(o.entries, func(e catalog.Entry) bool { return e.ID == id }) {
							return true
						}
					}
					return false
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				s, r, err := f.m.Sync(ctx, f.entries, keep)
				cancel()
				if err != nil {
					x.h.Notify(core.Notice{ID: "plugins.sync", Severity: core.SevError, Title: "Plugins", Text: err.Error()})
				}
				sts = append(sts, s...)
				removed = append(removed, r...)
			}
			x.mu.Lock()
			idle := len(sts) == 0 && !x.busy
			x.mu.Unlock()
			if !idle {
				done := make(chan struct{})
				x.h.Do(func() { x.apply(sts); close(done) })
				<-done
			}
			x.mu.Lock()
			if !x.again {
				x.syncing = false
				close(x.synced)
				x.synced = make(chan struct{})
				x.mu.Unlock()
				return
			}
			x.again = false
			x.mu.Unlock()
		}
	}()
}

// files are your settings.toml's [plugins] and the window's project's, which applies only once it's trusted (the
// settings layer has no value until then). An id in both is yours.
func (x *catalogPlugin) files() []pluginFile {
	cfg := x.sh.cfg
	store := catalog.Store{Dir: filepath.Join(cmpOr(cfg.DataDir, DefaultDataDir()), "plugins")}
	x.mu.Lock()
	res := x.resolver
	x.mu.Unlock()
	o := x.h.Options()
	read := func(l registry.Layer, at registry.At, file string) []catalog.Entry {
		v, _ := o.Lookup(PluginsOption, l, at)
		t, _ := v.(map[string]any)
		es, errs := catalog.ParseEntries(t)
		for _, err := range errs {
			x.h.Notify(core.Notice{ID: "plugins.entry", Severity: core.SevWarning, Title: "Plugins", Location: file, Text: err.Error()})
		}
		return es
	}
	userFile := filepath.Join(cfg.ConfigDir, "settings.toml")
	out := []pluginFile{{entries: read(registry.LayerUser, registry.At{}, userFile),
		m: &catalog.Manager{Store: store, Lock: filepath.Join(cfg.ConfigDir, catalog.LockFile), Resolver: res}}}
	if root := cfg.WorkDir; root != "" {
		file := filepath.Join(root, ".oriel", "settings.toml")
		var es []catalog.Entry
		for _, e := range read(registry.LayerProject, registry.At{Project: root}, file) {
			if !slices.ContainsFunc(out[0].entries, func(u catalog.Entry) bool { return u.ID == e.ID }) {
				e.Project = root
				es = append(es, e)
			}
		}
		out = append(out, pluginFile{entries: es, project: root,
			m: &catalog.Manager{Store: store, Lock: filepath.Join(root, ".oriel", catalog.LockFile), Resolver: res}})
	}
	for i := range out {
		for j := range out[i].entries {
			out[i].entries[j].File = userFile
			if out[i].project != "" {
				out[i].entries[j].File = filepath.Join(out[i].project, ".oriel", "settings.toml")
			}
		}
	}
	return out
}

// apply loads what's ready, stops what's gone or turned off, and puts up a card for what waits for consent. A
// plugin waiting on an update's consent keeps running the version you accepted. It runs on the loop.
func (x *catalogPlugin) apply(sts []catalog.Status) {
	x.mu.Lock()
	x.statuses = sts
	x.mu.Unlock()
	listed := map[string]bool{}
	waiting := map[string]catalog.Status{}
	for _, st := range sts {
		id := st.Entry.ID
		listed[id] = true
		switch st.State {
		case catalog.Ready:
			delete(x.failed, id)
			x.h.Dismiss("plugins.failed." + id)
			x.load(st)
		case catalog.Disabled:
			x.stop(id)
		case catalog.Consent:
			waiting[id] = st
		case catalog.Failed:
			if msg := st.Err.Error(); x.failed[id] != msg {
				x.failed[id] = msg
				x.h.Notify(core.Notice{ID: "plugins.failed." + id, Severity: core.SevError, Title: "Plugin " + id, Location: st.Entry.File, Text: msg})
			}
		}
	}
	for id := range x.loaded {
		if !listed[id] {
			x.stop(id)
		}
	}
	x.mu.Lock()
	x.waiting = waiting
	x.busy = len(sts) > 0 || len(x.loaded) > 0 || len(waiting) > 0
	x.mu.Unlock()
	x.h.Redraw()
}

// load starts st's plugin, in place of another version of it.
func (x *catalogPlugin) load(st catalog.Status) {
	id := st.Entry.ID
	if l := x.loaded[id]; l != nil {
		if l.dir == st.Dir {
			return
		}
		x.stop(id)
	}
	p := DirPlugin(st.Dir, PluginOpts{Grants: st.Grants(), Data: filepath.Join(x.sh.cfg.StateDir, "plugins", id)})
	stop, err := x.h.Load(p)
	if err != nil {
		x.h.Notify(core.Notice{ID: "plugins.failed." + id, Severity: core.SevError, Title: "Plugin " + id, Text: err.Error()})
		return
	}
	x.loaded[id] = &loadedPlugin{dir: st.Dir, stop: stop}
}

func (x *catalogPlugin) stop(id string) {
	if l := x.loaded[id]; l != nil {
		delete(x.loaded, id)
		l.stop.Dispose()
	}
}

// cards are the plugins waiting for consent.
func (x *catalogPlugin) cards() []registry.InboxCard {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []registry.InboxCard
	for _, id := range sortedKeys(x.waiting) {
		st := x.waiting[id]
		detail := "It asks for more than oriel-lock.toml covers, so it isn't loaded until you accept."
		if st.From != nil {
			detail = fmt.Sprintf("v%s asks for more than v%s, which keeps running until you accept.", st.Manifest.Version, st.From.Version)
		}
		sub := "plugins · " + st.Locked.Badge
		if st.Entry.Project != "" {
			sub += " · " + filepath.Base(st.Entry.Project)
		}
		out = append(out, registry.InboxCard{ID: id, Type: "plugin-consent", Icon: "⚑", Tone: "accent", Interrupt: true,
			Kicker: "PLUGIN · WAITING FOR CONSENT", Title: id, Sub: sub, Detail: detail, Note: st.Warning,
			Actions: []registry.InboxAction{
				{Label: "Review", Key: "r", Primary: true, Command: "plugins.review", Args: registry.Args{"id": id}},
				{Label: "Uninstall", Key: "u", Command: "plugins.uninstall", Args: registry.Args{"id": id, "scope": scopeOf(st.Entry)}},
			}})
	}
	return out
}

func scopeOf(e catalog.Entry) string {
	if e.Project != "" {
		return "project"
	}
	return "user"
}

// review opens the consent screen for a plugin waiting for it.
func (x *catalogPlugin) review(id string) error {
	x.mu.Lock()
	st, ok := x.waiting[id]
	x.mu.Unlock()
	if !ok {
		return fmt.Errorf("%s isn't waiting for consent", id)
	}
	var facts []any
	for _, f := range st.Facts {
		facts = append(facts, map[string]any{"label": f.Label, "value": f.Value})
	}
	return x.h.Run(context.Background(), "plugins.consent", registry.Args{"dir": st.Dir, "from": st.FromDir, "source": st.Locked.Source,
		"badge": st.Locked.Badge, "warning": st.Warning, "facts": facts, "then": "plugins.accept"})
}

// accept records consent to the plugin waiting in dir, then loads it.
func (x *catalogPlugin) accept(dir string) error {
	x.mu.Lock()
	var st catalog.Status
	found := false
	for _, s := range x.waiting {
		if s.Dir == dir {
			st, found = s, true
		}
	}
	x.mu.Unlock()
	if !found {
		return fmt.Errorf("no plugin waits for consent in %s", dir)
	}
	f := x.fileOf(st.Entry)
	if err := f.m.Accept(st); err != nil {
		return err
	}
	x.kick()
	return nil
}

func (x *catalogPlugin) fileOf(e catalog.Entry) pluginFile {
	fs := x.files()
	if e.Project != "" && len(fs) > 1 {
		return fs[1]
	}
	return fs[0]
}

// Statuses are the last sync's, for the Plugins page.
func (x *catalogPlugin) Statuses() []catalog.Status {
	x.mu.Lock()
	defer x.mu.Unlock()
	return slices.Clone(x.statuses)
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
