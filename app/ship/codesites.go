package ship

import (
	"bufio"
	"cmp"
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// Which code host, and which of its sites, a remote or a pull request's URL is on: a host's public site by its
// hostname; else code.hosts names it, or gh's or glab's config lists it; else the
// URL's shape says so; else a host's probe answers (off the loop, kept in code.hosts); else the user is asked. A
// self-hosted site's API is code.api's for its hostname, else the host's default, and its token is a secret of its own.

// Options naming self-hosted sites: hostname → code host's name, and hostname → API URL.
const (
	CodeHostsOption = "code.hosts"
	CodeAPIOption   = "code.api"
)

// codeSites decides where remotes and URLs are hosted. Its methods don't block but probe, and are safe off the loop.
type codeSites struct {
	h      plugin.Host
	config string // the user's config directory, where gh and glab keep theirs

	mu      sync.Mutex
	probed  map[string]bool             // hostnames probed, whatever they answered
	allowed map[string]bool             // the sites their plugins were let reach, by key
	ran     map[string]string           // each site secret's command's answer, "" for none
	served  map[string]string           // the source each site secret was last answered from: "secret", "env:X", "command"
	refused map[string]map[string]bool  // by site secret: the sources whose token the host refused
	env     func(string) (string, bool) // os.LookupEnv
	run     func(context.Context, []string) (string, error)
}

func newCodeSites(h plugin.Host) *codeSites {
	dir, _ := os.UserConfigDir()
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		dir = x
	}
	return &codeSites{h: h, config: dir, probed: map[string]bool{}, allowed: map[string]bool{}, ran: map[string]string{}, served: map[string]string{},
		refused: map[string]map[string]bool{}, env: os.LookupEnv, run: runToken}
}

func (c *codeSites) options() []registry.OptionSpec {
	return []registry.OptionSpec{
		{Name: CodeHostsOption, Section: "git", Type: registry.Map, Default: map[string]string{}, Scopes: registry.ScopeUser | registry.ScopeProject,
			KeyLabel: "hostname", ValueLabel: "code host",
			Help: "Which code host a self-hosted server is (github, gitlab, azure…), by hostname. Oriel fills it in as it finds out or asks."},
		{Name: "code.trusted_users", Section: "git", Type: registry.List, Default: []string{}, Scopes: registry.ScopeUser,
			Help: "Logins on a code host other than GitHub whose review comments the agent may read, beside the repo's own members."},
		{Name: CodeAPIOption, Section: "git", Type: registry.Map, Default: map[string]string{}, Scopes: registry.ScopeUser,
			KeyLabel: "hostname", ValueLabel: "API URL",
			Help: "A self-hosted server's API URL, by hostname, where it isn't the code host's usual one."},
	}
}

func (c *codeSites) optionMap(name string) map[string]string {
	v, _, _ := c.h.Options().Get(name, registry.At{})
	m, _ := v.(map[string]string)
	return m
}

// Named is the code host the user, gh or glab names hostname as.
func (c *codeSites) Named(hostname string) (string, bool) {
	if n, ok := c.optionMap(CodeHostsOption)[hostname]; ok {
		return n, true
	}
	for name, hosts := range map[string][]string{"github": cliHosts(filepath.Join(c.config, "gh", "hosts.yml"), ""),
		"gitlab": cliHosts(filepath.Join(c.config, "glab-cli", "config.yml"), "hosts")} {
		for _, h := range hosts {
			if strings.EqualFold(h, hostname) {
				return name, true
			}
		}
	}
	return "", false
}

// cliHosts are the hostnames a gh or glab config lists: its top-level keys, or the keys a level under section.
func cliHosts(path, section string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	in, child := section == "", -1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		key, isKey := strings.CutSuffix(strings.TrimSpace(line), ":")
		if !isKey || strings.HasPrefix(key, "#") {
			continue
		}
		key = strings.Trim(key, `"'`)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case section == "":
			if indent == 0 && strings.Contains(key, ".") {
				out = append(out, key)
			}
		case indent == 0:
			in, child = key == section, -1
		case in:
			if child < 0 {
				child = indent
			}
			if indent == child && strings.Contains(key, ".") {
				out = append(out, key)
			}
		}
	}
	return out
}

// at is the host and site at hostname, for a remote or URL whole (for the hosts' Shape).
func (c *codeSites) at(hostname, whole string) (registry.HostSite, bool) {
	hosts := registry.CodeHosts(c.h)
	for _, h := range hosts {
		if s, ok := h.PublicSite(hostname); ok {
			return registry.HostSite{CodeHost: h, Site: s}, true
		}
	}
	if name, ok := c.Named(hostname); ok {
		if h, ok := registry.HostByName(c.h, name); ok {
			return c.site(h, hostname), true
		}
		return registry.HostSite{}, false
	}
	for _, h := range hosts {
		if h.Shape != nil && h.Shape.MatchString(whole) {
			return c.site(h, hostname), true
		}
	}
	return registry.HostSite{}, false
}

// site is h's self-hosted site at hostname, its plugin let reach it.
func (c *codeSites) site(h registry.CodeHost, hostname string) registry.HostSite {
	hs := registry.HostSite{CodeHost: h, Site: h.Setup.For(hostname, c.optionMap(CodeAPIOption)[hostname])}
	c.mu.Lock()
	allow := h.Allow != nil && !c.allowed[hs.Key()]
	c.allowed[hs.Key()] = true
	c.mu.Unlock()
	if allow {
		h.Allow(hostname, hs.Site.Secret)
	}
	return hs
}

// Remote is the host, site and repo of a git remote.
func (c *codeSites) Remote(remote string) (registry.HostSite, string, bool) {
	hostname, path, ok := registry.ParseRemote(remote)
	if !ok {
		return registry.HostSite{}, "", false
	}
	hs, ok := c.at(hostname, remote)
	if !ok {
		return hs, "", false
	}
	repo, ok := hs.RepoAt(path)
	return hs, repo, ok
}

// URL is the host and site of a pull request's URL.
func (c *codeSites) URL(raw string) (registry.HostSite, bool) {
	hostname, path, ok := registry.ParseRemote(raw)
	if !ok {
		return registry.HostSite{}, false
	}
	hs, ok := c.at(hostname, raw)
	return hs, ok && hs.TakesPath(path)
}

// Probe asks each host's probe whether hostname is one of its servers, once a hostname; a host that answers is kept
// in code.hosts.
func (c *codeSites) Probe(ctx context.Context, hostname string) (registry.HostSite, bool) {
	c.mu.Lock()
	done := c.probed[hostname]
	c.probed[hostname] = true
	c.mu.Unlock()
	if done {
		return registry.HostSite{}, false
	}
	for _, h := range registry.CodeHosts(c.h) {
		if h.Setup.Probe == nil {
			continue
		}
		if h.Allow != nil {
			h.Allow(hostname, "")
		}
		var yes bool
		if registry.Call(func() (err error) { yes, err = h.Setup.Probe(ctx, hostname); return err }) == nil && yes {
			c.Remember(hostname, h.Name, "")
			return c.site(h, hostname), true
		}
	}
	return registry.HostSite{}, false
}

// Remember keeps hostname as host name's, with its API URL when given.
func (c *codeSites) Remember(hostname, name, api string) error {
	set := func(option, v string) error {
		m := maps.Clone(c.optionMap(option))
		if m == nil {
			m = map[string]string{}
		}
		if v == "" {
			delete(m, hostname)
		} else {
			m[hostname] = v
		}
		return c.h.Options().Set(option, registry.LayerUser, registry.At{}, m)
	}
	if err := set(CodeHostsOption, name); err != nil {
		return err
	}
	if h, ok := registry.HostByName(c.h, name); ok && api == h.Setup.For(hostname, "").API {
		api = ""
	}
	return set(CodeAPIOption, api)
}

// sites are host h's public sites and those set up for it.
func (c *codeSites) sites(h registry.CodeHost) []registry.HostSite {
	var out []registry.HostSite
	for _, s := range h.Sites {
		out = append(out, registry.HostSite{CodeHost: h, Site: s})
	}
	for hostname, name := range c.optionMap(CodeHostsOption) {
		if name == h.Name {
			out = append(out, c.site(h, hostname))
		}
	}
	return out
}

// ReviewSources are every host's sites, for a host that asks for reviews.
func (c *codeSites) ReviewSources() []registry.HostSite {
	var out []registry.HostSite
	for _, h := range registry.CodeHosts(c.h) {
		if h.Requested != nil {
			out = append(out, c.sites(h)...)
		}
	}
	return out
}

// Keyed says hs has a token the host hasn't refused, or can do without one.
func (c *codeSites) Keyed(hs registry.HostSite) bool {
	if hs.Site.Secret == "" {
		return true
	}
	if _, ok := c.h.Secrets().Secret(hs.Site.Secret); ok {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return hs.Tokens.Optional && len(c.refused[hs.Site.Secret]) == 0
}

// siteOf is the site whose secret is name, of any host.
func (c *codeSites) siteOf(name string) (registry.HostSite, bool) {
	for _, h := range registry.CodeHosts(c.h) {
		for _, hs := range c.sites(h) {
			if hs.Site.Secret == name {
				return hs, true
			}
		}
	}
	return registry.HostSite{}, false
}

// Token is a site secret's value: the one set, then the host's environment variables, then its command's answer
// (once warm has run it), each skipped once the host refused it.
// Token is the secret name: a site's token from wherever it's found, else what set has (kit.SiteTokens).
func (c *codeSites) Token(name string, set plugin.Secrets) (string, bool) {
	hs, ok := c.siteOf(name)
	if !ok {
		return set.Secret(name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	bad := c.refused[name]
	if v, ok := set.Secret(name); ok && !bad["secret"] {
		c.served[name] = "secret"
		return v, true
	}
	_, public := hs.PublicSite(hs.Site.Host)
	for _, e := range hs.Tokens.EnvFor(public) {
		if v, ok := c.env(e); ok && strings.TrimSpace(v) != "" && !bad["env:"+e] {
			c.served[name] = "env:" + e
			return strings.TrimSpace(v), true
		}
	}
	if v := c.ran[name]; v != "" && !bad["command"] {
		c.served[name] = "command"
		return v, true
	}
	return "", false
}

// Warm runs a site's token command, off the loop, if nothing else has a token and it hasn't run.
func (c *codeSites) Warm(ctx context.Context, hs registry.HostSite) {
	name := hs.Site.Secret
	if name == "" || len(hs.Tokens.Command) == 0 {
		return
	}
	c.mu.Lock()
	_, done := c.ran[name]
	c.mu.Unlock()
	if done {
		return
	}
	if _, ok := c.h.Secrets().Secret(name); ok {
		return
	}
	argv := make([]string, len(hs.Tokens.Command))
	for i, a := range hs.Tokens.Command {
		argv[i] = strings.ReplaceAll(a, "{host}", hs.Site.Host)
	}
	v, _ := c.run(ctx, argv)
	c.mu.Lock()
	c.ran[name] = strings.TrimSpace(v)
	c.mu.Unlock()
}

// Refuse marks the token hs last sent as refused, when err is the host saying so (a 401), and says whether it was.
func (c *codeSites) Refuse(hs registry.HostSite, err error) bool {
	if err == nil || hs.Site.Secret == "" || !strings.Contains(err.Error(), "401") {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	name := hs.Site.Secret
	if c.refused[name] == nil {
		c.refused[name] = map[string]bool{}
	}
	c.refused[name][cmp.Or(c.served[name], "none")] = true
	return true
}

// Forget clears what the host refused of a site's tokens, as a new one is set.
// Forget forgets that the secret name was refused (kit.SiteTokens).
func (c *codeSites) Forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.refused, name)
}

// runToken runs a CLI that prints a token, for at most ten seconds.
func runToken(ctx context.Context, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := exec.LookPath(argv[0]); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	return string(out), err
}

// CodeSites owns the shell's codeSites and its options.
type CodeSites struct {
	kit.Disposer
	h     plugin.Host
	sites *codeSites
	ask   *siteAsk
}

func (x *CodeSites) Manifest() plugin.Manifest { return plugin.Manifest{ID: "code-sites"} }

func (x *CodeSites) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.sites = newCodeSites(h)
	for _, o := range x.sites.options() {
		x.Add(h.Option(o))
	}
	x.Add(h.Contribute(registry.Service{Name: registry.CodeSitesService, Impl: x.sites, Source: "builtin:code-sites"}))
	x.Add(h.Contribute(registry.Service{Name: kit.SiteTokensService, Impl: x.sites, Source: "builtin:code-sites"}))
	x.activateSetup(h)
	return nil
}

// TrustedUser says code.trusted_users names login.
func (c *codeSites) TrustedUser(login string) bool {
	v, _, _ := c.h.Options().Get("code.trusted_users", registry.At{})
	l, _ := v.([]string)
	return slices.ContainsFunc(l, func(u string) bool { return strings.EqualFold(strings.TrimPrefix(u, "@"), login) })
}
