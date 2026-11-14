package kit

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// NewestSession is, of the sessions match takes, a live one before an archived one, then the newest.
func NewestSession(h plugin.Host, match func(core.SessionState) bool) (core.Session, bool) {
	var best core.Session
	var bestSt core.SessionState
	for _, se := range h.Sessions() {
		st := se.Snapshot()
		if !match(st) {
			continue
		}
		if best == nil || bestSt.Archived && !st.Archived || bestSt.Archived == st.Archived && !st.Created.Before(bestSt.Created) {
			best, bestSt = se, st
		}
	}
	return best, best != nil
}

// PendingFirst reports a list's first fetch running; one never asked for isn't loading.
func PendingFirst(s widget.LoadState) bool { return s.Phase == widget.Pending && !s.Stale }

// DefaultAgent is the agent and model a session starts on when nobody picks: the last session's (a job's runs on its
// own), while its provider is still there; else the first provider by installed, then logged in, then name.
func DefaultAgent(st core.State, ps []host.ProviderInfo) (provider, model string) {
	var last core.SessionState
	for _, se := range st.Sessions {
		if se.Agent.Provider != "" && host.JobOf(se) == "" && se.Created.After(last.Created) {
			last = se
		}
	}
	if i := slices.IndexFunc(ps, func(p host.ProviderInfo) bool { return p.ID == last.Agent.Provider }); i >= 0 {
		p := ps[i]
		if last.Agent.Model != "" && (len(p.Models) == 0 || slices.ContainsFunc(p.Models, func(m agent.Model) bool { return m.ID == last.Agent.Model })) {
			return p.ID, last.Agent.Model
		}
		return p.ID, DefaultModel(p)
	}
	if len(ps) == 0 {
		return "", ""
	}
	rank := func(p host.ProviderInfo) int {
		switch installed := p.Error == "" && p.Install.Bin != ""; {
		case installed && p.Install.LoggedIn:
			return 0
		case installed:
			return 1
		}
		return 2
	}
	best := slices.MinFunc(ps, func(a, b host.ProviderInfo) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	return best.ID, DefaultModel(best)
}

// DefaultModel is a provider's default model, else its first.
func DefaultModel(p host.ProviderInfo) string {
	for _, m := range p.Models {
		if m.Default {
			return m.ID
		}
	}
	if len(p.Models) > 0 {
		return p.Models[0].ID
	}
	return ""
}

// AssistantAgent is the utility agent's name: who its tools are for.
const AssistantAgent = "assistant"

// Initials are a name's first two words' first letters, upper case.
func Initials(name string) string {
	var out []rune
	for _, w := range strings.Fields(name) {
		out = append(out, []rune(strings.ToUpper(w))[0])
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// TAvatarGitHub is a GitHub user's avatar.
var TAvatarGitHub = theme.Declare("requests.avatar.github", "violet")

// SessionAt is the session and project whose work tree root is, the session in front first.
func SessionAt(st core.State, root string) (core.SessionState, string, bool) {
	var out []core.SessionState
	if se, ok := st.Session(st.Focus); ok {
		out = append(out, se.Snapshot())
	}
	out = append(out, st.Sessions...)
	for _, se := range out {
		for _, p := range se.Projects {
			if p.Dir != "" && filepath.Clean(p.Dir) == filepath.Clean(root) {
				return se, p.Name, true
			}
		}
	}
	return core.SessionState{}, "", false
}

// TPermAllow is the tone of what's allowed, or running as it should.
var TPermAllow = theme.Declare("permissions.allow", "ok")

// TranscriptKey is a session's transcript as it is now, and where its relative paths are: the state replaces a
// transcript when it changes, never changing one in place.
type TranscriptKey struct {
	items *core.Item
	n     int
	dir   string
}

func TranscriptOf(se core.SessionState) TranscriptKey {
	k := TranscriptKey{n: len(se.Transcript)}
	if k.n > 0 {
		k.items = &se.Transcript[0]
	}
	if p, ok := se.Primary(); ok {
		k.dir = p.Dir
	}
	return k
}

// ThreadGroup is where a thread sits in the Requests list: what it waits on.
type ThreadGroup int

const (
	GroupWorking ThreadGroup = iota
	GroupNeedsYou
	GroupOpen
	GroupResolved
)

// GroupOf is the group t sits in.
func GroupOf(t core.Thread) ThreadGroup {
	switch {
	case !t.Open():
		return GroupResolved
	case t.Busy:
		return GroupWorking
	case t.AgentAnswered() && !t.Btw:
		return GroupNeedsYou
	}
	return GroupOpen
}

// SameFile reports whether a path the agent edited (absolute, or relative to the primary project) is path.
func SameFile(se core.SessionState, edited, path string) bool {
	if !filepath.IsAbs(edited) {
		if p, ok := se.Primary(); ok {
			edited = filepath.Join(p.Dir, edited)
		}
	}
	return filepath.Clean(edited) == filepath.Clean(path)
}

// EditedAgo is how long ago an edit was, to the second while it's fresh.
func EditedAgo(now, t time.Time) string {
	if d := now.Sub(t); d < time.Minute {
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	}
	return widget.AgoLabel(now, t) + " ago"
}

// MainInput is what a hook matches a tool's input on: a shell's command, an edit's path, else the JSON.
func MainInput(raw string) string {
	var in map[string]any
	if json.Unmarshal([]byte(raw), &in) != nil {
		return raw
	}
	for _, k := range []string{"command", "file_path", "path", "url"} {
		if s, ok := in[k].(string); ok {
			return s
		}
	}
	return raw
}

// A diagnostic's tones: an error, a warning.
var (
	TDiagError   = theme.Declare("diagnostic.error", "error")
	TDiagWarning = theme.Declare("diagnostic.warning", "warn")
)

// ProviderNameIn is what agent provider p is called (services.ProviderTitle), from the registry c is drawn with.
func ProviderNameIn(c *ui.Ctx, p string) string {
	r, _ := ui.Service[registry.Registry](c)
	return services.ProviderTitle(r, p)
}
