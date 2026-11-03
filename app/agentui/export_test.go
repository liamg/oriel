package agentui

import (
	"time"

	"github.com/liamg/oriel/render"
)

// What the package's outside tests read of its plugins.

type (
	SessionLine = sessionLine
	PRLine      = prLine
	AsstFile    = asstFile
	WhereLine   = whereLine
)

func (x *Assistant) SetNow(now func() time.Time) { x.conv.Now = now }
func (x *Assistant) Open() bool                  { return x.open }
func (x *Assistant) WindowRect() render.Rect     { return x.win.Rect }
