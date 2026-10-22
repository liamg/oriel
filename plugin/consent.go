package plugin

import (
	"slices"
	"strings"
)

// Consent is what installing or updating a plugin asks: every capability it declares beyond the always-allowed
// ones, accepted together or not at all. An update lists its whole new set, with what's new marked.
type Consent struct {
	Plugin  Manifest
	From    *Manifest // the version installed, for an update; nil for an install
	Source  string    // where it comes from: a registry, a git URL, a directory
	Badge   string    // who looked at its code: reviewed, trusted, unverified
	Warning string    // said before anything else: an unverified source's "Nobody has reviewed this code."
	Facts   []Fact    // what the installer knows of its provenance: commit, checksum, badge, deps
	Rows    []ConsentRow
	Removed []string // capabilities the installed version had and this one drops, as Lock writes them
}

// Fact is a line of provenance, as the install screen lists it.
type Fact struct{ Label, Value string }

// ConsentRow is one declared capability as the install screen shows it.
type ConsentRow struct {
	Grant
	Info    CapInfo
	Group   string   // the heading it's listed under
	Explain string   // Oriel's words for what it lets the plugin do, with its scope; the author's are Why
	Added   []string // for an update, what it asks for that the installed version didn't, as Lock writes them
}

// New is a row an update asks for that the installed version had none of.
func (r ConsentRow) New() bool { return len(r.Added) > 0 && len(r.Added) == len(r.Grant.Lock()) }

// Groups are the install screen's headings, in order.
var Groups = []string{"Network & secrets", "Your code & agents", "Your machine", "Critical"}

func groupOf(c Capability) string {
	switch c {
	case CapNet, CapSecrets:
		return Groups[0]
	case CapInputObserve, CapFSRead, CapFSWrite, CapExec, CapClipboard:
		return Groups[2]
	case CapGate:
		return Groups[3]
	}
	return Groups[1]
}

// Ask is the consent installing m asks for, or, with from, updating to m from it.
func Ask(m Manifest, from *Manifest) Consent {
	c := Consent{Plugin: m, From: from}
	var had []string
	if from != nil {
		had = Locked(from.Permissions)
		c.Removed = Diff(had, Locked(m.Permissions))
	}
	for _, g := range m.Permissions {
		info, ok := g.Cap.Info()
		if !ok || !info.Shown {
			continue
		}
		r := ConsentRow{Grant: g, Info: info, Group: groupOf(g.Cap), Explain: Explain(g)}
		if from != nil {
			r.Added = Diff(g.Lock(), had)
		}
		c.Rows = append(c.Rows, r)
	}
	order := func(c Capability) int {
		return slices.IndexFunc(Capabilities, func(i CapInfo) bool { return i.Cap == c })
	}
	slices.SortStableFunc(c.Rows, func(a, b ConsentRow) int {
		if d := slices.Index(Groups, a.Group) - slices.Index(Groups, b.Group); d != 0 {
			return d
		}
		return order(a.Cap) - order(b.Cap)
	})
	return c
}

// Update says whether this is an update rather than an install.
func (c Consent) Update() bool { return c.From != nil }

// Silent is a consent with nothing to ask: an install that declares only always-allowed capabilities, or an
// update that adds none. Removing capabilities applies silently.
func (c Consent) Silent() bool {
	for _, r := range c.Rows {
		if !c.Update() || len(r.Added) > 0 {
			return false
		}
	}
	return true
}

// Count is how many rows there are at risk r or above.
func (c Consent) Count(r Risk) int {
	n := 0
	for _, row := range c.Rows {
		if row.Info.Risk >= r {
			n++
		}
	}
	return n
}

// Lock is the grant as the lockfile records consent to it: "net:acme.atlassian.net" for each scope
// item, or the capability alone.
func (g Grant) Lock() []string {
	if len(g.Scope) == 0 {
		return []string{string(g.Cap)}
	}
	out := make([]string, len(g.Scope))
	for i, s := range g.Scope {
		out[i] = string(g.Cap) + ":" + s
	}
	return out
}

// Locked is every grant consent covers, as Lock writes them, sorted: the always-allowed ones aren't.
func Locked(gs []Grant) []string {
	var out []string
	for _, g := range gs {
		if !g.Cap.Always() {
			out = append(out, g.Lock()...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Diff is what want asks for that accepted doesn't cover, both as Lock writes them: empty when consent to
// accepted stands. accepted is a lockfile's list, or Locked of the grants consented to; "cap:*" covers any of
// cap's scope.
func Diff(want, accepted []string) []string {
	var out []string
	for _, w := range want {
		c, _, _ := strings.Cut(w, ":")
		if !slices.Contains(accepted, w) && !slices.Contains(accepted, c+":*") && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	slices.Sort(out)
	return out
}

// Explain is Oriel's one line for what g lets a plugin do.
func Explain(g Grant) string {
	scope := strings.Join(g.Scope, ", ")
	switch g.Cap {
	case CapEditorRead:
		return "read open buffers and the worktree files the session can see"
	case CapEditorWrite:
		return "edit buffers, as plugin:<id> in blame and the review ledger"
	case CapInputObserve:
		return "see every key you press, not only its own mappings"
	case CapFSRead:
		return "read files in " + places(g.Scope)
	case CapFSWrite:
		return "write files in " + places(g.Scope)
	case CapNet:
		return "HTTPS to " + scope + " only"
	case CapSecrets:
		var parts []string
		for _, s := range g.Scope {
			name, host, ok := strings.Cut(s, "@")
			if ok {
				parts = append(parts, name+" → bound to "+host)
			} else {
				parts = append(parts, name)
			}
		}
		return strings.Join(parts, ", ") + "; the plugin never sees the value"
	case CapExec:
		return "run " + scope + " as you"
	case CapAgentRead:
		return "read session transcripts and threads: everything you discuss"
	case CapAgentPrompt:
		return "send prompts and comments to a session's agent, which can steer it"
	case CapAgentTools:
		return "offer " + scope + " to agents; tool output reaches the agent"
	case CapGate:
		return "veto and decide hooks: block commits and answer permission prompts automatically"
	case CapSessionControl:
		return "start, stop and resume sessions"
	case CapGit:
		return "commit, push and open PRs through the gateway, still subject to its approvals"
	case CapClipboard:
		return "read the clipboard"
	}
	return string(g.Cap)
}

func places(scope []string) string {
	var out []string
	for _, s := range scope {
		switch s {
		case "worktree":
			out = append(out, "the worktree")
		case "config":
			out = append(out, "your Oriel config")
		case "*":
			out = append(out, "any directory")
		default:
			out = append(out, s)
		}
	}
	return strings.Join(out, ", ")
}
