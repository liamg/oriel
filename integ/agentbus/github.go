package agentbus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/integ/agentbus/autoapprove"
)

// GitHub is what review requests need of GitHub: a PR's details for its card, and for the auto-approve rules its
// files, checks and the approval itself; and the reviews submitted there, to report one back to the asker. gh.API is
// one.
type GitHub interface {
	Me(ctx context.Context) (string, error)
	PRDetails(ctx context.Context, repo gh.Repo, number int) (gh.PRDetails, error)
	PRFiles(ctx context.Context, repo gh.Repo, number int) ([]gh.PRFile, error)
	Checks(ctx context.Context, repo gh.Repo, ref string) ([]gh.Check, error)
	Reviews(ctx context.Context, repo gh.Repo, number int) ([]gh.Review, error)
	ApproveAt(ctx context.Context, repo gh.Repo, number int, sha string) error
}

// maxPRFiles is GitHub's cap on a pull request's file list.
const maxPRFiles = 3000

// Reviewer is the GitHub half of review requests (crit's worker/ghreviewed.go and worker/autoapprove.go): it fills
// in a request's PR, approves one that passes a rule, and sends the result of a review this person submitted on
// GitHub.
type Reviewer struct {
	GitHub GitHub

	mu    sync.Mutex
	rules []autoapprove.Rule
	login string
}

// SetRules sets the auto-approve rules.
func (r *Reviewer) SetRules(rules []autoapprove.Rule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules = rules
}

func (r *Reviewer) me(ctx context.Context) (string, error) {
	r.mu.Lock()
	login := r.login
	r.mu.Unlock()
	if login != "" {
		return login, nil
	}
	login, err := r.GitHub.Me(ctx)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.login = login
	r.mu.Unlock()
	return login, nil
}

// PRInfo is a PR's details for its card.
func (r *Reviewer) PRInfo(ctx context.Context, prURL string) (PRInfo, error) {
	repo, n, err := gh.ParsePRURL(prURL)
	if err != nil {
		return PRInfo{}, err
	}
	d, err := r.GitHub.PRDetails(ctx, repo, n)
	if err != nil {
		return PRInfo{}, err
	}
	return PRInfo{Title: d.Title, Author: d.Author, Base: d.Base, Additions: d.Additions, Deletions: d.Deletions, Files: d.ChangedFiles}, nil
}

// facts reads a PR as the rules see it, and the head it was read at. It fails when the head moves while it reads, so
// the facts are one commit's.
func (r *Reviewer) facts(ctx context.Context, prURL string) (autoapprove.PR, gh.PRDetails, error) {
	repo, n, err := gh.ParsePRURL(prURL)
	if err != nil {
		return autoapprove.PR{}, gh.PRDetails{}, err
	}
	before, err := r.GitHub.PRDetails(ctx, repo, n)
	if err != nil {
		return autoapprove.PR{}, before, err
	}
	if before.ChangedFiles >= maxPRFiles {
		return autoapprove.PR{}, before, fmt.Errorf("%d files is over GitHub's file list cap", before.ChangedFiles)
	}
	files, err := r.GitHub.PRFiles(ctx, repo, n)
	if err != nil {
		return autoapprove.PR{}, before, err
	}
	checks, err := r.GitHub.Checks(ctx, repo, before.HeadSHA)
	if err != nil {
		return autoapprove.PR{}, before, err
	}
	after, err := r.GitHub.PRDetails(ctx, repo, n)
	if err != nil {
		return autoapprove.PR{}, before, err
	}
	if after.HeadSHA != before.HeadSHA {
		return autoapprove.PR{}, before, errors.New("the PR changed while it was read")
	}
	pr := autoapprove.PR{Repo: repo.String(), Author: before.Author, Additions: before.Additions, Deletions: before.Deletions,
		ChecksGreen: gh.Green(checks)}
	for _, f := range files {
		pr.Files = append(pr.Files, autoapprove.File{Path: f.Path, Previous: f.Previous})
	}
	return pr, before, nil
}

// matchRule reads the pull request and returns the first rule it passes, with the head commit it was read at.
func (r *Reviewer) matchRule(ctx context.Context, prURL, requester string) (rule, sha string, ok bool) {
	r.mu.Lock()
	rules := r.rules
	r.mu.Unlock()
	if !autoapprove.AnyEnabled(rules) {
		return "", "", false
	}
	pr, d, err := r.facts(ctx, prURL)
	if err != nil {
		log.Printf("auto-approve %s: %v", prURL, err)
		return "", "", false
	}
	if d.State != "open" {
		return "", "", false
	}
	if me, err := r.me(ctx); err != nil || strings.EqualFold(d.Author, me) {
		log.Printf("auto-approve %s: your own PR, or GitHub didn't say who you are", prURL)
		return "", "", false
	}
	pr.Requester = requester
	rule, ok = autoapprove.Match(rules, pr)
	return rule, d.HeadSHA, ok
}

// Vet approves a review request that passes an auto-approve rule: claimed on the bus, approved on GitHub at the head
// that was read, the result sent. Any doubt or error leaves it for the human.
func (r *Reviewer) Vet(ctx context.Context, b *Bus, it Item) bool {
	if it.Review == nil {
		return false
	}
	url := it.Review.ReviewRequestBody.PR
	rule, sha, ok := r.matchRule(ctx, url, it.From.User)
	if !ok || ctx.Err() != nil {
		return false
	}
	repo, n, err := gh.ParsePRURL(url)
	if err != nil {
		return false
	}
	if err := b.Claim(ctx, it.ID); err != nil {
		log.Printf("agentbus: auto-approve %s: claim: %v", url, err)
		return false
	}
	if err := r.GitHub.ApproveAt(ctx, repo, n, sha); err != nil {
		log.Printf("agentbus: auto-approve %s: %v", url, err)
		return false
	}
	rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.Result(rctx, it.ID, ReviewResultBody{State: ReviewApproved, URL: url}); err != nil {
		log.Printf("agentbus: auto-approve %s: send result: %v", url, err)
	}
	b.NoteAutoApproved(it, rule)
	log.Printf("agentbus: auto-approved %s (rule: %s)", url, rule)
	return true
}

// latestReviewBy is login's newest review submitted after since, if there is one.
func latestReviewBy(rs []gh.Review, login string, since time.Time) (gh.Review, bool) {
	var out gh.Review
	for _, r := range rs {
		if !strings.EqualFold(r.User, login) || r.State == gh.ReviewPending || !r.Submitted.After(since) {
			continue
		}
		if r.Submitted.After(out.Submitted) {
			out = r
		}
	}
	return out, out.State != ""
}

// GitHubEvery is how often open review requests are checked for a review this person submitted on GitHub.
const GitHubEvery = 2 * time.Minute

// CheckGitHub sends the result of every review request this person answered on GitHub itself: claimed first if it
// wasn't, then the review's state, its text and its link.
func (r *Reviewer) CheckGitHub(ctx context.Context, b *Bus) {
	var login string
	for _, it := range b.Items() {
		if it.Kind != "review" || it.Review == nil || it.Checking || ctx.Err() != nil {
			continue
		}
		if login == "" {
			var err error
			if login, err = r.me(ctx); err != nil {
				log.Printf("agentbus: who you are on GitHub: %v", err)
				return
			}
		}
		url := it.Review.ReviewRequestBody.PR
		repo, n, err := gh.ParsePRURL(url)
		if err != nil {
			continue
		}
		rs, err := r.GitHub.Reviews(ctx, repo, n)
		if err != nil {
			log.Printf("agentbus: GitHub reviews of %s: %v", url, err)
			continue
		}
		if rv, ok := latestReviewBy(rs, login, it.Created); ok {
			r.sendReview(ctx, b, it, rv)
		}
	}
}

func (r *Reviewer) sendReview(ctx context.Context, b *Bus, it Item, rv gh.Review) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	url := it.Review.ReviewRequestBody.PR
	if !it.Claimed {
		if err := b.Claim(ctx, it.ID); err != nil {
			log.Printf("agentbus: claim %s reviewed on GitHub: %v", url, err)
			return
		}
	}
	state := map[string]string{gh.ReviewApproved: ReviewApproved, gh.ReviewChangesRequested: ReviewChangesRequested}[rv.State]
	if state == "" {
		state = ReviewCommented
	}
	summary := strings.TrimSpace(rv.Body)
	if summary == "" {
		summary = "Reviewed on GitHub."
	}
	link := rv.URL
	if link == "" {
		link = url
	}
	if err := b.Result(ctx, it.ID, ReviewResultBody{State: state, Summary: summary, URL: link}); err != nil {
		log.Printf("agentbus: send GitHub review of %s: %v", url, err)
		return
	}
	log.Printf("agentbus: sent GitHub review of %s (%s)", url, state)
}
