package kit

import (
	"context"
	"errors"
	"time"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

var ErrNoSessions = errors.New("there are no sessions in this window")

// The session host's parts' calls, each its own service, or why there are none.
func SessionCalls(r registry.Registry) (host.SessionCalls, error) {
	return hostPart[host.SessionCalls](r, registry.HostSessionsService)
}
func SandboxCalls(r registry.Registry) (host.SandboxCalls, error) {
	return hostPart[host.SandboxCalls](r, registry.HostSandboxesService)
}
func ShipCalls(r registry.Registry) (host.ShipCalls, error) {
	return hostPart[host.ShipCalls](r, registry.HostShipsService)
}
func CodeHostCalls(r registry.Registry) (host.CodeHostCalls, error) {
	return hostPart[host.CodeHostCalls](r, registry.HostCodeHostsService)
}
func RepoCalls(r registry.Registry) (host.RepoCalls, error) {
	return hostPart[host.RepoCalls](r, registry.HostReposService)
}
func JobCalls(r registry.Registry) (host.JobCalls, error) {
	return hostPart[host.JobCalls](r, registry.HostJobsService)
}
func StatsCalls(r registry.Registry) (host.StatsCalls, error) {
	return hostPart[host.StatsCalls](r, registry.HostStatsService)
}

func hostPart[T any](r registry.Registry, name string) (T, error) {
	if w, ok := registry.ServiceOf[T](r, name); ok {
		return w, nil
	}
	var none T
	return none, ErrNoSessions
}

// HasSessions says the window has a session host.
func HasSessions(r registry.Registry) bool {
	_, err := SessionCalls(r)
	return err == nil
}

// SitesOf is the CodeSites service, or one that knows no sites.
func SitesOf(r registry.Registry) registry.CodeSites {
	if s, ok := registry.CodeSitesOf(r); ok {
		return s
	}
	return noSites{}
}

type noSites struct{}

func (noSites) URL(string) (registry.HostSite, bool) { return registry.HostSite{}, false }
func (noSites) Remote(string) (registry.HostSite, string, bool) {
	return registry.HostSite{}, "", false
}
func (noSites) Named(string) (string, bool)             { return "", false }
func (noSites) Remember(string, string, string) error   { return errors.New("no code sites") }
func (noSites) ReviewSources() []registry.HostSite      { return nil }
func (noSites) Keyed(registry.HostSite) bool            { return false }
func (noSites) Warm(context.Context, registry.HostSite) {}
func (noSites) Refuse(registry.HostSite, error) bool    { return false }
func (noSites) TrustedUser(string) bool                 { return false }
func (noSites) Probe(context.Context, string) (registry.HostSite, bool) {
	return registry.HostSite{}, false
}

// SessionFrom is the session a command's {id} names, else the one in front (Sessions.FromArgs).
func SessionFrom(h plugin.Host, a registry.Args) (core.SessionState, error) {
	s, ok := registry.SessionsOf(h)
	if !ok {
		return core.SessionState{}, ErrNoSessions
	}
	return s.FromArgs(a)
}

// SessionArgs is SessionFrom, and the session host's session calls.
func SessionArgs(h plugin.Host, a registry.Args) (core.SessionState, host.SessionCalls, error) {
	se, err := SessionFrom(h, a)
	if err != nil {
		return se, nil, err
	}
	w, err := SessionCalls(h)
	return se, w, err
}

// The app's own services, whose types draw or carry the toolkit's and so aren't the registry's.
const (
	IndicatorsService = "indicators"
	StatsService      = "stats"
	MarkdownService   = "markdown"
)

// Markdown draws markdown, as the markdown plugin renders it.
type Markdown interface {
	// Text draws src wrapped to width, its body in body's style.
	Text(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle)
	// TextIn is Text as o says: its images found from o.Dir, its newlines line breaks with o.Breaks.
	TextIn(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle, o MarkdownOpts)
	// Title is src's first level-1 heading, as plain text.
	Title(src string) (string, bool)
}

// MarkdownTitle is src's first level-1 heading as the window's markdown service reads it.
func MarkdownTitle(r registry.Registry, src string) (string, bool) {
	if md, ok := registry.ServiceOf[Markdown](r, MarkdownService); ok {
		return md.Title(src)
	}
	return "", false
}

// Indicators build indicators to draw inside another panel, as the HUD keeps them.
type Indicators interface {
	// Node is ind's node to draw, or its error in its place; nil once it's been turned off.
	Node(c *ui.Ctx, ind uireg.Indicator) ui.Node
}

func IndicatorsOf(r registry.Registry) (Indicators, bool) {
	return registry.ServiceOf[Indicators](r, IndicatorsService)
}

// Stats are what the sessions cost, as the Stats page last read them.
type Stats interface {
	// Session is a session's stats, once they've been read.
	Session(id core.SessionID) (stats.Detail, bool)
}

func StatsOf(r registry.Registry) (Stats, bool) { return registry.ServiceOf[Stats](r, StatsService) }

// RefetchAfter is how old an answer gets before it's asked for again while it's shown.
const RefetchAfter = time.Minute

// Prompts are the prompts registered with r's window, beside agent/prompts' own: plugins' (prompts.Prepare).
func Prompts(r registry.Registry) []prompts.Def {
	return registry.ContributionsOf[prompts.Def](r)
}

// RenderPrompt is one of agent/prompts as the user has it, its placeholders filled.
func RenderPrompt(configDir, id string, vars map[string]string) string {
	return prompts.Store{Dir: configDir}.Load().Render(id, vars)
}

// SiteTokensService finds a code host's site's token wherever it's kept, for the window's secrets.
const SiteTokensService = "code.tokens"

// SiteTokens are the code sites' tokens, as secrets.
type SiteTokens interface {
	// Token is the secret name: a site's token from wherever it's found, else what set has.
	Token(name string, set plugin.Secrets) (string, bool)
	// Forget forgets what's known of the secret name, which is being set.
	Forget(name string)
}
