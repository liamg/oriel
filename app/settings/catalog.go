package settings

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// PluginsOption is the [plugins] table: the installed plugins, by id.
const PluginsOption = "plugins"

// Catalog is builtin:catalog, the plugin manager: it installs
// what [plugins] lists in your settings.toml and a trusted project's, keeps each file's oriel-lock.toml, and loads
// each plugin the lockfile covers. One asking for more than its lock entry stays inactive behind an inbox card that
// opens the consent screen. A line taken out of [plugins] stops the plugin, deletes its files and drops its lock
// entry.
type Catalog struct {
	kit.Disposer
	GitHub *catalog.GitHub // how plugins and registries are read; GitHub's API with your gh login's token if nil
	// ConfigDir and the window's directory hold the settings files, DataDir the installed plugins, StateDir their
	// data; CacheDir is DataDir/cache if "".
	ConfigDir, DataDir, StateDir, CacheDir string
	Bundled                                []plugin.Bundled // shipped in the binary
	Self                                   []string         // the command that runs oriel itself, for bundled plugins' processes; this executable if nil
	h                                      plugin.Host
	work                                   kit.Work // every goroutine it starts, stopped with it

	mu       sync.Mutex
	syncing  bool
	again    bool
	statuses []catalog.Status         // the last sync's, every file's
	loaded   map[string]*loadedPlugin // what's running, by id
	waiting  map[string]catalog.Status
	failed   map[string]string // why, by id, as last noticed
	busy     bool              // the last sync had something: a plugin listed, loaded or waiting
	client   *catalog.Client   // the registries' indexes, once fetched
	updates  map[string]bool   // ids to update at the next sync
	revoked  map[string]catalog.Status
	offers   map[string]catalog.Status // updates waiting to be asked for: [registry] updates = ask
	synced   chan struct{}             // closed and made again after each sync, for tests
	page     pluginsPage

	bundled    map[string]*bundledPlugin // the plugins shipped in the binary, by id
	bundledIDs []string                  // in the order they're bundled
}

// registryEvery is how often the registries' indexes are fetched again.
var registryEvery = time.Hour

// github is how plugins and registries are read: the config's, or GitHub's API with your gh login's token.
func (x *Catalog) github() catalog.GitHub {
	if g := x.GitHub; g != nil {
		return *g
	}
	return catalog.GitHub{Auth: gh.Token}
}

// sources are [registry] sources with their keys: the default registry's built in unless keys pins another.
func (x *Catalog) sources() []catalog.Source {
	o := x.h.Options()
	v, _, _ := o.Get("registry.sources", registry.At{})
	repos, _ := v.([]string)
	kv, _, _ := o.Get("registry.keys", registry.At{})
	keys, _ := kv.(map[string]string)
	var out []catalog.Source
	for _, r := range repos {
		s := catalog.Source{Repo: r}
		if k, err := catalog.ParseKey(keys[r]); err == nil {
			s.Key = k
		} else if r == catalog.DefaultRepo {
			s.Key = catalog.DefaultKey()
		}
		out = append(out, s)
	}
	return out
}

// refresh fetches every registry's index, then syncs.
func (x *Catalog) refresh(ctx context.Context) {
	x.fetchIndexes(ctx)
	x.kick()
}

// fetchIndexes fetches every registry's index. They're fetched only once something needs them: a registry entry in
// [plugins], the Registry tab, plugins.refresh; then hourly.
func (x *Catalog) fetchIndexes(ctx context.Context) {
	g := x.github()
	c := &catalog.Client{Sources: x.sources(), Fetch: g.LatestAsset, Cache: filepath.Join(x.cacheDir(), "registry")}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	err := c.Refresh(ctx)
	cancel()
	if err != nil {
		x.h.Log().Warn("plugin registries", "err", err)
		x.h.Notify(core.Notice{ID: "plugins.registry", Severity: core.SevWarning, Title: "Plugin registries", Text: err.Error()})
	} else {
		x.h.Dismiss("plugins.registry")
	}
	x.mu.Lock()
	x.client = c
	x.mu.Unlock()
}

func (x *Catalog) cacheDir() string {
	if d := x.CacheDir; d != "" {
		return d
	}
	return filepath.Join(x.DataDir, "cache")
}

// resolver is a sync's resolver for registry entries, nil until the indexes are fetched.
func (x *Catalog) newResolver() catalog.Resolver {
	x.mu.Lock()
	c := x.client
	if c == nil {
		x.mu.Unlock()
		return nil
	}
	updates := x.updates
	x.updates = map[string]bool{}
	x.mu.Unlock()
	o := x.h.Options()
	u, _, _ := o.Get("registry.updates", registry.At{})
	hold, _, _ := o.Get("registry.trusted_hold", registry.At{})
	r := &catalog.RegistryResolver{Client: c, GitHub: x.github(), Updates: u.(string), Hold: hold.(time.Duration),
		Platform: runtime.GOOS + "/" + runtime.GOARCH, Now: x.h.UI().Now}
	for id := range updates {
		r.Update(id)
	}
	return r
}

type loadedPlugin struct {
	dir   string
	stop  registry.Disposable
	waits bool // lazy, and not used yet: stop drops its placeholder
}

// pluginFile is a settings file's [plugins] with its manager.
type pluginFile struct {
	entries []catalog.Entry
	m       *catalog.Manager
	project string
}

func (x *Catalog) Manifest() plugin.Manifest { return plugin.Manifest{ID: "catalog"} }

func (x *Catalog) Deactivate(ctx context.Context) error {
	x.work.Stop()
	return x.Disposer.Deactivate(ctx)
}

func (x *Catalog) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "catalog", Busy: x.work.Busy, Source: "builtin:catalog"}))
	x.h = h
	x.loaded, x.waiting, x.failed = map[string]*loadedPlugin{}, map[string]catalog.Status{}, map[string]string{}
	x.updates = map[string]bool{}
	x.synced = make(chan struct{})
	for _, o := range x.options() {
		x.Add(h.Option(o))
	}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Contribute(uireg.InboxSource{Name: "plugins", Cards: x.cards, Source: "builtin:catalog"}))
	x.pageCommands(h)
	cancel := h.Options().Watch(PluginsOption, x.kick)
	x.Add(registry.DisposeFunc(cancel))
	for _, o := range []string{"registry.sources", "registry.keys"} {
		x.Add(registry.DisposeFunc(h.Options().Watch(o, func() { x.work.Go(x.refresh) })))
	}
	for _, o := range []string{"registry.updates", "registry.trusted_hold"} {
		x.Add(registry.DisposeFunc(h.Options().Watch(o, x.kick)))
	}
	x.work.Loop(func(ctx context.Context) {
		t := kit.NewTicker(kit.PollsOf(x.h), registryEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				x.mu.Lock()
				used := x.client != nil
				x.mu.Unlock()
				if used {
					x.refresh(ctx)
				}
			}
		}
	})
	x.Add(registry.DisposeFunc(func() {
		x.mu.Lock()
		loaded := x.loaded
		x.loaded = map[string]*loadedPlugin{}
		x.mu.Unlock()
		for _, l := range loaded {
			l.stop.Dispose()
		}
		x.stopBundled()
	}))
	x.readBundled()
	listed := func(id string) bool { return x.listed()[id] }
	x.syncBundled(listed)
	x.Add(registry.DisposeFunc(h.Options().Watch(BundledDisabledOption, func() { x.syncBundled(listed) })))
	// A project's own plugins run while the window is on it: they start and stop as it comes and goes.
	x.Add(kit.OnDirChanged(h, "the project's own plugins", func(kit.DirChanged) {
		x.syncBundled(listed)
		x.kick()
	}))
	x.kick()
	return nil
}

// options are [plugins] and the registries'.
func (x *Catalog) options() []registry.OptionSpec {
	return []registry.OptionSpec{
		registry.OptionSpec{Name: PluginsOption, Type: registry.Table, Default: map[string]any{}, Section: "plugins",
			Scopes: registry.ScopeUser | registry.ScopeProject,
			Help:   "The installed plugins, by id: a version range from the registries, or { git = … } or { path = … }; editing it installs and uninstalls"},
		{Name: "registry.sources", Type: registry.List, Default: []string{catalog.DefaultRepo}, Section: "plugins", Scopes: registry.ScopeUser,
			Help: "The plugin registries, as GitHub repos (owner/name), in order: an id in more than one comes from the first"},
		{Name: "registry.keys", Type: registry.Map, Default: map[string]string{}, Section: "plugins", Scopes: registry.ScopeUser,
			KeyLabel: "registry", ValueLabel: "key", Validate: registry.MapOf(func(_, v string) error { _, err := catalog.ParseKey(v); return err }),
			Help: "Each registry's public key, ed25519:<hex>, which its index must be signed with; the default registry's is built in"},
		{Name: "registry.updates", Type: registry.Enum, Values: []string{"auto", "ask", "manual"}, Default: "ask", Section: "plugins",
			Scopes: registry.ScopeUser, Help: "Plugin updates that ask for no more: auto takes them, ask offers them in the inbox, manual on the Plugins page only"},
		{Name: "registry.trusted_hold", Type: registry.Duration, Default: 24 * time.Hour, Section: "plugins", Scopes: registry.ScopeUser,
			Help: "How long a trusted plugin's new tag waits before it's offered"},
		{Name: BundledDisabledOption, Type: registry.List, Default: []string{}, Section: "plugins", Scopes: registry.ScopeUser,
			Help: "The plugins built into Oriel that are turned off, by id"},
	}
}

// commands install, uninstall, consent to, update and sync plugins.
func (x *Catalog) commands() []registry.Command {
	h := x.h
	str := func(a registry.Args, k string) string { v, _ := a[k].(string); return v }
	return []registry.Command{
		{ID: "plugins.sync", Title: "Install and load what [plugins] lists now", Run: func(registry.Ctx, registry.Args) error {
			x.kick()
			return nil
		}},
		{ID: "plugins.install", Title: "Install {id}: one line in [plugins] at {scope} (user by default), a {version} range or a {git} or {path} source",
			Run: func(_ registry.Ctx, a registry.Args) error {
				var v any = cmp.Or(str(a, "version"), "*")
				switch {
				case str(a, "git") != "":
					v = map[string]any{"git": str(a, "git")}
				case str(a, "path") != "":
					v = map[string]any{"path": str(a, "path")}
				}
				return h.Run(context.Background(), "settings.set", registry.Args{"name": PluginsOption, "key": str(a, "id"), "value": v,
					"scope": cmp.Or(str(a, "scope"), "user")})
			}},
		{ID: "plugins.uninstall", Title: "Uninstall {id}: its line leaves [plugins] at {scope} (user by default)",
			Run: func(_ registry.Ctx, a registry.Args) error {
				return h.Run(context.Background(), "settings.reset", registry.Args{"name": PluginsOption, "key": str(a, "id"),
					"scope": cmp.Or(str(a, "scope"), "user")})
			}},
		{ID: "plugins.review", Title: "Open the consent screen for {id}, which waits for it", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.review(str(a, "id"))
		}},
		{ID: "plugins.accept", Title: "Record consent to the plugin waiting in {dir}, and load it", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.accept(str(a, "dir"))
		}},
		{ID: "plugins.update", Title: "Update {id} to the newest version its range allows", Run: func(_ registry.Ctx, a registry.Args) error {
			x.mu.Lock()
			x.updates[str(a, "id")] = true
			x.mu.Unlock()
			x.kick()
			return nil
		}},
		{ID: "plugins.refresh", Title: "Fetch the plugin registries' indexes now", Run: func(registry.Ctx, registry.Args) error {
			x.work.Go(x.refresh)
			return nil
		}},
	}
}

// listed are the ids [plugins] installs, in your settings and the trusted project's.
func (x *Catalog) listed() map[string]bool {
	out := map[string]bool{}
	o := x.h.Options()
	for _, l := range []struct {
		layer registry.Layer
		at    registry.At
	}{{registry.LayerUser, registry.At{}}, {registry.LayerProject, registry.At{Project: kit.WindowDir(x.h)}}} {
		v, _ := o.Lookup(PluginsOption, l.layer, l.at)
		t, _ := v.(map[string]any)
		es, _ := catalog.ParseEntries(t)
		for _, e := range es {
			out[e.ID] = true
		}
	}
	return out
}

// kick syncs off the loop, once at a time: a change while it runs syncs again after.
func (x *Catalog) kick() {
	x.mu.Lock()
	if x.syncing {
		x.again = true
		x.mu.Unlock()
		return
	}
	x.syncing = true
	x.mu.Unlock()
	x.work.Go(func(ctx context.Context) {
		for {
			files := x.files()
			if needsRegistry(files) {
				x.mu.Lock()
				fetched := x.client != nil
				x.mu.Unlock()
				if !fetched {
					x.fetchIndexes(ctx)
					files = x.files()
				}
			}
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
				ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
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
			if !idle && kit.OnLoop(ctx, x.h, func() { x.apply(sts) }) != nil {
				return
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
	})
}

func needsRegistry(fs []pluginFile) bool {
	for _, f := range fs {
		if slices.ContainsFunc(f.entries, func(e catalog.Entry) bool { return e.Registry() }) {
			return true
		}
	}
	return false
}

// files are your settings.toml's [plugins] and the window's project's, which applies only once it's trusted (the
// settings layer has no value until then). An id in both is yours.
func (x *Catalog) files() []pluginFile {
	store := catalog.Store{Dir: filepath.Join(x.DataDir, "plugins")}
	res := x.newResolver()
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
	userFile := filepath.Join(x.ConfigDir, "settings.toml")
	out := []pluginFile{{entries: read(registry.LayerUser, registry.At{}, userFile),
		m: &catalog.Manager{Store: store, Lock: filepath.Join(x.ConfigDir, catalog.LockFile), Resolver: res}}}
	if root := kit.WindowDir(x.h); root != "" {
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
func (x *Catalog) apply(sts []catalog.Status) {
	x.mu.Lock()
	x.statuses = sts
	x.mu.Unlock()
	listed := map[string]bool{}
	waiting := map[string]catalog.Status{}
	revoked := map[string]catalog.Status{}
	offers := map[string]catalog.Status{}
	ask := false
	if v, _, _ := x.h.Options().Get("registry.updates", registry.At{}); v == "ask" {
		ask = true
	}
	for _, st := range sts {
		listed[st.Entry.ID] = true
	}
	x.syncBundled(func(id string) bool { return listed[id] })
	for _, st := range sts {
		id := st.Entry.ID
		switch st.State {
		case catalog.Ready:
			delete(x.failed, id)
			x.h.Dismiss("plugins.failed." + id)
			x.load(st)
			if st.Update != "" && ask {
				offers[id] = st
			}
		case catalog.Revoked:
			x.stop(id)
			revoked[id] = st
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
	x.waiting, x.revoked, x.offers = waiting, revoked, offers
	x.busy = len(sts) > 0 || len(x.loaded) > 0 || len(waiting) > 0
	x.mu.Unlock()
	x.h.Redraw()
}

// load starts st's plugin, in place of another version of it. A lazy one waits behind a placeholder until it's
// first used, unless a version of it was running already.
func (x *Catalog) load(st catalog.Status) {
	id := st.Entry.ID
	lazy := !st.Manifest.Lazy.Empty()
	l := x.loaded[id]
	if l != nil && l.dir == st.Dir && (!l.waits || lazy) {
		return
	}
	running := l != nil && !l.waits
	x.stop(id)
	if lazy && !running {
		x.wait(st)
		return
	}
	x.start(st)
}

func (x *Catalog) start(st catalog.Status) error {
	id := st.Entry.ID
	stop, err := x.loadScript(os.DirFS(st.Dir), kit.ScriptOpts{Dir: st.Dir, Grants: st.Grants(), Data: filepath.Join(x.StateDir, "plugins", id)})
	if err != nil {
		x.h.Notify(core.Notice{ID: "plugins.failed." + id, Severity: core.SevError, Title: "Plugin " + id, Text: err.Error()})
		return err
	}
	x.loaded[id] = &loadedPlugin{dir: st.Dir, stop: stop}
	x.h.Redraw()
	return nil
}

// wait puts up a placeholder for what st's plugin's lazy triggers stand for, which starts it when one is used.
func (x *Catalog) wait(st catalog.Status) {
	id := st.Entry.ID
	p := st.Manifest.Lazy.Placeholder(x.h, id, id, func() error {
		delete(x.loaded, id)
		return x.start(st)
	})
	x.loaded[id] = &loadedPlugin{dir: st.Dir, stop: x.h.Placeholder(p), waits: true}
}

func (x *Catalog) stop(id string) {
	if l := x.loaded[id]; l != nil {
		delete(x.loaded, id)
		l.stop.Dispose()
	}
}

// cards are the plugins waiting for consent.
func (x *Catalog) cards() []uireg.InboxCard {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []uireg.InboxCard
	for _, id := range slices.Sorted(maps.Keys(x.waiting)) {
		st := x.waiting[id]
		detail := "It asks for more than oriel-lock.toml covers, so it isn't loaded until you accept."
		if st.From != nil {
			detail = fmt.Sprintf("v%s asks for more than v%s, which keeps running until you accept.", st.Manifest.Version, st.From.Version)
		}
		sub := "plugins · " + st.Locked.Badge
		if st.Entry.Project != "" {
			sub += " · " + filepath.Base(st.Entry.Project)
		}
		out = append(out, uireg.InboxCard{ID: id, Type: "plugin-consent", Icon: "⚑", Tone: "accent", Interrupt: true,
			Kicker: "PLUGIN · WAITING FOR CONSENT", Title: id, Sub: sub, Detail: detail, Note: st.Warning,
			Actions: []uireg.InboxAction{
				{Label: "Review", Key: "r", Primary: true, Command: "plugins.review", Args: registry.Args{"id": id}},
				{Label: "Uninstall", Key: "u", Command: "plugins.uninstall", Args: registry.Args{"id": id, "scope": scopeOf(st.Entry)}},
			}})
	}
	for _, id := range slices.Sorted(maps.Keys(x.revoked)) {
		st := x.revoked[id]
		out = append(out, uireg.InboxCard{ID: "revoked:" + id, Type: "plugin-revoked", Icon: "✕", Tone: "error", Interrupt: true,
			Kicker: "PLUGIN · REVOKED", Title: id + " " + st.Locked.Version + " is disabled", Sub: "plugins · " + strings.TrimPrefix(st.Locked.Source, "registry:"),
			Detail: st.Err.Error(), Note: "Its registry revoked this version, so it no longer runs.",
			Actions: []uireg.InboxAction{
				{Label: "Update", Key: "u", Primary: true, Command: "plugins.update", Args: registry.Args{"id": id}},
				{Label: "Uninstall", Key: "x", Command: "plugins.uninstall", Args: registry.Args{"id": id, "scope": scopeOf(st.Entry)}},
			}})
	}
	for _, id := range slices.Sorted(maps.Keys(x.offers)) {
		st := x.offers[id]
		out = append(out, uireg.InboxCard{ID: "update:" + id, Type: "plugin-update", Icon: "↑", Tone: "accent",
			Kicker: "PLUGIN · UPDATE", Title: id + " " + st.Update, Sub: "plugins · " + st.Locked.Version + " → " + st.Update,
			Detail:  "A newer version is in the registry. One that asks for more shows the consent screen first.",
			Actions: []uireg.InboxAction{{Label: "Update", Key: "u", Primary: true, Command: "plugins.update", Args: registry.Args{"id": id}}}})
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
func (x *Catalog) review(id string) error {
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
func (x *Catalog) accept(dir string) error {
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

func (x *Catalog) fileOf(e catalog.Entry) pluginFile {
	fs := x.files()
	if e.Project != "" && len(fs) > 1 {
		return fs[1]
	}
	return fs[0]
}

// Statuses are the last sync's, for the Plugins page.
func (x *Catalog) Statuses() []catalog.Status {
	x.mu.Lock()
	defer x.mu.Unlock()
	return slices.Clone(x.statuses)
}

// Sync syncs again; the channel closes once it has, with what it found applied.
func (x *Catalog) Sync() <-chan struct{} {
	x.mu.Lock()
	ch := x.synced
	x.mu.Unlock()
	x.kick()
	return ch
}

// State is what the Plugins page says of plugin id: loaded, waits for first use, disabled…; "" if it lists none.
// Call it on the loop.
func (x *Catalog) State(id string) string {
	for _, r := range x.rows() {
		if r.id == id {
			return r.state
		}
	}
	return ""
}
