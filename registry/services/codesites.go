package services

import (
	"context"

	"github.com/liamg/oriel/registry"
)

// CodeSitesService is the window's CodeSites.
const CodeSitesService = "code.sites"

func CodeSitesOf(r registry.Contributions) (CodeSites, bool) {
	return registry.ServiceOf[CodeSites](r, CodeSitesService)
}

// CodeSites say which code host, and which of its sites, a remote or a pull request's URL is on.
// They don't block but Probe, and are safe off the loop.
type CodeSites interface {
	URL(raw string) (registry.HostSite, bool)                             // a pull request's URL
	Remote(remote string) (registry.HostSite, string, bool)               // a remote's URL, and the repo's name there
	Probe(ctx context.Context, hostname string) (registry.HostSite, bool) // asks each host whether hostname is one of its
	Named(hostname string) (string, bool)                                 // the code host the user, gh or glab names hostname as
	Remember(hostname, name, api string) error                            // keeps which host hostname is, and its API URL
	ReviewSources() []registry.HostSite                                   // the sites review requests are read from
	Sources(can func(registry.CodeHost) bool) []registry.HostSite         // the sites of every host can says yes to
	Keyed(s registry.HostSite) bool                                       // s's token is set
	Warm(ctx context.Context, s registry.HostSite)                        // reads s's token ahead of its first request
	Refuse(s registry.HostSite, err error) bool                           // says s refused its token with err; true if it was refused
	TrustedUser(login string) bool                                        // code.trusted_users has login
}
