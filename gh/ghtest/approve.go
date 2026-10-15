package ghtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/liamg/oriel/gh"
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
	out := restPR(repo, p)
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

// submitReview takes the client's review: APPROVE at a commit, REQUEST_CHANGES or COMMENT.
func (s *Server) submitReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Event    string `json:"event"`
		Body     string `json:"body"`
		CommitID string `json:"commit_id"`
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
	rv := gh.Review{ID: s.id(), User: s.login, Association: "MEMBER", State: state, Body: in.Body, Submitted: s.tick()}
	rv.URL = fmt.Sprintf("https://github.com/%s/pull/%d#pullrequestreview-%d", repoOf(r), num(r), rv.ID)
	s.reviews[key] = append(s.reviews[key], rv)
	reply(w, 200, map[string]any{"id": rv.ID, "state": state, "html_url": rv.URL})
}
