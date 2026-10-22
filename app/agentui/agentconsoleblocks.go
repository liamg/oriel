package agentui

import (
	"fmt"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Transcript blocks' kinds.
const (
	blockLead = iota
	blockAsk
	blockItem
	blockSubagents
	blockBackground
	blockFork
)

// consoleBlock is a transcript block: its key, its kind, and its ask's, item's or fork's index.
type consoleBlock struct {
	key      string
	kind, at int
}

// blocksKey is what a transcript's blocks are laid out from: a transcript or asks changed are new slices.
type blocksKey struct {
	items                           *core.Item
	nItems                          int
	asks                            *core.Ask
	nAsks                           int
	starting, subagents, background bool
	forks                           int
}

// consoleBlocks are a transcript's blocks as last laid out.
type consoleBlocks struct {
	made   bool
	key    blocksKey
	blocks []consoleBlock
	keys   []string // the blocks' keys, the lead's left out
}

// of are the blocks of se and of forks side answers: its items, its asks where they arose, its subagents and background
// tasks, and the lead when there's nothing yet or it's starting.
func (l *consoleBlocks) of(se core.SessionState, forks int) []consoleBlock {
	k := blocksKey{nItems: len(se.Transcript), nAsks: len(se.Asks), starting: se.Agent.Starting != nil, subagents: len(se.Subagents) > 0,
		background: len(se.Agent.Background) > 0, forks: forks}
	if k.nItems > 0 {
		k.items = &se.Transcript[0]
	}
	if k.nAsks > 0 {
		k.asks = &se.Asks[0]
	}
	if l.made && l.key == k {
		return l.blocks
	}
	var bs []consoleBlock
	ask := 0
	emitAsks := func(before time.Time) {
		for ; ask < len(se.Asks); ask++ {
			if a := se.Asks[ask]; !before.IsZero() && (a.Created.IsZero() || !a.Created.Before(before)) {
				return
			}
			bs = append(bs, consoleBlock{key: "ask:" + se.Asks[ask].ID, kind: blockAsk, at: ask})
		}
	}
	for i, it := range se.Transcript {
		if it.Agent != "" {
			continue
		}
		emitAsks(it.Time)
		bs = append(bs, consoleBlock{key: "item:" + it.ID, kind: blockItem, at: i})
	}
	if k.subagents {
		bs = append(bs, consoleBlock{key: "subagents", kind: blockSubagents})
	}
	if k.background {
		bs = append(bs, consoleBlock{key: "background", kind: blockBackground})
	}
	emitAsks(time.Time{})
	for i := range forks {
		bs = append(bs, consoleBlock{key: fmt.Sprint("fork:", i), kind: blockFork, at: i})
	}
	keys := make([]string, len(bs))
	for i, b := range bs {
		keys[i] = b.key
	}
	if k.starting || k.nItems == 0 && k.nAsks == 0 {
		bs = append([]consoleBlock{{key: "\x00lead", kind: blockLead}}, bs...)
	}
	*l = consoleBlocks{made: true, key: k, blocks: bs, keys: keys}
	return bs
}

// open is whether a block shows its body: its default, flipped by ⏎ or a click.
func (st *consoleState) open(key string, def bool) bool { return def != st.flip[key] }

func (st *consoleState) toggle(key string) {
	st.flip[key] = !st.flip[key]
	st.sel = key
}

// blockStyle is a transcript block's box: bordered, lit while selected.
func blockStyle(th *theme.Resolved, width float32, selected bool) ui.Style {
	stroke := th.C(tBlockLine)
	if selected {
		stroke = th.C(tBlockSelected)
	}
	return ui.Style{W: ui.Px(width), Fill: th.C(tBlockBg), Stroke: stroke, Border: 1, Radius: 6, Clip: true, Role: ui.RoleListItem, Selected: selected}
}
