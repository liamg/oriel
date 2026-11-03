package gh

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/liamg/oriel/gitx"
)

// What reviewing someone else's PR on their asking needs (crit's gh, for agentbus): who the user is, a PR's size and
// head, its files, whether its checks passed, and approving it at a commit.

// PRDetails is a pull request as a review request's card and the auto-approve rules read it.
type PRDetails struct {
	Title        string `json:"title"`
	State        string `json:"state"` // open or closed
	Author       string `json:"author"`
	Base         string `json:"base"`
	HeadSHA      string `json:"headSha"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changedFiles"`
	Body         string `json:"body,omitempty"`
	Head         string `json:"head,omitempty"` // its branch
	URL          string `json:"url,omitempty"`
	Draft        bool   `json:"draft,omitempty"`
	Merged       bool   `json:"merged,omitempty"`
	// Mergeable is nil until GitHub has worked it out; MergeState is its mergeable_state: clean, behind, dirty…
	Mergeable  *bool    `json:"mergeable,omitempty"`
	MergeState string   `json:"mergeState,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Reviewers  []string `json:"reviewers,omitempty"` // asked for and not yet answered: logins, and org/team for a team
}

// PRFile is one changed file; Previous is the old path of a rename.
type PRFile struct {
	Path      string `json:"filename"`
	Previous  string `json:"previous_filename,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
	Patch     string `json:"patch,omitempty"` // its unified diff; none for a binary or very large file
}

// ParsePRURL reads https://github.com/owner/name/pull/123.
func ParsePRURL(raw string) (Repo, int, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err == nil && u.Host == "github.com" {
		p := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(p) >= 4 && p[2] == "pull" {
			if n, err := strconv.Atoi(p[3]); err == nil && n > 0 {
				return Repo{Owner: p[0], Name: p[1]}, n, nil
			}
		}
	}
	return Repo{}, 0, fmt.Errorf("not a GitHub pull request: %q", raw)
}

// Me is the login the API is signed in as.
func (a API) Me(ctx context.Context) (string, error) {
	var u restUser
	if err := a.T.Do(ctx, "GET", "user", nil, &u); err != nil {
		return "", err
	}
	if u.Login == "" {
		return "", errors.New("GitHub didn't say who you are")
	}
	return u.Login, nil
}

func (a API) PRDetails(ctx context.Context, repo Repo, number int) (PRDetails, error) {
	var p struct {
		restPR
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changed_files"`
		Mergeable    *bool  `json:"mergeable"`
		MergeState   string `json:"mergeable_state"`
		Labels       []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d", number)), nil, &p); err != nil {
		return PRDetails{}, err
	}
	pr := p.pr(repo.Owner)
	out := PRDetails{Title: p.Title, State: p.State, Author: p.User.Login, Base: p.Base.Ref, HeadSHA: p.Head.SHA, Additions: p.Additions,
		Deletions: p.Deletions, ChangedFiles: p.ChangedFiles, Body: p.Body, Head: p.Head.Ref, URL: p.HTMLURL, Draft: p.Draft,
		Mergeable: p.Mergeable, MergeState: p.MergeState, Reviewers: pr.Reviewers, Merged: pr.Merged}
	for _, l := range p.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	return out, nil
}

// PRFiles are the files a pull request changes: GitHub's first 100.
func (a API) PRFiles(ctx context.Context, repo Repo, number int) ([]PRFile, error) {
	var out []PRFile
	err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d/files?per_page=100", number)), nil, &out)
	return out, err
}

// ApproveAt approves a pull request at commit sha, so a later push isn't approved with it. The review has no body.
func (a API) ApproveAt(ctx context.Context, repo Repo, number int, sha string) error {
	return a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("pulls/%d/reviews", number)), map[string]string{"event": "APPROVE", "commit_id": sha}, nil)
}

// ReviewInput is a review to submit: Event is APPROVE, REQUEST_CHANGES or COMMENT; Commit pins it to the head it read.
type ReviewInput struct {
	Event    string
	Body     string
	Commit   string
	Comments []ReviewComment // Path, Line, Side and Body; OriginalLine, when set, is where a range starts
}

// SubmitReview submits a review with its comments on the lines they're about. GitHub refuses a whole review for one
// comment off the diff's hunks, so each comment is checked against the PR's patches first: one in a hunk goes on its
// line (a range starting outside the hunk keeps its last line); one elsewhere in a file the PR changes goes on the
// file, headed with its line; one on a file the PR doesn't change goes in the body. Only when GitHub still refuses
// does every comment go in the body.
func (a API) SubmitReview(ctx context.Context, repo Repo, number int, in ReviewInput) error {
	inline, onFile, inBody := in.Comments, []ReviewComment(nil), []ReviewComment(nil)
	if files, err := a.PRFiles(ctx, repo, number); err == nil && len(files) > 0 {
		inline, onFile, inBody = placeComments(files, in.Comments)
	}
	post := func(body string, comments []ReviewComment) error {
		req := map[string]any{"event": in.Event, "body": body}
		if in.Commit != "" {
			req["commit_id"] = in.Commit
		}
		var cs []map[string]any
		for _, c := range comments {
			m := map[string]any{"path": c.Path, "line": c.Line, "side": cmp.Or(c.Side, "RIGHT"), "body": c.Body}
			if c.OriginalLine > 0 && c.OriginalLine < c.Line {
				m["start_line"], m["start_side"] = c.OriginalLine, m["side"]
			}
			cs = append(cs, m)
		}
		if len(cs) > 0 {
			req["comments"] = cs
		}
		return a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("pulls/%d/reviews", number)), req, nil)
	}
	err := post(withComments(in.Body, inBody), inline)
	var e *Error
	if err != nil && len(inline) > 0 && errors.As(err, &e) && e.Status == 422 {
		return post(withComments(in.Body, in.Comments), nil)
	}
	if err != nil || len(onFile) == 0 {
		return err
	}
	commit := in.Commit
	if commit == "" {
		d, err := a.PRDetails(ctx, repo, number)
		if err != nil {
			return err
		}
		commit = d.HeadSHA
	}
	var errs []error
	for _, c := range onFile {
		req := map[string]any{"body": fmt.Sprintf(fileLineHead+"%s", c.Line, c.Body), "commit_id": commit, "path": c.Path, "subject_type": "file"}
		errs = append(errs, a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("pulls/%d/comments", number)), req, nil))
	}
	return errors.Join(errs...)
}

// fileLineHead heads a comment SubmitReview posts on its file, not its line: the line it was about.
const fileLineHead = "**Line %d**\n\n"

// PostedAs is the comment as it was written, from its body as SubmitReview posted it: without the line a comment on
// its file is headed with.
func PostedAs(body string) string {
	body = strings.TrimSpace(body)
	var line int
	if _, err := fmt.Sscanf(body, fileLineHead, &line); err == nil {
		if _, rest, ok := strings.Cut(body, "\n\n"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return body
}

// withComments is body with comments after it, each under its file and line.
func withComments(body string, comments []ReviewComment) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(body))
	for _, c := range comments {
		fmt.Fprintf(&b, "\n\n**`%s:%d`**\n\n%s", c.Path, c.Line, c.Body)
	}
	return strings.TrimSpace(b.String())
}

// placeComments sorts comments by where GitHub takes them: on their lines, on their files, or in the review's body. A
// file whose patch GitHub doesn't send (binary, too large, past the first 100) is tried on its lines.
func placeComments(files []PRFile, comments []ReviewComment) (inline, onFile, inBody []ReviewComment) {
	hunks := map[string]map[string]map[int]bool{}
	for _, f := range files {
		hunks[f.Path] = nil
		if f.Patch != "" {
			hunks[f.Path] = gitx.PatchLines(f.Patch)
		}
	}
	for _, c := range comments {
		lines, changed := hunks[c.Path]
		side := cmp.Or(c.Side, "RIGHT")
		switch {
		case !changed && len(files) < 100:
			inBody = append(inBody, c)
		case lines == nil || lines[side][c.Line]:
			if lines != nil && c.OriginalLine > 0 && !lines[side][c.OriginalLine] {
				c.OriginalLine = 0
			}
			inline = append(inline, c)
		default:
			onFile = append(onFile, c)
		}
	}
	return inline, onFile, inBody
}

// Green says whether every check passed (or was skipped); none at all isn't green.
func Green(cs []Check) bool {
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if s := c.State(); s != CheckPass && s != CheckSkipped {
			return false
		}
	}
	return true
}
