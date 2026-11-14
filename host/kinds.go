package host

import (
	"slices"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
)

// KindSource is where the host finds the kinds of session it starts (hostapi's).
type KindSource = hostapi.KindSource

// Built-in kinds of session, as data (hostapi's). Their plugins register them in the app; the host reads these until
// the app gives it its registry (SetKinds).
var (
	WorkKind    = hostapi.WorkKind
	PlanKind    = hostapi.PlanKind
	ReviewKind  = hostapi.ReviewKind
	JobKind     = hostapi.JobKind
	NewRepoKind = hostapi.NewRepoKind
	DirectKind  = hostapi.DirectKind
)

// BuiltinKinds are the built-in kinds, in the launcher's order.
func BuiltinKinds() []core.KindSpec { return hostapi.BuiltinKinds() }

type builtins struct{}

func (builtins) Kind(k core.Kind) (core.KindSpec, bool) {
	all := BuiltinKinds()
	if i := slices.IndexFunc(all, func(s core.KindSpec) bool { return s.Kind == k }); i >= 0 {
		return all[i], true
	}
	return core.KindSpec{}, false
}

// kinds holds the host's KindSource.
type kinds struct {
	mu  sync.RWMutex
	src KindSource
}

func (k *kinds) Get(kind core.Kind) (core.KindSpec, bool) {
	k.mu.RLock()
	src := k.src
	k.mu.RUnlock()
	if src == nil {
		src = builtins{}
	}
	return src.Kind(kind)
}

// SetKinds sets where the host finds its kinds of session; nil is the built-ins.
func (w *Host) SetKinds(src KindSource) {
	defer w.env.Recover("SetKinds", nil)
	w.kinds.mu.Lock()
	defer w.kinds.mu.Unlock()
	w.kinds.src = src
}

// Spec is se's kind's spec; a kind no longer registered can do everything a session with worktrees can.
func (k *kinds) Spec(se core.SessionState) core.KindSpec {
	if s, ok := k.Get(se.Kind); ok {
		return s
	}
	return core.KindSpec{Kind: se.Kind, Caps: hostapi.WorktreeCaps}
}
