package kit

import (
	"errors"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/services"
)

// GitHubOf is the window's GitHub, refusing a window without it.
func GitHubOf(r registry.Registry) (services.GitHub, error) {
	if x, ok := registry.ServiceOf[services.GitHub](r, services.GitHubService); ok {
		return x, nil
	}
	return nil, errors.New("GitHub isn't a code host in this window")
}
