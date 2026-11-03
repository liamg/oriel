package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/liamg/oriel/sandbox/mcp"
)

// The oriel__gh_* tools: GitHub calls the GitHub MCP server lacks, run with the
// host's gh. Each returns a small JSON, trimmed with --jq.

var repoProp = map[string]any{"type": "string", "description": "owner/name; defaults to the project's origin remote"}

var repoName = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// ghCaller runs gh api calls in one project against one repository.
type ghCaller struct {
	h       Host
	project string
	repo    string
}

func newGHCaller(h Host, project, repo string) (*ghCaller, error) {
	if repo == "" {
		repo = "{owner}/{repo}"
	} else if !repoName.MatchString(repo) {
		return nil, fmt.Errorf("repo must be owner/name, got %q", repo)
	}
	return &ghCaller{h: h, project: project, repo: repo}, nil
}

func (c *ghCaller) api(ctx context.Context, path, jq string, args ...string) (string, error) {
	full := append([]string{"api", "repos/" + c.repo + "/" + path, "--jq", jq}, args...)
	out, err := c.h.GH(ctx, c.project, full...)
	return strings.TrimSpace(out), err
}

func notFound(err error) bool { return err != nil && strings.Contains(err.Error(), "HTTP 404") }

func seg(s string) string { return neturl.PathEscape(s) }

func ghBuiltins(h Host, add func(name, desc string, sch json.RawMessage, readOnly bool, run func(context.Context, json.RawMessage) (mcp.CallResult, error))) {
	type common struct {
		Repo    string `json:"repo"`
		Project string `json:"project"`
	}
	props := func(p map[string]any) map[string]any {
		p["repo"], p["project"] = repoProp, projectProp
		return p
	}
	result := func(out string, err error) (mcp.CallResult, error) {
		if err != nil {
			return mcp.TextResult(err.Error(), true), nil
		}
		return mcp.TextResult(out, false), nil
	}

	add("gh_repo_variable", "Read a GitHub Actions repository variable. Returns {found, name, value}.",
		schema(props(map[string]any{"name": strProp}), "name"), true,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				common
				Name string `json:"name"`
			}
			_ = json.Unmarshal(args, &in)
			c, err := newGHCaller(h, in.Project, in.Repo)
			if err != nil {
				return result("", err)
			}
			out, err := c.api(ctx, "actions/variables/"+seg(in.Name), `{found: true, name, value}`)
			if notFound(err) {
				return result(`{"found":false}`, nil)
			}
			return result(out, err)
		})

	add("gh_workflow_runs", "List a workflow's recent runs, newest first. workflow is a file name (release.yml) or an id. status filters, e.g. completed, in_progress, success.",
		schema(props(map[string]any{
			"workflow": strProp, "status": strProp, "branch": strProp,
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		}), "workflow"), true,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				common
				Workflow string `json:"workflow"`
				Status   string `json:"status"`
				Branch   string `json:"branch"`
				Limit    int    `json:"limit"`
			}
			_ = json.Unmarshal(args, &in)
			c, err := newGHCaller(h, in.Project, in.Repo)
			if err != nil {
				return result("", err)
			}
			if in.Limit <= 0 || in.Limit > 100 {
				in.Limit = 10
			}
			q := []string{"--method", "GET", "-F", "per_page=" + strconv.Itoa(in.Limit)}
			if in.Status != "" {
				q = append(q, "-f", "status="+in.Status)
			}
			if in.Branch != "" {
				q = append(q, "-f", "branch="+in.Branch)
			}
			return result(c.api(ctx, "actions/workflows/"+seg(in.Workflow)+"/runs",
				`[.workflow_runs[] | {id, headBranch: .head_branch, headSha: .head_sha, status, conclusion, url: .html_url, createdAt: .created_at}]`, q...))
		})

	add("gh_compare", "Compare two refs: the commits in head that base lacks. Returns totalCommits and each commit's sha and subject; truncated is set when GitHub cut the list short.",
		schema(props(map[string]any{"base": strProp, "head": strProp}), "base", "head"), true,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				common
				Base string `json:"base"`
				Head string `json:"head"`
			}
			_ = json.Unmarshal(args, &in)
			c, err := newGHCaller(h, in.Project, in.Repo)
			if err != nil {
				return result("", err)
			}
			return result(c.api(ctx, "compare/"+seg(in.Base)+"..."+seg(in.Head),
				`{totalCommits: .total_commits, commits: [.commits[] | {sha, subject: (.commit.message | split("\n")[0])}]}
				 + (if .total_commits > (.commits | length) then {truncated: true} else {} end)`))
		})

	add("gh_matching_refs", "List the tags whose names start with prefix, e.g. v1.2. Returns each tag's ref and sha.",
		schema(props(map[string]any{"prefix": strProp}), "prefix"), true,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				common
				Prefix string `json:"prefix"`
			}
			_ = json.Unmarshal(args, &in)
			c, err := newGHCaller(h, in.Project, in.Repo)
			if err != nil {
				return result("", err)
			}
			return result(c.api(ctx, "git/matching-refs/tags/"+seg(in.Prefix), `[.[] | {ref, sha: .object.sha}]`))
		})

	add("gh_create_tag", "Create a tag on GitHub at a commit. With message it is an annotated tag; without, a lightweight one. Refuses a tag that exists.",
		schema(props(map[string]any{"tag": strProp, "sha": strProp, "message": strProp}), "tag", "sha"), false,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				common
				Tag     string `json:"tag"`
				SHA     string `json:"sha"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Tag == "" || in.SHA == "" {
				return result("", fmt.Errorf("tag and sha are required"))
			}
			c, err := newGHCaller(h, in.Project, in.Repo)
			if err != nil {
				return result("", err)
			}
			if _, err := c.api(ctx, "git/ref/tags/"+seg(in.Tag), `.ref`); err == nil {
				return result("", fmt.Errorf("tag %s already exists", in.Tag))
			} else if !notFound(err) {
				return result("", err)
			}
			target := in.SHA
			if in.Message != "" {
				target, err = c.api(ctx, "git/tags", `.sha`, "--method", "POST",
					"-f", "tag="+in.Tag, "-f", "message="+in.Message, "-f", "object="+in.SHA, "-f", "type=commit")
				if err != nil {
					return result("", err)
				}
			}
			return result(c.api(ctx, "git/refs", `{ref, sha: .object.sha}`, "--method", "POST",
				"-f", "ref=refs/tags/"+in.Tag, "-f", "sha="+target))
		})
}
