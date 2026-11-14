package agentui

import (
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/render"
)

// What the package's outside tests read of its plugins.

type (
	SessionLine = sessionLine
	PRLine      = prLine
	AsstFile    = asstFile
	WhereLine   = whereLine
)

func (x *Assistant) SetClock(c clock.Clock)  { x.conv.Clock = c }
func (x *Assistant) Open() bool              { return x.open }
func (x *Assistant) WindowRect() render.Rect { return x.win.Rect }
