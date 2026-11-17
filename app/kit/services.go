package kit

import (
	"context"
	"errors"
	"time"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/services"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

// Sites is the CodeSites service, or one that knows no sites.
func Sites(r registry.Registry) services.CodeSites {
	if s, ok := services.CodeSitesOf(r); ok {
		return s
	}
	return noSites{}
}

type noSites struct{}

func (noSites) URL(string) (registry.HostSite, bool) { return registry.HostSite{}, false }
func (noSites) Remote(string) (registry.HostSite, string, bool) {
	return registry.HostSite{}, "", false
}
func (noSites) Named(string) (string, bool)                              { return "", false }
func (noSites) Remember(string, string, string) error                    { return errors.New("no code sites") }
func (noSites) ReviewSources() []registry.HostSite                       { return nil }
func (noSites) Sources(func(registry.CodeHost) bool) []registry.HostSite { return nil }
func (noSites) Keyed(registry.HostSite) bool                             { return false }
func (noSites) Warm(context.Context, registry.HostSite)                  {}
func (noSites) Refuse(registry.HostSite, error) bool                     { return false }
func (noSites) TrustedUser(string) bool                                  { return false }
func (noSites) Probe(context.Context, string) (registry.HostSite, bool) {
	return registry.HostSite{}, false
}

// The app's own services, whose types draw or carry the toolkit's and so aren't the registry's.
const (
	IndicatorsService = "indicators"
	MarkdownService   = "markdown"
)

// Markdown draws markdown, as the markdown plugin renders it.
type Markdown interface {
	// TextIn draws src wrapped to width, its body in body's style, as o says: its images found from o.Dir, its
	// newlines line breaks with o.Breaks.
	TextIn(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle, o MarkdownOpts)
	// Title is src's first level-1 heading, as plain text.
	Title(src string) (string, bool)
}

// Indicators build indicators to draw inside another panel, as the HUD keeps them.
type Indicators interface {
	// Node is ind's node to draw, or its error in its place; nil once it's been turned off.
	Node(c *ui.Ctx, ind uireg.Indicator) ui.Node
}

func IndicatorsOf(r registry.Registry) (Indicators, bool) {
	return registry.ServiceOf[Indicators](r, IndicatorsService)
}

// ProviderChoices are what an option naming an agent provider offers: auto, then each provider registered.
func ProviderChoices(r registry.Registry) func() []string {
	return func() []string {
		out := []string{"auto"}
		for _, p := range services.AgentProviders(r) {
			out = append(out, p.Name)
		}
		return out
	}
}

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
