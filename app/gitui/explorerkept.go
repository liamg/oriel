package gitui

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/liamg/oriel/gitx"
)

// explorerKept is what the Explorer keeps for each session (Host.Keep), so it comes back as it was left.
type explorerKept struct {
	Scope  string `json:"scope,omitempty"`  // one of explorerScopes; all by default
	Layout string `json:"layout,omitempty"` // Changed's: one of changedLayouts; tree by default
}

const explorerKeptKey = "explorer"

var changedLayouts = []string{"tree", "flat", "by review"}

func (e *Explorer) scope() gitx.Scope {
	if i := slices.Index(explorerScopes, e.state().Scope); i >= 0 {
		return gitx.Scope(i)
	}
	return gitx.All
}

// state is what the Explorer keeps for the session in front.
func (e *Explorer) state() explorerKept {
	id := e.session()
	k, ok := e.kept[id]
	if !ok {
		if b, found := e.h.Kept(id, explorerKeptKey); found {
			if err := json.Unmarshal(b, &k); err != nil {
				e.h.Log().Warn("the explorer's kept state", "session", id, "err", err)
			}
		}
		e.kept[id] = k
	}
	return k
}

// keep changes what the Explorer keeps for the session in front.
func (e *Explorer) keep(change func(*explorerKept)) {
	id, k := e.session(), e.state()
	change(&k)
	e.kept[id] = k
	if err := e.h.Keep(id, explorerKeptKey, k); err != nil {
		e.h.Log().Warn("keeping the explorer's state", "session", id, "err", err)
	}
}

// layout is how Changed lists its files: tree, flat or by review.
func (e *Explorer) layout() string { return cmp.Or(e.state().Layout, changedLayouts[0]) }
