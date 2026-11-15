package sessions

import (
	"github.com/liamg/oriel/registry"
)

// What the package's outside tests read and set of its plugins.

func (x *Launcher) Open() bool               { return x.open }
func (x *Launcher) Prompt() string           { return x.prompt }
func (x *Launcher) Provider() string         { return x.provider }
func (x *Launcher) Sandbox() bool            { return x.sandbox }
func (x *Launcher) OpenWith(a registry.Args) { x.openWith(a) }
