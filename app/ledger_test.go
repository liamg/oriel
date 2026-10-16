package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

// ledgerOf is root's review ledger, asked on the loop as any feature asks it.
func (r *running) ledgerOf(root string) registry.Ledger {
	var l registry.Ledger
	r.do(func() error { l, _ = registry.LedgerOf(r.host, root); return nil })
	return l
}

// waitLedger waits for root's ledger to be counted and satisfy ok.
func (r *running) waitLedger(what, root string, ok func(registry.Ledger) bool) registry.Ledger {
	r.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		l := r.ledgerOf(root)
		if l.Ready && ok(l) {
			return l
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("the ledger never had %s: %+v", what, l)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// agentBlameLines is how many non-blank lines of the work tree's files blame gives to Claude: the agent-written lines
// of diffRepo's change that are committed.
func agentBlameLines(t *testing.T, r *gittest.Repo, files ...string) int {
	n := 0
	for _, f := range files {
		author := ""
		for _, l := range strings.Split(r.Git("blame", "--line-porcelain", "--", f), "\n") {
			switch {
			case strings.HasPrefix(l, "author "):
				author = strings.TrimPrefix(l, "author ")
			case strings.HasPrefix(l, "\t") && author == "Claude" && strings.TrimSpace(l) != "":
				n++
			}
		}
	}
	return n
}

// RV-2: ui.md 06, "Review ledger bar: agent-written lines read, skimmed and unread"; agents.md, "unread until you've
// seen it in the diff (… or marked its hunk reviewed)". The agent's committed lines are counted by blame; marking
// the hunk they're in reads them.
// ref: oracle git
func TestLedgerCountsAgentLinesAndMarks(t *testing.T) {
	r := diffRepo(t)
	undo := slowReading(t)
	app := openDiff(t, r, nil)
	defer app.do(func() error { undo(); return nil })
	want := agentBlameLines(t, r, "internal/pricing/round.go", "internal/pricing/currency.go")
	if want == 0 {
		t.Fatal("blame gives Claude no lines")
	}
	app.clickText("currency.go") // outside a session, the ledger counts the files opened
	app.waitLedger("currency.go's lines", r.Dir, func(l registry.Ledger) bool { _, ok := l.File("internal/pricing/currency.go"); return ok })
	app.clickText("internal/pricing/currency.go") // folded again
	app.clickText("round.go")
	l := app.waitLedger("every agent line", r.Dir, func(l registry.Ledger) bool { return l.All.Total() == want })
	if l.All.Read != 0 || l.Uncommitted.Total() != 0 {
		t.Errorf("before any review: %+v", l)
	}
	app.waitFrame("the ledger bar", func(s string) bool {
		return containsAll(s, "REVIEW LEDGER", "You have read", "/ "+fmt.Sprint(want), "agent-written lines", "UNREAD · 2 LINES")
	})
	app.typeKeys("]h")
	app.typeKeys("r")
	inHunk := 6 // roundHalfEven's new body: the hunk the agent's commit changed
	app.waitLedger("the hunk's lines read", r.Dir, func(l registry.Ledger) bool { return l.All.Read == inHunk })
	app.waitFrame("the count read", func(s string) bool { return strings.Contains(s, fmt.Sprint(inHunk)) })
}

// slowReading makes lines on screen take an hour to read, so a test can see them skimmed; it returns the undo, to
// run on the loop.
func slowReading(t *testing.T) func() {
	read, skim, line := ledgerRead, ledgerSkim, ledgerReadLine
	ledgerRead, ledgerSkim = time.Hour, 50*time.Millisecond
	return func() { ledgerRead, ledgerSkim, ledgerReadLine = read, skim, line }
}

// RV-2: agents.md, "unread until you've seen it in the diff (scrolled past it …)": lines that stay on screen are
// skimmed first, then read.
func TestLedgerLinesOnScreenAreSkimmedThenRead(t *testing.T) {
	r := diffRepo(t)
	undo := slowReading(t)
	app := openDiff(t, r, nil)
	l := app.waitLedger("lines on screen skimmed", r.Dir, func(l registry.Ledger) bool { return l.All.Skimmed > 0 && l.All.Read == 0 })
	app.waitFrame("the skimmed count", func(s string) bool { return strings.Contains(s, fmt.Sprintf("%d skimmed", l.All.Skimmed)) })
	app.do(func() error { ledgerRead, ledgerReadLine = 100*time.Millisecond, 0; return nil })
	defer app.do(func() error { undo(); return nil })
	app.typeKeys("j") // a frame, to look again
	app.waitLedger("lines on screen read", r.Dir, func(l registry.Ledger) bool { return l.All.Read > 0 && l.All.Skimmed == 0 })
}

// gateSession is a work session whose project has round.go committed at its base and the agent's edit of it in the
// work tree, staged, not committed: the agent's transcript has the edit, which makes its lines the agent's.
func gateSession(t *testing.T) (*running, *agenttest.FakeSession, string) {
	t.Helper()
	r, a, _ := agentSession(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(roundBase), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	git(t, dir, "config", "user.name", "Ada")
	git(t, dir, "config", "user.email", "ada@example.com")
	os.WriteFile(path, []byte(roundAgent), 0o644)
	git(t, dir, "add", "round.go")
	d := &core.FileDiff{Path: path}
	for _, l := range strings.Split(roundAgent, "\n") {
		if !strings.Contains(roundBase, l+"\n") {
			d.Lines = append(d.Lines, core.DiffLine{Op: "+", Text: l})
		}
	}
	a.Emit(agent.ToolCall{ID: "e1", Tool: "Edit", Input: toolInput(map[string]string{"file_path": path})})
	a.Emit(agent.ToolResult{ID: "e1", Diff: d})
	r.do(func() error { return r.host.Run(context.Background(), "git.refresh", nil) })
	return r, a, dir
}

// RV-2, done when: "the gate blocks; "commit anyway" writes the trailer". agents.md: "work and plan: hard. Commit and
// PR are blocked until every agent-written line is read. "Commit anyway" is one keypress, and it's recorded in a
// commit trailer (Unreviewed-Agent-Lines: 38) and in the session log."
// ref: oracle git
func TestCommitGateBlocksAndCommitAnywayWritesTheTrailer(t *testing.T) {
	r, _, dir := gateSession(t)
	added := 0
	for _, l := range strings.Split(git(t, dir, "diff", "--cached", "-U0"), "\n") {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") && strings.TrimSpace(l[1:]) != "" {
			added++
		}
	}
	r.waitLedger("the agent's uncommitted lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread == added })
	head := git(t, dir, "rev-parse", "HEAD")
	r.do(func() error {
		return r.host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: round half to even"})
	})
	r.waitFrame("the gate", func(s string) bool {
		return containsAll(s, "COMMIT GATED", "Agent lines reviewed", fmt.Sprintf("0 / %d", added), fmt.Sprintf("%d agent-written lines unread", added),
			fmt.Sprintf("Unreviewed-Agent-Lines: %d", added), "Commit anyway")
	})
	time.Sleep(200 * time.Millisecond)
	if got := git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("the gate didn't block the commit")
	}
	r.typeKeys("a")
	deadline := time.Now().Add(5 * time.Second)
	for git(t, dir, "rev-parse", "HEAD") == head {
		if time.Now().After(deadline) {
			t.Fatal("commit anyway didn't commit")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if got := strings.TrimSpace(git(t, dir, "log", "-1", "--format=%(trailers:key=Unreviewed-Agent-Lines,valueonly)")); got != fmt.Sprint(added) {
		t.Errorf("the trailer reads %q, want %d", got, added)
	}
	if got := strings.TrimSpace(git(t, dir, "log", "-1", "--format=%s")); got != "fix: round half to even" {
		t.Errorf("subject %q", got)
	}
	st := waitState(t, r.store, "the override in the session", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && len(se.Snapshot().Overrides) == 1
	})
	se, _ := st.Session("w")
	o := se.Snapshot().Overrides[0]
	if o.Gate != "ledger" || o.Mode != core.GateHard || o.Unread != added || o.Trailer == "" {
		t.Errorf("override: %+v", o)
	}
	waitState(t, r.store, "the session's log", func(st core.State) bool {
		for _, n := range st.Notifications {
			if n.Session == "w" && n.ID == "git.commit.gate" && strings.Contains(n.Text, "Unreviewed-Agent-Lines") {
				return true
			}
		}
		return false
	})
}

// RV-2: agents.md, "jobs: soft. One confirmation, logged" (here set by ledger.gate for the session); and once every
// line is read, the gate lets the commit through without asking.
func TestCommitGateSoftAsksOnceAndReadLinesPass(t *testing.T) {
	r, _, dir := gateSession(t)
	r.waitLedger("the agent's lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread > 0 })
	r.do(func() error {
		return r.host.Options().Set("ledger.gate", registry.LayerSession, registry.At{Session: "w"}, core.GateSoft)
	})
	head := git(t, dir, "rev-parse", "HEAD")
	r.do(func() error {
		return r.host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: soft"})
	})
	r.waitText("COMMIT WITH UNREAD LINES?")
	r.typeKeys("<CR>")
	waitCommit(t, dir, head)
	if got := strings.TrimSpace(git(t, dir, "log", "-1", "--format=%(trailers:key=Unreviewed-Agent-Lines,valueonly)")); got != "" {
		t.Errorf("a soft gate wrote a trailer: %q", got)
	}
	waitState(t, r.store, "the soft override", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && len(se.Snapshot().Overrides) == 1 && se.Snapshot().Overrides[0].Mode == core.GateSoft
	})
	// Now read: a new agent edit, its hunk marked reviewed, commits straight away under the hard gate.
	r.do(func() error {
		return r.host.Options().Set("ledger.gate", registry.LayerSession, registry.At{Session: "w"}, core.GateHard)
	})
	os.WriteFile(filepath.Join(dir, "round.go"), []byte(roundWork), 0o644)
	git(t, dir, "add", "round.go")
	r.do(func() error { return r.host.Run(context.Background(), "git.refresh", nil) })
	r.do(func() error { return r.host.Run(context.Background(), "diff.open", registry.Args{"path": dir}) })
	r.waitText("func RoundMinor")
	r.do(func() error {
		return r.host.Run(context.Background(), "diff.review.file", registry.Args{"path": "round.go", "reviewed": true})
	})
	r.waitLedger("every line read", dir, func(l registry.Ledger) bool { return l.All.NotRead() == 0 })
	head = git(t, dir, "rev-parse", "HEAD")
	r.do(func() error {
		return r.host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: read"})
	})
	waitCommit(t, dir, head)
}

func waitCommit(t *testing.T, dir, head string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for git(t, dir, "rev-parse", "HEAD") == head {
		if time.Now().After(deadline) {
			t.Fatal("nothing was committed")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// RV-2: PLAN.md, "Repos can override it": a project's ledger.gate turns the gate off for its repo, and the
// commit goes through; the Commit hook still sees it, and a veto stops it.
func TestCommitGateProjectOverrideAndCommitHook(t *testing.T) {
	r, _, dir := gateSession(t)
	r.waitLedger("the agent's lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread > 0 })
	r.do(func() error {
		return r.host.Options().Set("ledger.gate", registry.LayerProject, registry.At{Project: dir}, core.GateOff)
	})
	var seen CommitEvent
	r.do(func() error {
		r.host.Hook(registry.Hook{Event: "Commit", Kind: registry.Veto, Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			seen = ev.(CommitEvent)
			return registry.Verdict{Veto: strings.Contains(seen.Message, "wip"), Reason: "no wip commits"}, nil
		}})
		return nil
	})
	head := git(t, dir, "rev-parse", "HEAD")
	err := r.do(func() error {
		return r.host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "wip"})
	})
	if err == nil || !strings.Contains(err.Error(), "no wip commits") {
		t.Errorf("the veto: %v", err)
	}
	r.do(func() error {
		return r.host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: off"})
	})
	waitCommit(t, dir, head)
	if seen.Project == "" || len(seen.Files) != 1 || seen.Files[0] != "round.go" {
		t.Errorf("the hook saw %+v", seen)
	}
}

// RV-2: "a commit with nothing to take, or no message": nothing staged is an error; no message opens the dialog.
func TestCommitAsksForAMessage(t *testing.T) {
	r, _, dir := gateSession(t)
	r.waitLedger("the agent's lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread > 0 })
	r.do(func() error {
		return r.host.Options().Set("ledger.gate", registry.LayerSession, registry.At{Session: "w"}, core.GateOff)
	})
	if err := r.do(func() error { return r.host.Run(context.Background(), "git.commit", registry.Args{"path": dir}) }); err != nil {
		t.Fatal(err)
	}
	r.waitText("Commits what's staged.")
	head := git(t, dir, "rev-parse", "HEAD")
	r.typeText("feat: typed")
	r.waitText("feat: typed")
	r.clickText("Commit")
	waitCommit(t, dir, head)
	if got := strings.TrimSpace(git(t, dir, "log", "-1", "--format=%s")); got != "feat: typed" {
		t.Errorf("subject %q", got)
	}
	r.waitGone("Commits what's staged.")
	deadline := time.Now().Add(5 * time.Second)
	for r.do(func() error {
		return r.host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "x"})
	}) == nil {
		if time.Now().After(deadline) {
			t.Fatal("nothing staged, and git.commit didn't say so")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// RV-2: ui.md 01c, "Each file has a read bar (the review ledger per file). Sort by tree, flat, or "by review"
// (unread first)".
func TestExplorerByReview(t *testing.T) {
	r := diffRepo(t)
	app := startApp(t, t.TempDir(), r.Dir, nil)
	app.typeKeys("<M-1>")
	app.waitText("CHANGED 2")
	app.waitLedger("no file opened", r.Dir, func(l registry.Ledger) bool { return l.Ready && len(l.Files) == 0 })
	app.clickText("currency.go") // outside a session, the ledger counts the files opened
	app.clickText("round.go")
	app.waitLedger("both files", r.Dir, func(l registry.Ledger) bool { return len(l.Files) == 2 })
	app.typeKeys("<M-1>")
	app.clickText("by review")
	app.waitFrame("round.go, with more unread, first", func(s string) bool {
		i, j := strings.Index(s, "internal/pricing/r"), strings.Index(s, "internal/pricing/cu")
		return i >= 0 && j > i
	})
	if n := app.access("read 0%"); n == nil {
		t.Error("no read bar")
	}
}

// RV-2: ui.md 01, the summary's session stats: "review ledger"; the agent's lines read of all of them.
func TestSummaryShowsReviewed(t *testing.T) {
	r, _, dir := gateSession(t)
	l := r.waitLedger("the agent's lines", dir, func(l registry.Ledger) bool { return l.All.Unread > 0 })
	r.do(func() error { return r.host.Run(context.Background(), "requests.summary", nil) })
	r.waitFrame("REVIEWED", func(s string) bool { return containsAll(s, "REVIEWED", fmt.Sprintf("0/%d", l.All.Total())) })
}

// The ledger over hundreds of changed files no agent commit touched runs no more gits than over a few: their old
// versions come through one, and there's nothing to blame. (The status, read every so often, is counted apart.)
func TestLedgerGitsDontGrowWithFiles(t *testing.T) {
	gits := func(n int) int {
		dir := changedRepo(t, n)
		runs := gittest.CountGit(t)
		s := newScene(t, "", dir, mockupSize)
		s.sh.git.wait()
		ledger := []string{"cat-file", "blame", "log", "config"}
		before := runs(ledger...)
		s.keyCommand(t, "<M-1>")
		if !s.Until(30*time.Second, func() bool {
			l, ok := registry.LedgerOf(s.reg, dir)
			return ok && l.Ready
		}) {
			t.Fatalf("the ledger over %d files was never ready", n)
		}
		return runs(ledger...) - before
	}
	few, many := gits(5), gits(300)
	t.Logf("gits for the ledger over 5 files: %d; over 300: %d", few, many)
	if many > few+3 {
		t.Errorf("the ledger over 300 files ran %d gits, against %d over 5", many, few)
	}
}

// changedRepo is a repo with n files of 100 lines changed since its one commit, every fifth line.
func changedRepo(t *testing.T, n int) string {
	r := gittest.New(t)
	before, after := map[string]string{}, map[string]string{}
	for i := range n {
		var b, a strings.Builder
		for l := range 100 {
			fmt.Fprintf(&b, "line %d\n", l)
			if l%5 == 0 {
				fmt.Fprintf(&a, "line %d, changed\n", l)
			} else {
				fmt.Fprintf(&a, "line %d\n", l)
			}
		}
		p := fmt.Sprintf("pkg%d/f%03d.txt", i%20, i)
		before[p], after[p] = b.String(), a.String()
	}
	r.Write(before)
	r.Commit("Ada", "base")
	r.Write(after)
	return r.Dir
}
