package gitlab

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// The code host's functions (registry.CodeHost), in Oriel's shapes.

// Requested are the open merge requests that ask you to review, each with where you are with it.
func (c Client) Requested(ctx context.Context) ([]registry.ReviewRequest, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	var mrs []MR
	path := "/merge_requests?scope=all&state=opened&per_page=100&reviewer_username=" + me.Username
	if err := c.call(ctx, "GET", path, nil, &mrs); err != nil {
		return nil, err
	}
	out := []registry.ReviewRequest{}
	for _, m := range mrs {
		r := registry.ReviewRequest{URL: m.WebURL, Repo: m.Path(), Number: m.IID, Title: m.Title, Author: m.Author.Username, Draft: m.Draft,
			Asked: when(m.CreatedAt), State: registry.ReviewNew}
		rs, err := c.Reviewers(ctx, m.Path(), m.IID)
		if err != nil {
			return nil, err
		}
		for _, rv := range rs {
			if rv.User.Username != me.Username {
				continue
			}
			if t := when(rv.CreatedAt); !t.IsZero() {
				r.Asked = t
			}
			r.State = reviewState(rv.State)
		}
		out = append(out, r)
	}
	return out, nil
}

// reviewState is a reviewer's state on GitLab as the Reviews page says it.
func reviewState(s string) string {
	switch s {
	case "approved":
		return registry.ReviewApproved
	case "requested_changes":
		return registry.ReviewChanges
	case "reviewed":
		return registry.ReviewCommented
	}
	return registry.ReviewNew
}

// Lookup is a merge request in full.
func (c Client) Lookup(ctx context.Context, url string) (registry.ReviewDetail, error) {
	path, iid, err := ParseMRURL(url)
	if err != nil {
		return registry.ReviewDetail{}, err
	}
	m, err := c.MR(ctx, path, iid)
	if err != nil {
		return registry.ReviewDetail{}, err
	}
	d := registry.ReviewDetail{Request: registry.ReviewRequest{URL: m.WebURL, Repo: path, Number: m.IID, Title: m.Title, Author: m.Author.Username,
		Draft: m.Draft, Asked: when(m.CreatedAt)}, Base: m.TargetBranch, Head: m.SourceBranch, Labels: m.Labels, Body: m.Description,
		Clone: "https://" + cmp.Or(c.Host, Public.Host) + "/" + path + ".git"}
	switch {
	case m.HasConflicts:
		d.Mergeable = "conflicts"
	case m.MergeStatus == "need_rebase":
		d.Mergeable = "yes · behind " + m.TargetBranch
	case m.MergeStatus != "":
		d.Mergeable = "yes"
	}
	rs, err := c.Reviewers(ctx, path, iid)
	if err != nil {
		return d, err
	}
	for _, rv := range rs {
		state := map[string]string{"approved": "approved", "requested_changes": "changes", "reviewed": "commented"}[rv.State]
		d.Reviewers = append(d.Reviewers, registry.PRReviewer{Login: rv.User.Username, State: cmp.Or(state, "requested")})
	}
	diffs, err := c.Diffs(ctx, path, iid)
	if err != nil {
		return d, err
	}
	for _, f := range diffs {
		fd := core.PatchDiff(f.NewPath, f.Diff)
		adds, dels := fd.Added, fd.Removed
		d.Files = append(d.Files, registry.PRFileChange{Path: f.NewPath, Adds: adds, Dels: dels})
		d.Request.Adds, d.Request.Dels = d.Request.Adds+adds, d.Request.Dels+dels
	}
	d.Request.Files = len(d.Files)
	if m.HeadPipeline != nil {
		if d.Checks, err = c.pipelineChecks(ctx, path, m.HeadPipeline.ID); err != nil {
			return d, err
		}
	}
	return d, nil
}

// Checkout is where a review fetches a merge request from: GitLab keeps each one's head at merge-requests/N/head.
func Checkout(url string) (registry.PRCheckout, error) {
	path, iid, err := ParseMRURL(url)
	if err != nil {
		return registry.PRCheckout{}, err
	}
	return registry.PRCheckout{Remote: path, Ref: fmt.Sprintf("merge-requests/%d/head", iid)}, nil
}

// draftPrefix is how GitLab marks a draft in its title.
var draftPrefix = regexp.MustCompile(`(?i)^\s*(\[draft\]|draft:|\(draft\))\s*`)

// prOf is a merge request as Oriel's code host tells it: its title without the draft mark.
func prOf(m MR) *registry.PR {
	p := &registry.PR{Number: m.IID, ID: fmt.Sprint(m.ID), Title: draftPrefix.ReplaceAllString(m.Title, ""), Body: m.Description, URL: m.WebURL,
		Author: m.Author.Username, State: "open", Draft: m.Draft, Branch: m.SourceBranch, Base: m.TargetBranch, Head: m.SHA}
	switch m.State {
	case "merged":
		p.State, p.Merged = "closed", true
	case "closed", "locked":
		p.State = "closed"
	}
	for _, u := range m.Reviewers {
		p.Reviewers = append(p.Reviewers, u.Username)
	}
	return p
}

// ForBranch is the branch's merge request: its open one, else the newest; nil for none.
func (c Client) ForBranch(ctx context.Context, path, branch string) (*registry.PR, error) {
	var mrs []MR
	q := fmt.Sprintf("/projects/%s/merge_requests?state=all&per_page=20&source_branch=%s", project(path), queryEscape(branch))
	if err := c.call(ctx, "GET", q, nil, &mrs); err != nil {
		return nil, err
	}
	var best *MR
	for i, m := range mrs {
		if m.SourceBranch != branch {
			continue
		}
		switch {
		case best == nil,
			m.State == "opened" && best.State != "opened",
			m.State == best.State && when(m.CreatedAt).After(when(best.CreatedAt)):
			best = &mrs[i]
		}
	}
	if best == nil {
		return nil, nil
	}
	return prOf(*best), nil
}

// Create raises a merge request; a draft's title carries GitLab's mark.
func (c Client) Create(ctx context.Context, path string, n registry.NewPR) (*registry.PR, error) {
	title := n.Title
	if n.Draft {
		title = "Draft: " + title
	}
	var m MR
	in := map[string]any{"source_branch": n.Branch, "target_branch": n.Base, "title": title, "description": n.Body}
	if err := c.call(ctx, "POST", "/projects/"+project(path)+"/merge_requests", in, &m); err != nil {
		return nil, err
	}
	return prOf(m), nil
}

// Edit sets a merge request's title and description, keeping a draft one a draft.
func (c Client) Edit(ctx context.Context, path string, pr registry.PR, title, body string) (*registry.PR, error) {
	title = draftPrefix.ReplaceAllString(title, "")
	if pr.Draft {
		title = "Draft: " + title
	}
	var m MR
	in := map[string]any{"title": title, "description": body}
	if err := c.call(ctx, "PUT", fmt.Sprintf("/projects/%s/merge_requests/%d", project(path), pr.Number), in, &m); err != nil {
		return nil, err
	}
	return prOf(m), nil
}

// Ready takes a merge request out of draft: its title without the mark.
func (c Client) Ready(ctx context.Context, path string, pr registry.PR) error {
	m, err := c.MR(ctx, path, pr.Number)
	if err != nil {
		return err
	}
	in := map[string]any{"title": draftPrefix.ReplaceAllString(m.Title, "")}
	return c.call(ctx, "PUT", fmt.Sprintf("/projects/%s/merge_requests/%d", project(path), pr.Number), in, nil)
}

// RequestReviewers adds people to a merge request's reviewers. GitLab has no team reviewers: a group is left out.
func (c Client) RequestReviewers(ctx context.Context, path string, pr registry.PR, who []string) error {
	m, err := c.MR(ctx, path, pr.Number)
	if err != nil {
		return err
	}
	var ids []int
	for _, u := range m.Reviewers {
		ids = append(ids, u.ID)
	}
	added := false
	for _, w := range who {
		w = strings.TrimPrefix(w, "@")
		if w == "" || strings.Contains(w, "/") || slices.ContainsFunc(m.Reviewers, func(u User) bool { return u.Username == w }) {
			continue
		}
		id, err := c.UserID(ctx, w)
		if err != nil {
			return err
		}
		ids, added = append(ids, id), true
	}
	if !added {
		return nil
	}
	return c.call(ctx, "PUT", fmt.Sprintf("/projects/%s/merge_requests/%d", project(path), pr.Number), map[string]any{"reviewer_ids": ids}, nil)
}

// Checks are the jobs of a merge request's head pipeline.
func (c Client) Checks(ctx context.Context, path string, pr registry.PR) ([]registry.Check, error) {
	m, err := c.MR(ctx, path, pr.Number)
	if err != nil || m.HeadPipeline == nil {
		return nil, err
	}
	return c.pipelineChecks(ctx, path, m.HeadPipeline.ID)
}

// pipelineChecks are a pipeline's jobs as checks, the first failing one's with the line of its log that says why.
func (c Client) pipelineChecks(ctx context.Context, path string, pipeline int) ([]registry.Check, error) {
	jobs, err := c.Jobs(ctx, path, pipeline)
	if err != nil {
		return nil, err
	}
	out := []registry.Check{}
	told := false
	for _, j := range jobs {
		ch := registry.Check{Name: j.Name, State: checkState(j), URL: j.WebURL}
		if j.StartedAt != nil {
			ch.Started = when(*j.StartedAt)
		}
		if j.FinishedAt != nil {
			ch.Completed = when(*j.FinishedAt)
		}
		if !told && ch.State == registry.CheckFailed {
			told = true
			if log, err := c.Trace(ctx, path, j.ID); err == nil {
				ch.Detail = FailLine(log)
			}
		}
		out = append(out, ch)
	}
	return out, nil
}

// checkState is a job's status as a check's.
func checkState(j Job) string {
	switch j.Status {
	case "success":
		return registry.CheckOK
	case "failed", "canceled":
		if j.AllowFailure {
			return registry.CheckSkipped
		}
		return registry.CheckFailed
	case "running":
		return registry.CheckRunning
	case "skipped", "manual":
		return registry.CheckSkipped
	}
	return registry.CheckQueued
}

var (
	failing = regexp.MustCompile(`(?i)\b(fail(ed|ure)?|error|panic)\b`)
	ansi    = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|section_(start|end):\d+:\S+\r?`)
)

// FailLine is the first line of a job's log that names a failure, "" for none; GitLab's own section markers and
// colour codes are left out.
func FailLine(log string) string {
	for _, l := range strings.Split(log, "\n") {
		l = strings.TrimSpace(ansi.ReplaceAllString(strings.TrimRight(l, "\r"), ""))
		if l != "" && !strings.HasPrefix(l, "$ ") && failing.MatchString(l) {
			return l
		}
	}
	return ""
}

// MergeMethods are what the project allows: its merge method (a merge commit, or fast-forward as a rebase) and squash
// unless it's never; squash is the default when the project squashes by default.
func (c Client) MergeMethods(ctx context.Context, path string) (registry.MergeMethods, error) {
	p, err := c.Project(ctx, path)
	if err != nil {
		return registry.MergeMethods{}, err
	}
	base := registry.MergeCommit
	if p.MergeMethod == "ff" {
		base = registry.MergeRebase
	}
	m := registry.MergeMethods{Allowed: []string{base}, Default: base}
	switch p.SquashOption {
	case "always":
		m = registry.MergeMethods{Allowed: []string{registry.MergeSquash}, Default: registry.MergeSquash}
	case "default_on":
		m = registry.MergeMethods{Allowed: []string{registry.MergeSquash, base}, Default: registry.MergeSquash}
	case "never":
	default:
		m.Allowed = append(m.Allowed, registry.MergeSquash)
	}
	return m, nil
}

// Blockers are what GitLab says of merging now: unresolved threads, changes asked for, and its merge status. A
// draft, a conflict and missing approvals it refuses outright.
func (c Client) Blockers(ctx context.Context, path string, pr registry.PR) ([]registry.MergeBlocker, error) {
	m, err := c.MR(ctx, path, pr.Number)
	if err != nil {
		return nil, err
	}
	var out []registry.MergeBlocker
	n, err := c.Unresolved(ctx, path, pr.Number)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockThreads, Count: n, Text: counted(n, "reviewer comment") + " unresolved", URL: m.WebURL})
	}
	rs, err := c.Reviewers(ctx, path, pr.Number)
	if err != nil {
		return nil, err
	}
	var changes []string
	for _, rv := range rs {
		if rv.State == "requested_changes" {
			changes = append(changes, rv.User.Username)
		}
	}
	if len(changes) > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockChanges, Count: len(changes), Text: strings.Join(changes, ", ") + " asked for changes", URL: m.WebURL})
	}
	if m.Draft {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockDraft, Text: "it's a draft: mark it ready first", Hard: true})
	}
	if m.HasConflicts {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockConflicts, Text: "it conflicts with " + m.TargetBranch, Hard: true})
	}
	switch m.MergeStatus {
	case "not_approved":
		out = append(out, registry.MergeBlocker{Kind: registry.BlockReviews, Text: "the approvals it needs aren't in", Hard: true, URL: m.WebURL})
	case "need_rebase":
		out = append(out, registry.MergeBlocker{Kind: registry.BlockBehind, Text: "it's behind " + m.TargetBranch})
	}
	return out, nil
}

// Merge merges at the head the user saw: GitLab refuses if the source branch has moved on.
func (c Client) Merge(ctx context.Context, path string, pr registry.PR, o registry.MergeOpts) error {
	in := map[string]any{"squash": o.Method == registry.MergeSquash, "should_remove_source_branch": o.DeleteBranch}
	if o.Head != "" {
		in["sha"] = o.Head
	}
	if msg := strings.TrimSpace(o.Title + "\n\n" + o.Body); msg != "" {
		key := "merge_commit_message"
		if o.Method == registry.MergeSquash {
			key = "squash_commit_message"
		}
		in[key] = msg
	}
	return c.call(ctx, "PUT", fmt.Sprintf("/projects/%s/merge_requests/%d/merge", project(path), pr.Number), in, nil)
}

func when(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func counted(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func queryEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// Threads are an MR's review threads, mirrored into the session: one per diff discussion, and one with no id for its
// general conversation. A comment's author is a member when they have Developer access to the project.
func (c Client) Threads(ctx context.Context, path string, pr registry.PR) ([]registry.ReviewThread, error) {
	m, err := c.MR(ctx, path, pr.Number)
	if err != nil {
		return nil, err
	}
	ds, err := c.Discussions(ctx, path, pr.Number)
	if err != nil {
		return nil, err
	}
	rs, err := c.Reviewers(ctx, path, pr.Number)
	if err != nil {
		return nil, err
	}
	approved := map[string]bool{}
	for _, rv := range rs {
		approved[rv.User.Username] = rv.State == "approved"
	}
	note := func(n Note) (registry.ReviewNote, error) {
		member, err := c.Member(ctx, path, n.Author.ID)
		return registry.ReviewNote{ID: fmt.Sprint(n.ID), Author: n.Author.Username, Body: n.Body, At: when(n.CreatedAt), Member: member}, err
	}
	general := registry.ReviewThread{URL: m.WebURL}
	out := []registry.ReviewThread{}
	for _, d := range ds {
		if len(d.Notes) == 0 || d.Notes[0].System {
			continue
		}
		first := d.Notes[0]
		if first.Position == nil {
			for _, n := range d.Notes {
				if n.System {
					continue
				}
				rn, err := note(n)
				if err != nil {
					return nil, err
				}
				general.Comments = append(general.Comments, rn)
			}
			continue
		}
		resolved := first.Resolved
		t := registry.ReviewThread{ID: d.ID, Path: first.Position.NewPath, Line: first.Position.NewLine, Resolved: &resolved,
			Reviewer: first.Author.Username, State: registry.ReviewCommented, URL: fmt.Sprintf("%s#note_%d", m.WebURL, first.ID)}
		if t.Line == 0 {
			t.Path, t.Line, t.Old = cmp.Or(first.Position.OldPath, t.Path), first.Position.OldLine, true
		}
		if approved[t.Reviewer] {
			t.State = registry.ReviewApproved
		}
		for _, n := range d.Notes {
			if n.System {
				continue
			}
			rn, err := note(n)
			if err != nil {
				return nil, err
			}
			t.Comments = append(t.Comments, rn)
		}
		out = append(out, t)
	}
	if len(general.Comments) > 0 {
		out = append(out, general)
	}
	return out, nil
}

// Reply posts a note as the user: in a discussion, or with thread "" in the MR's general conversation. It answers
// the note's id.
func (c Client) Reply(ctx context.Context, path string, pr registry.PR, thread, body string) (string, error) {
	where := fmt.Sprintf("/projects/%s/merge_requests/%d/notes", project(path), pr.Number)
	if thread != "" {
		where = fmt.Sprintf("/projects/%s/merge_requests/%d/discussions/%s/notes", project(path), pr.Number, url.PathEscape(thread))
	}
	var n Note
	if err := c.call(ctx, "POST", where, map[string]any{"body": body}, &n); err != nil {
		return "", err
	}
	return fmt.Sprint(n.ID), nil
}
