package ghtest

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
)

// A PR as a review request reads it on the fake: its author, size, head and files; who the client is; and the
// reviews the client submits.

// PRSize is what the fake says of a PR beyond gh.PR.
type PRSize struct {
	Author    string
	HeadSHA   string
	Additions int
	Deletions int
	Files     []gh.PRFile
}

// SetPRSize sets a PR's author, size, head and files.
func (s *Server) SetPRSize(repo string, number int, z PRSize) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes[prKey(repo, number)] = z
}

// SetNow sets the fake's clock: what it stamps reviews and PRs with next (a minute on, each).
func (s *Server) SetNow(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = t.Add(-time.Minute)
}

// Reviews are a PR's submitted reviews, oldest first.
func (s *Server) Reviews(repo string, number int) []gh.Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]gh.Review(nil), s.reviews[prKey(repo, number)]...)
}

func (s *Server) user(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply(w, 200, map[string]string{"login": s.login})
}

func (s *Server) prDetails(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, p := s.find(r)
	if p == nil {
		notFound(w)
		return
	}
	out := s.restPR(repo, p)
	z := s.sizes[prKey(repo, p.Number)]
	out["user"] = user(z.Author)
	out["head"] = map[string]string{"ref": p.Head, "sha": z.HeadSHA}
	out["additions"], out["deletions"], out["changed_files"] = z.Additions, z.Deletions, len(z.Files)
	reply(w, 200, out)
}

func (s *Server) prFiles(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files := s.sizes[prKey(repoOf(r), num(r))].Files
	if files == nil {
		files = []gh.PRFile{}
	}
	reply(w, 200, files)
}

// prCommits are the commits AddCommit pushed, each by the PR's author at the fake's now.
func (s *Server) prCommits(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := prKey(repoOf(r), num(r))
	out := []map[string]any{}
	for _, sha := range s.commits[k] {
		out = append(out, map[string]any{"sha": sha, "author": user(s.sizes[k].Author),
			"commit": map[string]any{"message": "commit " + sha[:min(7, len(sha))], "author": map[string]any{"name": s.sizes[k].Author, "date": s.now}}})
	}
	reply(w, 200, out)
}

// onDiff says a review comment may sit on line of path: one DiffLines names, or one in the hunks of the file's patch.
func (s *Server) onDiff(repo string, number int, path, side string, line int) bool {
	if slices.Contains(s.diffLines[prKey(repo, number)], fmt.Sprintf("%s:%d", path, line)) {
		return true
	}
	for _, f := range s.sizes[prKey(repo, number)].Files {
		if f.Path == path && f.Patch != "" {
			return gitx.PatchLines(f.Patch)[side][line]
		}
	}
	return false
}

// fileComment takes a review comment on a file as a whole (subject_type file), as GitHub's pulls/{n}/comments does.
func (s *Server) fileComment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body        string `json:"body"`
		CommitID    string `json:"commit_id"`
		Path        string `json:"path"`
		SubjectType string `json:"subject_type"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.SubjectType != "file" || in.CommitID == "" || in.Path == "" {
		reply(w, 422, map[string]string{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.addReview(repoOf(r), num(r), gh.ReviewComment{User: s.login, Association: "MEMBER", Path: in.Path, Body: in.Body})
	reply(w, 201, map[string]any{"id": c.ID})
}

// DiffLines are the lines of a PR's diff a review may comment on, "path:line"; comments elsewhere are refused.
func (s *Server) DiffLines(repo string, number int, lines ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diffLines[prKey(repo, number)] = lines
}

// submitReview takes the client's review: APPROVE at a commit, REQUEST_CHANGES or COMMENT.
func (s *Server) submitReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Event    string `json:"event"`
		Body     string `json:"body"`
		CommitID string `json:"commit_id"`
		Comments []struct {
			Path      string `json:"path"`
			Line      int    `json:"line"`
			Side      string `json:"side"`
			StartLine int    `json:"start_line"`
			Body      string `json:"body"`
		} `json:"comments"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		reply(w, 422, map[string]string{"message": "Validation Failed"})
		return
	}
	state := map[string]string{"APPROVE": gh.ReviewApproved, "REQUEST_CHANGES": gh.ReviewChangesRequested, "COMMENT": gh.ReviewCommented}[in.Event]
	if state == "" {
		reply(w, 422, map[string]string{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := prKey(repoOf(r), num(r))
	if z := s.sizes[key]; in.CommitID != "" && z.HeadSHA != "" && in.CommitID != z.HeadSHA {
		reply(w, 422, map[string]string{"message": "commit_id is not the pull request's head"})
		return
	}
	for _, c := range in.Comments {
		if !s.onDiff(repoOf(r), num(r), c.Path, cmp.Or(c.Side, "RIGHT"), c.Line) {
			reply(w, 422, map[string]any{"message": "Unprocessable Entity", "errors": []string{"Line could not be resolved"}})
			return
		}
	}
	rv := gh.Review{ID: s.id(), User: s.login, Association: "MEMBER", State: state, Body: in.Body, Submitted: s.tick(), Commit: in.CommitID}
	for _, c := range in.Comments {
		s.addReview(repoOf(r), num(r), gh.ReviewComment{User: s.login, Association: "MEMBER", Review: rv.ID, Path: c.Path, Line: c.Line,
			OriginalLine: c.Line, Side: c.Side, Body: c.Body})
	}
	rv.URL = fmt.Sprintf("https://github.com/%s/pull/%d#pullrequestreview-%d", repoOf(r), num(r), rv.ID)
	s.reviewed(repoOf(r), num(r), &rv)
	reply(w, 200, map[string]any{"id": rv.ID, "state": state, "html_url": rv.URL})
}
