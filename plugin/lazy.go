package plugin

import (
	"slices"
	"strings"

	"github.com/liamg/oriel/registry"
)

// Lazy is what loads a plugin that isn't loaded at start: the first use of one of its commands (globs), events
// ("BufRead *.go"), keys ("<leader>f", "global <D-j>"), filetypes, omnibox prefixes or views (globs over view ids).
type Lazy struct {
	Commands, Events, Keys, Filetypes, Omnibox, Views []string
	Titles                                            map[string]string // what its commands say before it loads, by id
}

// Empty is a plugin loaded at start.
func (l Lazy) Empty() bool {
	return len(l.Commands)+len(l.Events)+len(l.Keys)+len(l.Filetypes)+len(l.Omnibox)+len(l.Views) == 0
}

// ParseLazy reads a lazy table, a manifest's or a [plugins] entry's.
func ParseLazy(t map[string]any) Lazy {
	return Lazy{Commands: Strings(t["commands"]), Events: Strings(t["events"]), Keys: Strings(t["keys"]), Filetypes: Strings(t["filetypes"]),
		Omnibox: Strings(t["omnibox"]), Views: Strings(t["views"])}
}

// Triggers are the triggers' words, as the Plugins page lists them.
func (l Lazy) Triggers() []string {
	return slices.Concat(l.Commands, l.Events, l.Keys, l.Filetypes, l.Omnibox, l.Views)
}

// Placeholder is the registry placeholder for l's triggers, which calls provide on the first use of one. Filetypes
// are a buffer of the language of that name opening, as r knows the languages now.
func (l Lazy) Placeholder(r registry.Registry, name, source string, provide func() error) registry.Placeholder {
	p := registry.Placeholder{Name: name, Commands: l.Commands, Omnibox: l.Omnibox, Titles: l.Titles, Source: source, Provide: provide}
	if len(l.Views) > 0 {
		p.Contributions = map[string][]string{registry.KindView: l.Views}
	}
	for _, k := range l.Keys {
		p.Keys = append(p.Keys, registry.KeyTrigger(k))
	}
	for _, e := range l.Events {
		p.Events = append(p.Events, registry.EventTriggerOf(e))
	}
	p.Events = append(p.Events, filetypeTriggers(r, l.Filetypes)...)
	return p
}

// filetypeTriggers are a buffer of each filetype opening: BufRead of a file the language of that name is for, or
// of *.<filetype> when no language has the name.
func filetypeTriggers(r registry.Registry, fts []string) []registry.EventTrigger {
	if len(fts) == 0 {
		return nil
	}
	langs, _ := r.Languages()
	var out []registry.EventTrigger
	for _, ft := range fts {
		i := slices.IndexFunc(langs, func(l registry.Language) bool { return strings.EqualFold(l.Name, ft) })
		if i < 0 {
			out = append(out, registry.EventTrigger{Event: "BufRead", Pattern: "*." + ft})
			continue
		}
		for _, e := range langs[i].Extensions {
			out = append(out, registry.EventTrigger{Event: "BufRead", Pattern: "*" + e})
		}
		for _, n := range langs[i].Filenames {
			out = append(out, registry.EventTrigger{Event: "BufRead", Pattern: n})
		}
	}
	return out
}
