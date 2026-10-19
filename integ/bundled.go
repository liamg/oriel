package integ

import (
	"github.com/liamg/oriel/integ/linear"
	"github.com/liamg/oriel/plugin"
)

// Bundled are the plugins built into oriel: loaded at start unless disabled, or replaced by an install of the same id.
func Bundled() []plugin.Bundled {
	return []plugin.Bundled{linear.Plugin}
}

// Mains are the bundled plugins' process mains, by the name `oriel plugin-process` runs them by.
var Mains = map[string]plugin.Main{"linear": linear.Serve}
