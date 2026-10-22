package agentrun

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/testrun"
)

// TestRunTimeout bounds a test run the agent asks for.
var TestRunTimeout = 10 * time.Minute

// toolTestRun runs a project's tests with Oriel's runners and records the run as evidence.
func (h *session) toolTestRun(ctx context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", err
	}
	if se.Agent.Sandboxed {
		return "", errors.New("in a sandbox, run tests with your shell: the run is recorded from its output")
	}
	p, ok := se.Primary()
	for _, q := range se.Projects {
		if q.Name == a.Str("project") {
			p, ok = q, true
		}
	}
	if !ok {
		return "", errors.New("this session has no project to test")
	}
	dir := p.Dir
	if d := a.Str("dir"); d != "" {
		if filepath.IsAbs(d) {
			dir = d
		} else {
			dir = filepath.Join(p.Dir, d)
		}
	}
	ws := testrun.Workspace{Root: p.Dir}
	adapters := h.r.tests().Detect(ws)
	if len(adapters) == 0 {
		return "", fmt.Errorf("found no tests Oriel knows how to run in %s", p.Name)
	}
	ad := adapters[0]
	var items []testrun.Item
	if f := a.Str("filter"); f != "" {
		items = []testrun.Item{{Kind: testrun.KindTest, Name: f, File: dir}}
	} else if dir != p.Dir {
		items = []testrun.Item{{Kind: testrun.KindPackage, File: dir}}
	}
	started := h.r.env.Now().UTC()
	x, err := testrun.Start(ctx, testrun.Request{Adapter: ad, Workspace: ws, Items: items, Trigger: "agent", By: testrun.ByAgent,
		Timeout: TestRunTimeout}, nil)
	if err != nil {
		return "", err
	}
	run, err := x.Wait()
	if err != nil && run == nil {
		return "", err
	}
	tr := core.TestRun{ID: hostcore.NewID(4), Command: strings.Join(run.Args, " "), Dir: dir, By: "agent", Started: started, Ended: h.r.env.Now().UTC()}
	for _, r := range run.Tests {
		switch {
		case r.Status == testrun.StatusPassed:
			tr.Passed++
		case r.Status == testrun.StatusSkipped:
			tr.Skipped++
		case r.Status.Failing():
			tr.Failed++
			tr.Failing = append(tr.Failing, r.Key())
		}
	}
	tr.OK = run.Status == testrun.StatusPassed && tr.Failed == 0
	tr.Output = tail(strings.Join(run.Output, "\n")+run.Stderr, 2000)
	h.mu.Lock()
	if len(h.delivered) > 0 {
		tr.Thread = h.delivered[0]
	}
	h.runs = append(h.runs, tr.ID)
	h.mu.Unlock()
	if err := h.r.st.Emit(core.TestRunRecorded{Session: h.id, Run: tr}); err != nil {
		return "", err
	}
	verdict := "passed"
	if !tr.OK {
		verdict = "failed: " + strings.Join(tr.Failing, ", ")
		if len(tr.Failing) == 0 {
			verdict = "failed (" + string(run.Status) + ")"
		}
	}
	return fmt.Sprintf("run:%s %s — %d passed, %d failed, %d skipped\n%s", tr.ID, verdict, tr.Passed, tr.Failed, tr.Skipped, tr.Output), nil
}

func (x *Runner) tests() *testrun.Set {
	x.testsOnce.Do(func() { x.testSet = testrun.Builtin() })
	return x.testSet
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

var testCommand = regexp.MustCompile(`(^|[\s;&|(])(go test|gotestsum|pytest|python -m pytest|cargo test|cargo nextest|npm test|npm run test|pnpm test|yarn test|npx vitest|vitest|npx jest|jest|make test)\b`)

// testRunFrom records a shell command that ran tests as a run, from what it printed, so a claim made after it
// has something to point at.
func testRunFrom(call agent.ToolCall, res agent.ToolResult, now time.Time) (core.TestRun, bool) {
	cmd := hostcore.ParseArgs(call.Input).Str("command")
	if cmd == "" || !testCommand.MatchString(cmd) {
		return core.TestRun{}, false
	}
	r := core.TestRun{Command: cmd, By: "agent", Ended: now, Output: tail(res.Output, 2000)}
	for _, line := range strings.Split(res.Output, "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "--- PASS"), strings.HasPrefix(l, "ok "), strings.HasPrefix(l, "PASS "), strings.HasSuffix(l, " PASSED"), strings.HasPrefix(l, "✓"):
			r.Passed++
		case strings.HasPrefix(l, "--- FAIL"), strings.HasPrefix(l, "FAIL\t"), strings.HasPrefix(l, "FAIL "), strings.HasSuffix(l, " FAILED"), strings.HasPrefix(l, "✗"), strings.HasPrefix(l, "×"):
			r.Failed++
			r.Failing = append(r.Failing, l)
		case strings.HasPrefix(l, "--- SKIP"):
			r.Skipped++
		}
	}
	r.OK = !res.IsError && r.Failed == 0
	return r, true
}

// editOf is the file a tool call edited, and the lines it added and removed.
func editOf(call agent.ToolCall, res agent.ToolResult) (path string, added, removed int, ok bool) {
	tool := call.Tool
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		tool = tool[i+2:]
	}
	switch tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit", "fileChange", "apply_patch":
	default:
		return "", 0, 0, false
	}
	a := hostcore.ParseArgs(call.Input)
	path = cmp.Or(a.Str("file_path"), a.Str("path"))
	if d := res.Diff; d != nil {
		added, removed = d.Added, d.Removed
		path = cmp.Or(path, d.Path)
	}
	return path, added, removed, path != ""
}
