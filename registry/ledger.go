package registry

import (
	"cmp"
	"slices"
	"strings"
)

// LedgerCount is agent-written lines by how far they've been reviewed (agents.md, "Review ledger").
type LedgerCount struct {
	Read, Skimmed, Unread int
}

// Total is every agent-written line counted.
func (l LedgerCount) Total() int { return l.Read + l.Skimmed + l.Unread }

// NotRead is the lines skimmed or unread: what a commit past the gate leaves unreviewed.
func (l LedgerCount) NotRead() int { return l.Skimmed + l.Unread }

// Frac is the share read, 1 with none to read.
func (l LedgerCount) Frac() float64 {
	if l.Total() == 0 {
		return 1
	}
	return float64(l.Read) / float64(l.Total())
}

// Add is l and o together.
func (l LedgerCount) Add(o LedgerCount) LedgerCount {
	return LedgerCount{Read: l.Read + o.Read, Skimmed: l.Skimmed + o.Skimmed, Unread: l.Unread + o.Unread}
}

// LedgerFile is one file's agent-written lines: every one in the branch's change, and those not committed yet.
type LedgerFile struct {
	Path        string // relative to the repo, slash-separated
	All         LedgerCount
	Uncommitted LedgerCount
}

// Ledger is a repo's review ledger: the agent-written lines of its branch's change, against the merge-base with its
// base, uncommitted work included. Ready is false while some files are still being read; the counts are of those
// read so far.
type Ledger struct {
	Root        string
	Ready       bool
	All         LedgerCount
	Uncommitted LedgerCount
	Files       []LedgerFile // those with agent-written lines, by path
}

// File is a file's line of the ledger.
func (l Ledger) File(path string) (LedgerFile, bool) {
	i := slices.IndexFunc(l.Files, func(f LedgerFile) bool { return f.Path == path })
	if i < 0 {
		return LedgerFile{Path: path}, false
	}
	return l.Files[i], true
}

// Of is the ledger of some of its files: their uncommitted lines, or every line.
func (l Ledger) Of(paths []string, uncommitted bool) LedgerCount {
	var out LedgerCount
	for _, f := range l.Files {
		if !slices.Contains(paths, f.Path) {
			continue
		}
		if uncommitted {
			out = out.Add(f.Uncommitted)
		} else {
			out = out.Add(f.All)
		}
	}
	return out
}

// ReviewLedger keeps the review ledger: a built-in counts it from the diff, and a plugin may count it its own way.
// The first by Order answers.
type ReviewLedger struct {
	Name   string
	Order  int
	Get    func(root string) Ledger // root: the repo's work tree
	Source string
}

func (ReviewLedger) ContributionKind() string   { return "review.ledger" }
func (l ReviewLedger) ContributionName() string { return l.Name }

// LedgerOf is root's review ledger from the first ReviewLedger; ok is false when none is registered.
func LedgerOf(r Registry, root string) (l Ledger, ok bool) {
	var ls []ReviewLedger
	for _, c := range r.Contributions("review.ledger") {
		ls = append(ls, c.(ReviewLedger))
	}
	if len(ls) == 0 {
		return Ledger{Root: root}, false
	}
	slices.SortFunc(ls, func(a, b ReviewLedger) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	err := Call(func() error { l = ls[0].Get(root); return nil })
	return l, err == nil
}

// A commit gate's modes (agents.md): hard blocks until "commit anyway", soft asks once, off lets it through.
const (
	GateHard = "hard"
	GateSoft = "soft"
	GateOff  = "off"
)

// CommitCheck is a commit about to be made, for the gates to look at.
type CommitCheck struct {
	Root    string   // the repo's work tree
	Files   []string // the files it commits, repo paths
	Message string
}

// GateResult is what a gate says about a commit. A gate that passes may still report its progress (Done of Total)
// for the Ship page's "before this ships".
type GateResult struct {
	Mode        string // GateHard, GateSoft or GateOff
	Pass        bool
	Pending     bool // still counting: ask again shortly
	Done, Total int
	Reason      string // why it doesn't pass: "21 agent-written lines unread"
	Trailer     string // the trailer a commit past it carries: "Unreviewed-Agent-Lines: 21"
	Unread      int    // what it counts as left to do, for the hooks and the log
	Review      string // a command that works towards passing it: "diff.open"
	ReviewArgs  Args
	ReviewLabel string // "Review 21 lines"
}

// Blocks says the gate stops the commit: hard, and not passing.
func (g GateResult) Blocks() bool { return !g.Pass && g.Mode == GateHard }

// Asks says the gate wants a confirmation first: soft, and not passing.
func (g GateResult) Asks() bool { return !g.Pass && g.Mode == GateSoft }

// CommitGate is a check every commit path runs before it commits (the Explorer, the Ship page, git.commit from a
// script): the review ledger's, a plugin's. Label names it in a list of gates: "Agent lines reviewed".
type CommitGate struct {
	Name, Label string
	Order       int
	Check       func(c CommitCheck) GateResult
	Source      string
}

func (CommitGate) ContributionKind() string   { return "commit.gate" }
func (g CommitGate) ContributionName() string { return g.Name }

// GateCheck is one gate's answer about a commit.
type GateCheck struct {
	Gate CommitGate
	GateResult
}

// CheckCommit asks every commit gate about a commit, by Order; a gate that panics is left out.
func CheckCommit(r Registry, c CommitCheck) []GateCheck {
	var gs []CommitGate
	for _, x := range r.Contributions("commit.gate") {
		gs = append(gs, x.(CommitGate))
	}
	slices.SortFunc(gs, func(a, b CommitGate) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	var out []GateCheck
	for _, g := range gs {
		var res GateResult
		if err := Call(func() error { res = g.Check(c); return nil }); err != nil {
			continue
		}
		out = append(out, GateCheck{Gate: g, GateResult: res})
	}
	return out
}
