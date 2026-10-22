package gitui_test

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
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/gitui"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

// ledgerOf is root's review ledger, asked on the loop as any feature asks it.
func ledgerOf(r *apptest.Window, root string) registry.Ledger {
	var l registry.Ledger
	r.Do(func() error { l, _ = registry.LedgerOf(r.Host, root); return nil })
	return l
}

// waitLedger waits for root's ledger to be counted and satisfy ok.
func waitLedger(r *apptest.Window, what, root string, ok func(registry.Ledger) bool) registry.Ledger {
	r.T.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		l := ledgerOf(r, root)
		if l.Ready && ok(l) {
			return l
		}
		if time.Now().After(deadline) {
			r.T.Fatalf("the ledger never had %s: %+v", what, l)
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

// The review ledger bar counts agent-written lines read, skimmed and unread; a line is unread until it's seen in the
// diff or its hunk is marked reviewed. The agent's committed lines are counted by blame; marking
// the hunk they're in reads them.
// ref: oracle git
func TestLedgerCountsAgentLinesAndMarks(t *testing.T) {
	r := rig.DiffRepo(t)
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir})
	slowReading(app)
	showDiff(app)
	want := agentBlameLines(t, r, "internal/pricing/round.go", "internal/pricing/currency.go")
	if want == 0 {
		t.Fatal("blame gives Claude no lines")
	}
	app.ClickText("currency.go") // outside a session, the ledger counts the files opened
	waitLedger(app, "currency.go's lines", r.Dir, func(l registry.Ledger) bool { _, ok := l.File("internal/pricing/currency.go"); return ok })
	app.ClickText("internal/pricing/currency.go") // folded again
	app.ClickText("round.go")
	l := waitLedger(app, "every agent line", r.Dir, func(l registry.Ledger) bool { return l.All.Total() == want })
	if l.All.Read != 0 || l.Uncommitted.Total() != 0 {
		t.Errorf("before any review: %+v", l)
	}
	app.WaitFrame("the ledger bar", func(s string) bool {
		return apptest.HasAll(s, "REVIEW LEDGER", "You have read", "/ "+fmt.Sprint(want), "agent-written lines", "UNREAD · 2 LINES")
	})
	app.TypeKeys("]h")
	app.TypeKeys("r")
	inHunk := 6 // roundHalfEven's new body: the hunk the agent's commit changed
	waitLedger(app, "the hunk's lines read", r.Dir, func(l registry.Ledger) bool { return l.All.Read == inHunk })
	app.WaitFrame("the count read", func(s string) bool { return strings.Contains(s, fmt.Sprint(inHunk)) })
}

// slowReading makes lines on screen take an hour to read, so a test can see them skimmed.
func slowReading(app *apptest.Window) {
	app.Do(func() error {
		apptest.WindowPlugin[*gitui.Diff](app, "diff").SetLedgerTimes(time.Hour, 50*time.Millisecond, 150*time.Millisecond)
		return nil
	})
}

// A line is unread until it's scrolled past in the diff: lines that stay on screen are
// skimmed first, then read.
func TestLedgerLinesOnScreenAreSkimmedThenRead(t *testing.T) {
	r := rig.DiffRepo(t)
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir})
	slowReading(app)
	showDiff(app)
	l := waitLedger(app, "lines on screen skimmed", r.Dir, func(l registry.Ledger) bool { return l.All.Skimmed > 0 && l.All.Read == 0 })
	app.WaitFrame("the skimmed count", func(s string) bool { return strings.Contains(s, fmt.Sprintf("%d skimmed", l.All.Skimmed)) })
	app.Do(func() error {
		apptest.WindowPlugin[*gitui.Diff](app, "diff").SetLedgerTimes(100*time.Millisecond, 50*time.Millisecond, 0)
		return nil
	})
	app.TypeKeys("j") // a frame, to look again
	waitLedger(app, "lines on screen read", r.Dir, func(l registry.Ledger) bool { return l.All.Read > 0 && l.All.Skimmed == 0 })
}

// gateSession is a work session whose project has round.go committed at its base and the agent's edit of it in the
// work tree, staged, not committed: the agent's transcript has the edit, which makes its lines the agent's.
func gateSession(t *testing.T) (*apptest.Window, *agenttest.FakeSession, string) {
	t.Helper()
	r, a, _ := apptest.AgentWindow(t)
	dir := r.ProjectDir("w")
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(rig.RoundBase), 0o644)
	rig.Git(t, dir, "add", "-A")
	rig.Git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	rig.Git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	rig.Git(t, dir, "config", "user.name", "Ada")
	rig.Git(t, dir, "config", "user.email", "ada@example.com")
	os.WriteFile(path, []byte(rig.RoundAgent), 0o644)
	rig.Git(t, dir, "add", "round.go")
	d := &core.FileDiff{Path: path}
	for _, l := range strings.Split(rig.RoundAgent, "\n") {
		if !strings.Contains(rig.RoundBase, l+"\n") {
			d.Lines = append(d.Lines, core.DiffLine{Op: "+", Text: l})
		}
	}
	a.Emit(agent.ToolCall{ID: "e1", Tool: "Edit", Input: rig.ToolInput(map[string]string{"file_path": path})})
	a.Emit(agent.ToolResult{ID: "e1", Diff: d})
	r.Do(func() error { return r.Host.Run(context.Background(), "git.refresh", nil) })
	return r, a, dir
}

// The hard gate (work and plan sessions) blocks commit and PR until every agent-written line is read. "Commit anyway"
// is one keypress, and it's recorded in a commit trailer (Unreviewed-Agent-Lines: 38) and in the session log.
// ref: oracle git
func TestCommitGateBlocksAndCommitAnywayWritesTheTrailer(t *testing.T) {
	r, _, dir := gateSession(t)
	added := 0
	for _, l := range strings.Split(rig.Git(t, dir, "diff", "--cached", "-U0"), "\n") {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") && strings.TrimSpace(l[1:]) != "" {
			added++
		}
	}
	waitLedger(r, "the agent's uncommitted lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread == added })
	head := rig.Git(t, dir, "rev-parse", "HEAD")
	r.Do(func() error {
		return r.Host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: round half to even"})
	})
	r.WaitFrame("the gate", func(s string) bool {
		return apptest.HasAll(s, "COMMIT GATED", "Agent lines reviewed", fmt.Sprintf("0 / %d", added), fmt.Sprintf("%d agent-written lines unread", added),
			fmt.Sprintf("Unreviewed-Agent-Lines: %d", added), "Commit anyway")
	})
	time.Sleep(200 * time.Millisecond)
	if got := rig.Git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("the gate didn't block the commit")
	}
	r.TypeKeys("a")
	deadline := time.Now().Add(5 * time.Second)
	for rig.Git(t, dir, "rev-parse", "HEAD") == head {
		if time.Now().After(deadline) {
			t.Fatal("commit anyway didn't commit")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if got := strings.TrimSpace(rig.Git(t, dir, "log", "-1", "--format=%(trailers:key=Unreviewed-Agent-Lines,valueonly)")); got != fmt.Sprint(added) {
		t.Errorf("the trailer reads %q, want %d", got, added)
	}
	if got := strings.TrimSpace(rig.Git(t, dir, "log", "-1", "--format=%s")); got != "fix: round half to even" {
		t.Errorf("subject %q", got)
	}
	st := r.UntilState("the override in the session", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && len(se.Snapshot().Overrides) == 1
	})
	se, _ := st.Session("w")
	o := se.Snapshot().Overrides[0]
	if o.Gate != "ledger" || o.Mode != core.GateHard || o.Unread != added || o.Trailer == "" {
		t.Errorf("override: %+v", o)
	}
	r.UntilState("the session's log", func(st core.State) bool {
		for _, n := range st.Notifications {
			if n.Session == "w" && n.ID == "git.commit.gate" && strings.Contains(n.Text, "Unreviewed-Agent-Lines") {
				return true
			}
		}
		return false
	})
}

// The soft gate (jobs): one confirmation, logged (here set by ledger.gate for the session); and once every
// line is read, the gate lets the commit through without asking.
func TestCommitGateSoftAsksOnceAndReadLinesPass(t *testing.T) {
	r, _, dir := gateSession(t)
	waitLedger(r, "the agent's lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread > 0 })
	r.Do(func() error {
		return r.Host.Options().Set("ledger.gate", registry.LayerSession, registry.At{Session: "w"}, core.GateSoft)
	})
	head := rig.Git(t, dir, "rev-parse", "HEAD")
	r.Do(func() error {
		return r.Host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: soft"})
	})
	r.WaitText("COMMIT WITH UNREAD LINES?")
	r.TypeKeys("<CR>")
	waitCommit(t, dir, head)
	if got := strings.TrimSpace(rig.Git(t, dir, "log", "-1", "--format=%(trailers:key=Unreviewed-Agent-Lines,valueonly)")); got != "" {
		t.Errorf("a soft gate wrote a trailer: %q", got)
	}
	r.UntilState("the soft override", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && len(se.Snapshot().Overrides) == 1 && se.Snapshot().Overrides[0].Mode == core.GateSoft
	})
	// Now read: a new agent edit, its hunk marked reviewed, commits straight away under the hard gate.
	r.Do(func() error {
		return r.Host.Options().Set("ledger.gate", registry.LayerSession, registry.At{Session: "w"}, core.GateHard)
	})
	os.WriteFile(filepath.Join(dir, "round.go"), []byte(rig.RoundWork), 0o644)
	rig.Git(t, dir, "add", "round.go")
	r.Do(func() error { return r.Host.Run(context.Background(), "git.refresh", nil) })
	r.Do(func() error { return r.Host.Run(context.Background(), "diff.open", registry.Args{"path": dir}) })
	r.WaitText("func RoundMinor")
	r.Do(func() error {
		return r.Host.Run(context.Background(), "diff.review.file", registry.Args{"path": "round.go", "reviewed": true})
	})
	waitLedger(r, "every line read", dir, func(l registry.Ledger) bool { return l.All.NotRead() == 0 })
	head = rig.Git(t, dir, "rev-parse", "HEAD")
	r.Do(func() error {
		return r.Host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: read"})
	})
	waitCommit(t, dir, head)
}

func waitCommit(t *testing.T, dir, head string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for rig.Git(t, dir, "rev-parse", "HEAD") == head {
		if time.Now().After(deadline) {
			t.Fatal("nothing was committed")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// Repos can override the gate: a project's ledger.gate turns the gate off for its repo, and the
// commit goes through; the Commit hook still sees it, and a veto stops it.
func TestCommitGateProjectOverrideAndCommitHook(t *testing.T) {
	r, _, dir := gateSession(t)
	waitLedger(r, "the agent's lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread > 0 })
	r.Do(func() error {
		return r.Host.Options().Set("ledger.gate", registry.LayerProject, registry.At{Project: dir}, core.GateOff)
	})
	var seen gitui.CommitEvent
	r.Do(func() error {
		r.Host.Hook(registry.Hook{Event: "Commit", Kind: registry.Veto, Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
			seen = ev.(gitui.CommitEvent)
			return registry.Verdict{Veto: strings.Contains(seen.Message, "wip"), Reason: "no wip commits"}, nil
		}})
		return nil
	})
	head := rig.Git(t, dir, "rev-parse", "HEAD")
	err := r.Do(func() error {
		return r.Host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "wip"})
	})
	if err == nil || !strings.Contains(err.Error(), "no wip commits") {
		t.Errorf("the veto: %v", err)
	}
	r.Do(func() error {
		return r.Host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "fix: off"})
	})
	waitCommit(t, dir, head)
	if seen.Project == "" || len(seen.Files) != 1 || seen.Files[0] != "round.go" {
		t.Errorf("the hook saw %+v", seen)
	}
}

// A commit with nothing to take, or no message: nothing staged is an error; no message opens the dialog.
func TestCommitAsksForAMessage(t *testing.T) {
	r, _, dir := gateSession(t)
	waitLedger(r, "the agent's lines", dir, func(l registry.Ledger) bool { return l.Uncommitted.Unread > 0 })
	r.Do(func() error {
		return r.Host.Options().Set("ledger.gate", registry.LayerSession, registry.At{Session: "w"}, core.GateOff)
	})
	if err := r.Do(func() error { return r.Host.Run(context.Background(), "git.commit", registry.Args{"path": dir}) }); err != nil {
		t.Fatal(err)
	}
	r.WaitText("Commits what's staged.")
	head := rig.Git(t, dir, "rev-parse", "HEAD")
	r.TypeText("feat: typed")
	r.WaitText("feat: typed")
	r.ClickText("Commit")
	waitCommit(t, dir, head)
	if got := strings.TrimSpace(rig.Git(t, dir, "log", "-1", "--format=%s")); got != "feat: typed" {
		t.Errorf("subject %q", got)
	}
	r.WaitGone("Commits what's staged.")
	deadline := time.Now().Add(5 * time.Second)
	for r.Do(func() error {
		return r.Host.Run(context.Background(), "git.commit", registry.Args{"path": dir, "message": "x"})
	}) == nil {
		if time.Now().After(deadline) {
			t.Fatal("nothing staged, and git.commit didn't say so")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Each file has a read bar (the review ledger per file). Sort by tree, flat, or "by review" (unread first).
func TestExplorerByReview(t *testing.T) {
	r := rig.DiffRepo(t)
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir})
	app.TypeKeys("<M-1>")
	app.WaitText("CHANGED 2")
	waitLedger(app, "no file opened", r.Dir, func(l registry.Ledger) bool { return l.Ready && len(l.Files) == 0 })
	app.ClickText("currency.go") // outside a session, the ledger counts the files opened
	app.ClickText("round.go")
	waitLedger(app, "both files", r.Dir, func(l registry.Ledger) bool { return len(l.Files) == 2 })
	app.TypeKeys("<M-1>")
	app.ClickText("by review")
	app.WaitFrame("round.go, with more unread, first", func(s string) bool {
		i, j := strings.Index(s, "internal/pricing/r"), strings.Index(s, "internal/pricing/cu")
		return i >= 0 && j > i
	})
	if n := app.Access("read 0%"); n == nil {
		t.Error("no read bar")
	}
}

// The summary's session stats: "review ledger", the agent's lines read of all of them.
func TestSummaryShowsReviewed(t *testing.T) {
	r, _, dir := gateSession(t)
	l := waitLedger(r, "the agent's lines", dir, func(l registry.Ledger) bool { return l.All.Unread > 0 })
	r.Do(func() error { return r.Host.Run(context.Background(), "requests.summary", nil) })
	r.WaitFrame("REVIEWED", func(s string) bool { return apptest.HasAll(s, "REVIEWED", fmt.Sprintf("0/%d", l.All.Total())) })
}

// The ledger over hundreds of changed files no agent commit touched runs no more gits than over a few: their old
// versions come through one, and there's nothing to blame. (The status, read every so often, is counted apart.)
func TestLedgerGitsDontGrowWithFiles(t *testing.T) {
	gits := func(n int) int {
		dir := rig.ChangedRepo(t, n)
		runs := gittest.CountGit(t)
		s := apptest.New(t, apptest.Options{WorkDir: dir})
		gitOf(s).Wait()
		ledger := []string{"cat-file", "blame", "log", "config"}
		before := runs(ledger...)
		s.Press("<M-1>")
		if !s.Headless.Until(30*time.Second, func() bool {
			l, ok := registry.LedgerOf(s.Registry(), dir)
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
