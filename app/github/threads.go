package github

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	gh "github.com/liamg/oriel/integ/github"
	"github.com/liamg/oriel/registry"
)

// A PR's review threads and conversation as a code host gives them (registry.CodeHost's Threads, Reply, Resolve): a
// review thread is keyed by its first comment's id, the conversation by "".

// trust is who the agent may read on GitHub: the trusted users, and the trusted orgs' members (the repo's owner's
// when neither is set), each org's asked once and kept.
type trust struct {
	mu      sync.Mutex
	members map[string]bool // org/login → a member
	me      map[string]string
}

// threads are the PR's review threads and its conversation.
func (x *Host) threads(ctx context.Context, r registry.CodeRepo, pr registry.PR) ([]registry.ReviewThread, error) {
	c, repo, err := x.repo(ctx, r)
	if err != nil {
		return nil, err
	}
	n, err := number(pr)
	if err != nil {
		return nil, err
	}
	comments, err := c.ReviewComments(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	reviews, err := c.Reviews(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	issue, err := c.IssueComments(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	resolved, err := c.ResolvedThreads(ctx, repo, n)
	if err != nil {
		resolved = nil // unknown: each thread's resolution is left as it is
	}
	states := gh.ReviewStates(reviews)
	state := func(user string) registry.ReviewState {
		if s := states[user]; s != "" {
			return reviewWord(s)
		}
		return ""
	}
	trusted, me := x.truster(ctx, c, repo), x.me(ctx, c, r.Site)
	note := func(id int64, user, assoc, body string, at time.Time) registry.ReviewNote {
		rn := registry.ReviewNote{ID: strconv.FormatInt(id, 10), Author: user, Body: body, At: at, Member: trusted(user, assoc), Mine: me != "" && user == me}
		if w := gh.PostedAs(body); w != strings.TrimSpace(body) {
			rn.Written = w
		}
		return rn
	}

	byRoot := map[int64][]gh.ReviewComment{}
	var roots []int64
	for _, rc := range comments {
		root := cmp.Or(rc.InReplyTo, rc.ID)
		if byRoot[root] == nil {
			roots = append(roots, root)
		}
		byRoot[root] = append(byRoot[root], rc)
	}
	var out []registry.ReviewThread
	for _, root := range roots {
		cs := byRoot[root]
		first := cs[0]
		if first.ID != root {
			continue // its first comment was deleted
		}
		t := registry.ReviewThread{ID: strconv.FormatInt(root, 10), Path: first.Path, Line: cmp.Or(first.Line, first.OriginalLine), Old: first.Side == "LEFT",
			Outdated: first.Line == 0, Reviewer: first.User, State: state(first.User), URL: first.URL}
		if resolved != nil {
			v := resolved[root]
			t.Resolved = &v
		}
		for _, rc := range cs {
			t.Comments = append(t.Comments, note(rc.ID, rc.User, rc.Association, rc.Body, rc.Created))
		}
		out = append(out, t)
	}
	convo := registry.ReviewThread{URL: pr.URL, Comments: []registry.ReviewNote{}}
	for _, ic := range issue {
		convo.Comments = append(convo.Comments, note(ic.ID, ic.User, ic.Association, ic.Body, ic.Created))
	}
	for _, rv := range reviews {
		if strings.TrimSpace(rv.Body) != "" {
			convo.Comments = append(convo.Comments, note(rv.ID, rv.User, rv.Association, rv.Body, rv.Submitted))
		}
	}
	slices.SortStableFunc(convo.Comments, func(a, b registry.ReviewNote) int { return a.At.Compare(b.At) })
	if len(convo.Comments) > 0 {
		convo.Reviewer = convo.Comments[0].Author
		convo.State = state(convo.Reviewer)
	}
	for _, user := range slices.Sorted(maps.Keys(states)) {
		convo.Reviewers = append(convo.Reviewers, registry.PRReviewer{Login: user, State: reviewWord(states[user])})
	}
	return append(out, convo), nil
}

// reply posts body on a review thread, or in the conversation for t.ID "", as the user, and answers its id.
func (x *Host) reply(ctx context.Context, r registry.CodeRepo, pr registry.PR, t registry.ReviewThread, body string) (string, error) {
	c, repo, err := x.repo(ctx, r)
	if err != nil {
		return "", err
	}
	n, err := number(pr)
	if err != nil {
		return "", err
	}
	if t.ID == "" {
		ic, err := c.Comment(ctx, repo, n, body)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(ic.ID, 10), nil
	}
	root, err := rootOf(t)
	if err != nil {
		return "", err
	}
	rc, err := c.ReplyReview(ctx, repo, n, root, body)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(rc.ID, 10), nil
}

// resolve resolves a review thread on GitHub, or reopens it.
func (x *Host) resolve(ctx context.Context, r registry.CodeRepo, pr registry.PR, t registry.ReviewThread, resolved bool) error {
	c, repo, err := x.repo(ctx, r)
	if err != nil {
		return err
	}
	n, err := number(pr)
	if err != nil {
		return err
	}
	root, err := rootOf(t)
	if err != nil {
		return err
	}
	return c.ResolveThread(ctx, repo, n, root, resolved)
}

// rootOf is a review thread's first comment's id.
func rootOf(t registry.ReviewThread) (int64, error) {
	root, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil || root == 0 {
		return 0, fmt.Errorf("%q isn't a GitHub review thread", t.ID)
	}
	return root, nil
}

// me is who the user is on the site, asked once.
func (x *Host) me(ctx context.Context, c gh.Client, s registry.Site) string {
	x.trust.mu.Lock()
	defer x.trust.mu.Unlock()
	if x.trust.me == nil {
		x.trust.me = map[string]string{}
	}
	if m, ok := x.trust.me[s.Host]; ok {
		return m
	}
	m, err := c.Me(ctx)
	if err == nil {
		x.trust.me[s.Host] = m
	}
	return m
}

// truster says whose text the agent may read: a trusted user, or a member of a trusted org (the repo's owner when
// none is set).
func (x *Host) truster(ctx context.Context, c gh.Client, repo gh.Repo) func(user, assoc string) bool {
	list := func(name string) []string {
		l, _ := registry.Option[[]string](x.h.Options(), name, registry.At{})
		return l
	}
	orgs, users := list(trustedOrgsOption), list(trustedUsersOption)
	if len(orgs) == 0 && len(users) == 0 {
		orgs = []string{repo.Owner}
	}
	return func(user, assoc string) bool {
		if slices.ContainsFunc(users, func(u string) bool { return strings.EqualFold(strings.TrimPrefix(u, "@"), user) }) {
			return true
		}
		for _, org := range orgs {
			if strings.EqualFold(org, repo.Owner) && (assoc == "OWNER" || assoc == "MEMBER") {
				return true
			}
			if x.member(ctx, c, org, user) {
				return true
			}
		}
		return false
	}
}

// member says user belongs to org, asked once; not known is no.
func (x *Host) member(ctx context.Context, c gh.Client, org, user string) bool {
	key := strings.ToLower(org + "/" + user)
	x.trust.mu.Lock()
	in, ok := x.trust.members[key]
	x.trust.mu.Unlock()
	if ok {
		return in
	}
	in, err := c.OrgMember(ctx, org, user)
	if err != nil {
		return false // asked again next time
	}
	x.trust.mu.Lock()
	if x.trust.members == nil {
		x.trust.members = map[string]bool{}
	}
	x.trust.members[key] = in
	x.trust.mu.Unlock()
	return in
}
