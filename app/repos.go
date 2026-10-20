package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// Repos (SE-6–8, mockup 45): the registered checkouts. The repos.* commands add, clone, remove and maintain them on
// the session host; the Repos page and the Add repo dialog are built on those commands.
const (
	ReposKeyMode   = "repos"
	RepoNewKeyMode = "repos.new"
)

type reposPlugin struct {
	disposer
	sh *shell
	h  plugin.Host

	open    bool
	sel     core.RepoID
	filter  string
	confirm string // what's asked "Sure?" about: "remove", "clean"
	err     string
	details map[core.RepoID]*repoDetail

	dialog   *addRepo // the Add repo dialog, while it's open
	scanning bool     // Scan for repos is running
}

func (x *reposPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "repos"} }

func repoOptions() []registry.OptionSpec {
	user := registry.ScopeUser
	return []registry.OptionSpec{
		{Name: "repos.dir", Type: registry.String, Default: "~/src", Scopes: user,
			Help: "Where clones and new repos go when nothing else says: the Add repo dialog starts from the folder most of your repos share, else this."},
		{Name: "repos.discover", Type: registry.Bool, Default: true, Scopes: user,
			Help: "On start, and when a repo is added, register the other git repos in the folders your repos are in (one level, never your home folder)."},
		{Name: "repos.tag.pull", Type: registry.Bool, Default: true, Scopes: user,
			Help: "New tag pulls the branch first (fast-forward only)."},
		{Name: "repos.tag.sign", Type: registry.Enum, Values: []string{"auto", "always", "never"}, Default: "auto", Scopes: user,
			Help: "Sign new tags: auto when a signing key is set up, always, or never."},
		{Name: "repos.tag.message", Type: registry.String, Default: "{tag}", Scopes: user,
			Help: "A new tag's message; {tag}, {branch} and {repo} expand."},
		{Name: "repos.tag.push", Type: registry.Bool, Default: true, Scopes: user,
			Help: "New tag pushes the tag, and only it, to origin."},
	}
}

func (x *reposPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.details = h, map[core.RepoID]*repoDetail{}
	for _, sp := range repoOptions() {
		x.add(h.Option(sp))
	}
	x.add(onDispose(h.Options().Watch("repos.dir", x.sendDir)))
	x.sendDir()
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if ev, ok := ev.(core.ReposChanged); ok && x.byHand(ev) {
			h.Do(func() { x.discover(false) })
		}
	})))
	x.discover(false)
	has := func(registry.Args) bool { return x.sh.cfg.Host != nil }
	dialogOpen := func(registry.Args) bool { return x.dialog != nil }
	for _, c := range []registry.Command{
		{ID: "repos.open", Title: "The Repos page, at a {repo}", Available: has,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.openPage(core.RepoID(a.String("repo"))) }},
		{ID: "Repos", Title: "The Repos page; :Repos {repo}", Available: has,
			Run: func(_ registry.Ctx, a registry.Args) error {
				return x.openPage(core.RepoID(strings.TrimSpace(a.String("args"))))
			}},
		{ID: "repos.new", Title: "Add a repo: the dialog, with {mode} (directory or clone), {path}, {url}, {parent} or {name} filled in; {then} is a list of {command, args} run with {repo} once it's added",
			Available: has, Run: func(_ registry.Ctx, a registry.Args) error { return x.newDialog(a) }},
		{ID: "repos.add", Title: "Register the checkout at {path}; {then} runs with {repo} once it's added", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				if a.String("path") == "" {
					return errors.New("add which checkout? Give its {path}")
				}
				r, err := x.sh.cfg.Host.AddRepoPath(ctx, expandTilde(a.String("path")))
				if err != nil {
					return err
				}
				return x.then(ctx, a["then"], r.ID)
			}},
		{ID: "repos.clone", Title: "Clone {url} into {parent} as {name}, and register it; {then} runs with {repo} once it's added", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				r, err := x.sh.cfg.Host.CloneRepo(ctx, host.CloneReq{URL: a.String("url"), Parent: a.String("parent"), Name: a.String("name")})
				if err != nil {
					return err
				}
				return x.then(ctx, a["then"], r.ID)
			}},
		{ID: "repos.remove", Title: "Unregister {repo} and stop discovery adding it back; its files stay", Available: has,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.sh.cfg.Host.RemoveRepo(x.repoArg(a)) }},
		{ID: "repos.scan", Title: "Register the git repos in the folders your repos are in", Available: has,
			Run: func(registry.Ctx, registry.Args) error { x.discover(true); return nil }},
		{ID: "repos.fetch", Title: "Fetch {repo}'s remote, pruning branches it deleted", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.sh.cfg.Host.FetchRepo(ctx, x.repoArg(a)) }},
		{ID: "repos.housekeep", Title: "Clean up {repo}: delete the branches in {apply} (merged, or upstream gone) with their worktrees; {force} lists dirty worktrees that go anyway; {prune} prunes stale worktrees. With no {apply}, the Repos page at its housekeeping",
			Available: has, Run: x.housekeepCommand},
		{ID: "repos.tag", Title: "Tag {repo}: an annotated tag {name} on {branch} (the default branch), {message}; {pull}, {sign} and {push} default to the repos.tag options",
			Available: has, Run: x.tagCommand},
		{ID: "repos.new.submit", Title: "Add the repo the Add repo dialog holds", Available: dialogOpen,
			Run: func(registry.Ctx, registry.Args) error { x.submitDialog(); return nil }},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: RepoNewKeyMode, Keys: "<D-CR>", Command: "repos.new.submit", Desc: "Add the repo"}))
	for _, k := range []registry.Keymap{
		{Keys: "a", Command: "repos.new", Desc: "Add a repo"},
		{Keys: "f", Command: "repos.fetch", Desc: "Fetch the repo"},
	} {
		k.Mode = ReposKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.Contribute(pagePlace("repos", "Repos", "repos.open", &x.open)))
	x.add(h.Contribute(registry.RailEntry{Name: "repos", Icon: "repos", Title: "Repos", Command: "repos.open", Scope: registry.ScopeGlobalEntry, Order: 5,
		Active: func(string) bool { return x.open }}))
	x.add(h.Contribute(registry.StartEntry{Name: "repos", Icon: "repos", Title: "Repos", Desc: "Your checkouts: add, clean up, tag", Command: "repos.open",
		Order: 75, Link: true, Note: x.count}))
	x.add(h.View(OverlayPrefix+"repos", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "repos", Fn: x.page} })))
	x.add(h.View(OverlayPrefix+"repos.new", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "reposnew", Fn: x.dialogView} })))
	return nil
}

// repos are the registered repos, as the store has them.
func (x *reposPlugin) repos() []core.Repo { return x.h.State().Repos }

// count is the start page's figure: how many repos, and how many found.
func (x *reposPlugin) count() string {
	rs := x.repos()
	if len(rs) == 0 {
		return ""
	}
	found := 0
	for _, r := range rs {
		if isFound(r) {
			found++
		}
	}
	if found == 0 {
		return counted(len(rs), "repo")
	}
	return fmt.Sprintf("%s · %d found", counted(len(rs), "repo"), found)
}

// isFound says whether discovery added r and no session has used it yet.
func isFound(r core.Repo) bool { return r.Source == core.RepoDiscovered && r.Used.IsZero() }

func (x *reposPlugin) optBool(name string) bool {
	v, _, _ := x.h.Options().Get(name, registry.At{})
	b, _ := v.(bool)
	return b
}

func (x *reposPlugin) optString(name string) string {
	v, _, _ := x.h.Options().Get(name, registry.At{})
	s, _ := v.(string)
	return s
}

func (x *reposPlugin) sendDir() {
	if w := x.sh.cfg.Host; w != nil {
		w.SetReposDir(x.optString("repos.dir"))
	}
}

// byHand says whether a change added a repo the user chose: one whose siblings discovery may not have read.
func (x *reposPlugin) byHand(ev core.ReposChanged) bool {
	for _, id := range ev.Added {
		if i := slices.IndexFunc(ev.Repos, func(r core.Repo) bool { return r.ID == id }); i >= 0 && ev.Repos[i].Source != core.RepoDiscovered {
			return true
		}
	}
	return false
}

// discover registers the siblings of the known repos off the loop, when repos.discover is on or all is asked for.
func (x *reposPlugin) discover(all bool) {
	w := x.sh.cfg.Host
	if w == nil || !all && !x.optBool("repos.discover") {
		return
	}
	if all {
		if x.scanning {
			return
		}
		x.scanning = true
	}
	go func() {
		if _, err := w.ScanRepos(context.Background(), all); err != nil {
			x.h.Log().Warn("repo discovery", "err", err)
		}
		if all {
			x.h.Do(func() { x.scanning = false })
			x.h.Redraw()
		}
	}()
}

// repoArg is {repo}, or the one the Repos page has selected.
func (x *reposPlugin) repoArg(a registry.Args) core.RepoID {
	if r := a.String("repo"); r != "" {
		if rr, ok := x.find(r); ok {
			return rr.ID
		}
		return core.RepoID(r)
	}
	return x.sel
}

// find is a registered repo by ID or path.
func (x *reposPlugin) find(ref string) (core.Repo, bool) {
	for _, r := range x.repos() {
		if string(r.ID) == ref || r.Path == expandTilde(ref) {
			return r, true
		}
	}
	return core.Repo{}, false
}

// then runs each of then's calls with {repo} merged into its args.
func (x *reposPlugin) then(ctx context.Context, then any, id core.RepoID) error {
	calls, err := callsOf(then)
	if err != nil {
		return err
	}
	for _, c := range calls {
		a := registry.Args{}
		for k, v := range c.args {
			a[k] = v
		}
		a["repo"] = string(id)
		if err := x.h.Run(ctx, c.command, a); err != nil {
			return err
		}
	}
	return nil
}

func (x *reposPlugin) openPage(ref core.RepoID) error {
	x.open, x.err, x.confirm = true, "", ""
	if ref == "" {
		return nil
	}
	r, ok := x.find(string(ref))
	if !ok {
		return fmt.Errorf("no repo %q", ref)
	}
	x.sel = r.ID
	return nil
}

func (x *reposPlugin) housekeepCommand(ctx registry.Ctx, a registry.Args) error {
	id := x.repoArg(a)
	if id == "" {
		return errors.New("clean up which repo? Give its {repo}")
	}
	apply := a.Strings("apply")
	if len(apply) == 0 && !a.Bool("prune") {
		if err := x.openPage(id); err != nil {
			return err
		}
		x.readPlan(id, true)
		return nil
	}
	res, err := x.sh.cfg.Host.Housekeep(ctx, host.HousekeepReq{Repo: id, Apply: apply, Force: a.Strings("force"), Prune: a.Bool("prune")})
	if err != nil {
		return err
	}
	var failed []string
	for _, r := range res {
		if r.Error != "" {
			failed = append(failed, r.Branch+": "+r.Error)
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// tagMessage is repos.tag.message with {tag}, {branch} and {repo} expanded.
func tagMessage(tmpl, tag, branch string, repo core.RepoID) string {
	return strings.NewReplacer("{tag}", tag, "{branch}", branch, "{repo}", string(repo)).Replace(tmpl)
}

// signTags says whether a new tag is signed by default: repos.tag.sign, with auto meaning a key is set up.
func (x *reposPlugin) signTags(configured bool) bool {
	switch x.optString("repos.tag.sign") {
	case "always":
		return true
	case "never":
		return false
	}
	return configured
}

func (x *reposPlugin) tagCommand(ctx registry.Ctx, a registry.Args) error {
	id := x.repoArg(a)
	w := x.sh.cfg.Host
	p, err := w.TagPreview(ctx, id, a.String("branch"))
	if err != nil {
		return err
	}
	branch := cmpOr(a.String("branch"), p.Default)
	name := strings.TrimSpace(a.String("name"))
	if name == "" {
		return errors.New("tag what? Give its {name}")
	}
	opt := func(k, option string) bool {
		if _, ok := a[k]; ok {
			return a.Bool(k)
		}
		return x.optBool(option)
	}
	sign := x.signTags(p.Signing)
	if _, ok := a["sign"]; ok {
		sign = a.Bool("sign")
	}
	msg := cmpOr(a.String("message"), tagMessage(x.optString("repos.tag.message"), name, branch, id))
	t, err := w.Tag(ctx, host.TagReq{Repo: id, Branch: branch, Name: name, Message: msg, Pull: opt("pull", "repos.tag.pull"),
		Sign: sign, Push: opt("push", "repos.tag.push") && p.Remote})
	if err != nil {
		return err
	}
	if t.PushErr != "" {
		return fmt.Errorf("tagged %s locally, but the push failed: %s", name, t.PushErr)
	}
	return nil
}
