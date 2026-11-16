package services

import "github.com/liamg/oriel/registry"

// HelpPagesService is the help view's index of every contributor's help pages (HelpPages).
const HelpPagesService = "help.pages"

// HelpPages are the help pages as the help view has them, from every contributor.
type HelpPages interface {
	// Resolves says a help id ("shell/omnibox", "git/ship#pr") names a page, and its anchor if it has one.
	Resolves(helpID string) bool
}

func HelpPagesOf(r registry.Contributions) (HelpPages, bool) {
	return registry.ServiceOf[HelpPages](r, HelpPagesService)
}
