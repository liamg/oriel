package rail

import (
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry/uireg"
)

// RailEntryActive says e's panel is showing for project, as its rail entry is lit.
func RailEntryActive(h plugin.Host, e uireg.RailEntry, project string) bool {
	return active(h, e, project)
}
