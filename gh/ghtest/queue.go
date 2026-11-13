package ghtest

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/gh"
)

// The review queue on the fake: teams, a PR's commits, when each reviewer was asked, and the search terms
// gh.SearchReview and gh.SearchReviewed use, over REST and GraphQL.

// AddTeam makes logins members of team ("acme/platform").
func (s *Server) AddTeam(team string, logins ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams[team] = append(s.teams[team], logins...)
}

// AddCommit pushes a commit to a PR: its head is now sha.
func (s *Server) AddCommit(repo string, number int, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := prKey(repo, number)
	s.commits[k] = append(s.commits[k], sha)
	z := s.sizes[k]
	z.HeadSHA = sha
	s.sizes[k] = z
}

func (s *Server) ask(repo string, number int, who string) {
	k := prKey(repo, number)
	if s.asked[k] == nil {
		s.asked[k] = map[string]time.Time{}
	}
	s.asked[k][who] = s.tick()
}

func (s *Server) head(repo string, number int) string {
	k := prKey(repo, number)
	if cs := s.commits[k]; len(cs) > 0 {
		return cs[len(cs)-1]
	}
	return s.sizes[k].HeadSHA
}

// reviewed records a review at the PR's head, and takes the reviewer's request (and their teams') off it, as GitHub does.
func (s *Server) reviewed(repo string, number int, r *gh.Review) {
	if r.Commit == "" {
		r.Commit = s.head(repo, number)
	}
	k := prKey(repo, number)
	s.reviews[k] = append(s.reviews[k], *r)
	if _, p := s.findNum(repo, number); p != nil {
		p.Reviewers = slices.DeleteFunc(p.Reviewers, func(who string) bool { return who == r.User || slices.Contains(s.teams[who], r.User) })
	}
}

type hit struct {
	repo string
	p    *gh.PR
}

// match is GitHub's issue search over the fake's pull requests, the newest first, for is:, author:, review-requested:
// (the login, or a team of theirs) and reviewed-by:, each negated by a leading -, with @me the fake's login. A PR
// with no author is the login's.
func (s *Server) match(q string) []hit {
	var hits []hit
	for repo, prs := range s.prs {
		for _, p := range prs {
			hits = append(hits, hit{repo, p})
		}
	}
	me := func(v string) string {
		if v == "@me" {
			return s.login
		}
		return v
	}
	for _, term := range strings.Fields(q) {
		not := strings.HasPrefix(term, "-")
		k, v, _ := strings.Cut(strings.TrimPrefix(term, "-"), ":")
		hits = slices.DeleteFunc(hits, func(h hit) bool {
			in := true
			switch k {
			case "is":
				in = v == "pr" || v == "open" && h.p.Open() || v == "closed" && !h.p.Open()
			case "author":
				in = cmpOr(h.p.Author, s.login) == me(v)
			case "review-requested":
				in = slices.ContainsFunc(h.p.Reviewers, func(who string) bool { return who == me(v) || slices.Contains(s.teams[who], me(v)) })
			case "reviewed-by":
				in = slices.ContainsFunc(s.reviews[prKey(h.repo, h.p.Number)], func(r gh.Review) bool { return r.User == me(v) })
			}
			return in == not
		})
	}
	slices.SortFunc(hits, func(a, b hit) int { return b.p.Created.Compare(a.p.Created) })
	return hits
}

// queue answers gh.API.ReviewQueue's GraphQL search.
func (s *Server) queue(w http.ResponseWriter, vars map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, _ := vars["q"].(string)
	reviewer := func(who string) map[string]any {
		if strings.Contains(who, "/") {
			return map[string]any{"__typename": "Team", "combinedSlug": who}
		}
		return map[string]any{"__typename": "User", "login": who}
	}
	nodes := []map[string]any{}
	for _, h := range s.match(q) {
		k := prKey(h.repo, h.p.Number)
		z := s.sizes[k]
		var requests, events, latest, commits []map[string]any
		for _, who := range h.p.Reviewers {
			requests = append(requests, map[string]any{"requestedReviewer": reviewer(who)})
		}
		for who, at := range s.asked[k] {
			events = append(events, map[string]any{"createdAt": at, "requestedReviewer": reviewer(who)})
		}
		last := map[string]gh.Review{}
		var order []string
		for _, r := range s.reviews[k] {
			if _, ok := last[r.User]; !ok {
				order = append(order, r.User)
			}
			last[r.User] = r
		}
		for _, u := range order {
			r := last[u]
			latest = append(latest, map[string]any{"author": map[string]string{"login": u}, "state": r.State, "commit": map[string]string{"oid": r.Commit}})
		}
		shas := s.commits[k]
		if len(shas) == 0 && z.HeadSHA != "" {
			shas = []string{z.HeadSHA}
		}
		for i, sha := range shas {
			c := map[string]any{"oid": sha}
			if i == len(shas)-1 {
				c["statusCheckRollup"] = rollup(s.checks[h.repo+"@"+sha])
			}
			commits = append(commits, map[string]any{"commit": c})
		}
		nodes = append(nodes, map[string]any{"number": h.p.Number, "title": h.p.Title, "url": h.p.URL, "isDraft": h.p.Draft,
			"state": strings.ToUpper(h.p.State), "createdAt": h.p.Created, "updatedAt": h.p.Created, "additions": z.Additions,
			"deletions": z.Deletions, "changedFiles": len(z.Files), "baseRefName": h.p.Base, "headRefName": h.p.Head, "author": map[string]string{"login": cmpOr(cmpOr(z.Author, h.p.Author), s.login)},
			"repository": map[string]string{"nameWithOwner": h.repo}, "reviewRequests": map[string]any{"nodes": requests},
			"timelineItems": map[string]any{"nodes": events}, "latestReviews": map[string]any{"nodes": latest},
			"commits": map[string]any{"nodes": commits}})
	}
	reply(w, 200, map[string]any{"data": map[string]any{"viewer": map[string]string{"login": s.login}, "search": map[string]any{"nodes": nodes}}})
}

// rollup is GitHub's statusCheckRollup of a commit's checks; nil with none.
func rollup(cs []gh.Check) any {
	if len(cs) == 0 {
		return nil
	}
	state := "SUCCESS"
	for _, c := range cs {
		switch c.State() {
		case gh.CheckFail:
			return map[string]string{"state": "FAILURE"}
		case gh.CheckPending:
			state = "PENDING"
		}
	}
	return map[string]string{"state": state}
}
