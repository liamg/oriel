package registry

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
)

// KindSpec is a kind of session as data (core.KindSpec).
type KindSpec = core.KindSpec

// Capability is something a kind of session can do (core.Capability).
type Capability = core.Capability

// SessionKind is a kind of session. Features ask it what a session can do and how it reads,
// rather than naming kinds: Can, Label, Chips, and the contributions scoped to kinds (Command.When, and uireg's
// SlotView and RailEntry.Kinds).
type SessionKind interface {
	Spec() KindSpec
	// Label is what a session of the kind is called where its kind shows: "plan → work" once a plan's work started.
	Label(se core.SessionState) string
	Can(c Capability) bool
	// Prepare fills in a spec before the session starts: a review finds where its PR comes from and names it. It runs
	// off the app's loop.
	Prepare(ctx Ctx, spec *core.SessionSpec) error
	// Gates are the ship gates its sessions get, by name; nil is every one.
	Gates() []string
	// Chips are what the session's kind says of it, beside its agent's: a review's PR.
	Chips(se core.SessionState) []Chip
}

// Chip is a short label on a session's card: "#4410", and the URL it stands for.
type Chip struct {
	Text string
	Help string
	Link string
}

// Kind is a SessionKind from its spec, with what it does beyond that as functions; nil ones do nothing.
type Kind struct {
	KindSpec
	GateNames   []string
	LabelFunc   func(se core.SessionState) string
	PrepareFunc func(ctx Ctx, spec *core.SessionSpec) error
	ChipsFunc   func(se core.SessionState) []Chip
}

func (k Kind) Spec() KindSpec  { return k.KindSpec }
func (k Kind) Gates() []string { return k.GateNames }

func (k Kind) Label(se core.SessionState) string {
	if k.LabelFunc == nil {
		return string(k.KindSpec.Kind)
	}
	return k.LabelFunc(se)
}

func (k Kind) Prepare(ctx Ctx, spec *core.SessionSpec) error {
	if k.PrepareFunc == nil {
		return nil
	}
	return k.PrepareFunc(ctx, spec)
}

func (k Kind) Chips(se core.SessionState) []Chip {
	if k.ChipsFunc == nil {
		return nil
	}
	return k.ChipsFunc(se)
}

// KindEntry is a session kind as a contribution: registering one with the same kind shadows it until disposed.
type KindEntry struct {
	SessionKind
	Source string
}

func (KindEntry) ContributionKind() string   { return "session.kind" }
func (e KindEntry) ContributionName() string { return string(e.Spec().Kind) }

// Kinds are the session kinds, by Order then kind.
func Kinds(r Contributions) []SessionKind {
	var out []SessionKind
	for _, c := range r.Contributions("session.kind") {
		out = append(out, c.(KindEntry).SessionKind)
	}
	slices.SortStableFunc(out, func(a, b SessionKind) int {
		return cmp.Or(cmp.Compare(a.Spec().Order, b.Spec().Order), strings.Compare(string(a.Spec().Kind), string(b.Spec().Kind)))
	})
	return out
}

// KindOf is the kind registered as k.
func KindOf(r Contributions, k core.Kind) (SessionKind, bool) {
	for _, c := range r.Contributions("session.kind") {
		if e := c.(KindEntry); e.Spec().Kind == k {
			return e.SessionKind, true
		}
	}
	return nil, false
}

// Can says whether sessions of kind k can do c; an unknown kind can't.
func Can(r Contributions, k core.Kind, c Capability) bool {
	sk, ok := KindOf(r, k)
	ok2 := false
	if ok {
		Call(func() error { ok2 = sk.Can(c); return nil })
	}
	return ok2
}

// Label is how se's kind reads for it: its kind's Label, else the kind's name.
func Label(r Contributions, se core.SessionState) string {
	out := string(se.Kind)
	if sk, ok := KindOf(r, se.Kind); ok {
		Call(func() error { out = sk.Label(se); return nil })
	}
	return out
}

// CardChips are chips a plugin adds to what's said of every session, after its kind's: the PR a session raised.
type CardChips struct {
	Name   string
	Chips  func(se core.SessionState) []Chip
	Source string
}

func (CardChips) ContributionKind() string   { return "card.chips" }
func (c CardChips) ContributionName() string { return c.Name }

// Chips are se's chips: its kind's, then each CardChips contribution's.
func Chips(r Contributions, se core.SessionState) []Chip {
	var out []Chip
	if sk, ok := KindOf(r, se.Kind); ok {
		Call(func() error { out = sk.Chips(se); return nil })
	}
	for _, c := range r.Contributions("card.chips") {
		cc := c.(CardChips)
		Call(func() error { out = append(out, cc.Chips(se)...); return nil })
	}
	return out
}

// SessionDone says whether a session's work looks finished: Done answers why ("You approved the pull request."), or ""
// for not done or not its to say.
type SessionDone struct {
	Name   string
	Done   func(se core.SessionState) string
	Source string
}

func (SessionDone) ContributionKind() string   { return "session.done" }
func (d SessionDone) ContributionName() string { return d.Name }

// Done is why se looks finished, the first SessionDone contribution's answer that isn't ""; "" for not finished.
func Done(r Contributions, se core.SessionState) string {
	for _, c := range r.Contributions("session.done") {
		d := c.(SessionDone)
		why := ""
		Call(func() error { why = d.Done(se); return nil })
		if why != "" {
			return why
		}
	}
	return ""
}

// SessionUnsaved says what ending a session would lose that only its contributor knows of: Unsaved answers what
// ("Your review isn't sent: 3 comments."), or "" for nothing.
type SessionUnsaved struct {
	Name    string
	Unsaved func(se core.SessionState) string
	Source  string
}

func (SessionUnsaved) ContributionKind() string   { return "session.unsaved" }
func (u SessionUnsaved) ContributionName() string { return u.Name }

// Unsaved is what ending se would lose, each SessionUnsaved contribution's answer that isn't "".
func Unsaved(r Contributions, se core.SessionState) []string {
	var out []string
	for _, c := range r.Contributions("session.unsaved") {
		u := c.(SessionUnsaved)
		what := ""
		Call(func() error { what = u.Unsaved(se); return nil })
		if what != "" {
			out = append(out, what)
		}
	}
	return out
}

// FrontSession is the session in front, if the Sessions service knows one.
func FrontSession(r Contributions) (core.SessionState, bool) {
	s, ok := SessionsOf(r)
	if !ok {
		return core.SessionState{}, false
	}
	var se core.SessionState
	ok = false
	Call(func() error { se, ok = s.Front(); return nil })
	return se, ok
}

// SessionByID is a session by id, if the Sessions service knows it.
func SessionByID(r Contributions, id core.SessionID) (core.SessionState, bool) {
	s, ok := SessionsOf(r)
	if !ok {
		return core.SessionState{}, false
	}
	var se core.SessionState
	ok = false
	Call(func() error { se, ok = s.Get(id); return nil })
	return se, ok
}

// FrontCan says whether the session in front's kind can do c; false with no session in front.
func FrontCan(r Contributions, c Capability) bool {
	se, ok := FrontSession(r)
	return ok && Can(r, se.Kind, c)
}

// ForSession says whether a contribution scoped by kinds and a capability is shown for a session of kind k: k is
// one of kinds (any, with none), and k can do when (anything, with "").
func ForSession(r Contributions, k core.Kind, kinds []core.Kind, when Capability) bool {
	if len(kinds) > 0 && !slices.Contains(kinds, k) {
		return false
	}
	return when == "" || Can(r, k, when)
}
