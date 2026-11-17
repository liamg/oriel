package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
)

// The gateway's gh_* tools: GitHub calls the GitHub MCP server lacks, run with the host's gh in the project the call
// names. Each returns a small JSON, trimmed with --jq.

var repoName = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// ghCaller runs gh api calls in one project against one repository.
type ghCaller struct {
	call registry.GatewayCall
	repo string
}

func newGHCaller(call registry.GatewayCall, repo string) (*ghCaller, error) {
	if repo == "" {
		repo = "{owner}/{repo}"
	} else if !repoName.MatchString(repo) {
		return nil, fmt.Errorf("repo must be owner/name, got %q", repo)
	}
	return &ghCaller{call: call, repo: repo}, nil
}

func (c *ghCaller) api(ctx context.Context, path, jq string, args ...string) (string, error) {
	full := append([]string{"api", "repos/" + c.repo + "/" + path, "--jq", jq}, args...)
	out, err := gh.Run(ctx, c.call.Dir, c.call.Env, full...)
	return strings.TrimSpace(out), err
}

func seg(s string) string { return neturl.PathEscape(s) }

// decodeArgs reads a call's arguments into in: none at all are the defaults.
func decodeArgs(args json.RawMessage, in any) error {
	if len(bytes.TrimSpace(args)) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, in); err != nil {
		return fmt.Errorf("the tool's arguments: %w", err)
	}
	return nil
}

func schema(props map[string]any, required ...string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": append([]string{}, required...)})
	return b
}

// gatewayTools are GitHub's tools for a sandboxed agent's gateway.
func gatewayTools() []registry.GatewayTool { return append(ghActionsTools(), ghRefTools()...) }

// ghArgs are the arguments every gh_* tool takes beside its own.
type ghArgs struct {
	Repo string `json:"repo"`
}

// ghProps are a gh_* tool's own arguments' schema, with repo and project added.
func ghProps(p map[string]any) map[string]any {
	p["repo"] = map[string]any{"type": "string", "description": "owner/name; defaults to the project's origin remote"}
	p["project"] = map[string]any{"type": "string", "description": "The project, in a workspace with more than one"}
	return p
}

func strProp() map[string]any { return map[string]any{"type": "string"} }

// addTool adds a gh_* tool to a list.
type addTool func(name, desc string, sch json.RawMessage, readOnly bool, run func(context.Context, registry.GatewayCall) (string, error))

// ghTools collects gh_* tools through the add it hands fill.
func ghTools(fill func(add addTool)) []registry.GatewayTool {
	var out []registry.GatewayTool
	fill(func(name, desc string, sch json.RawMessage, readOnly bool, run func(context.Context, registry.GatewayCall) (string, error)) {
		out = append(out, registry.GatewayTool{Name: name, Desc: desc, InputSchema: sch, ReadOnly: readOnly, Run: run, Source: "github"})
	})
	return out
}

// ghActionsTools read GitHub Actions: a repository variable, and a workflow's runs.
func ghActionsTools() []registry.GatewayTool {
	return ghTools(func(add addTool) {
		add("gh_repo_variable", "Read a GitHub Actions repository variable. Returns {found, name, value}.",
			schema(ghProps(map[string]any{"name": strProp()}), "name"), true,
			func(ctx context.Context, call registry.GatewayCall) (string, error) {
				var in struct {
					ghArgs
					Name string `json:"name"`
				}
				if err := decodeArgs(call.Args, &in); err != nil {
					return "", err
				}
				c, err := newGHCaller(call, in.Repo)
				if err != nil {
					return "", err
				}
				out, err := c.api(ctx, "actions/variables/"+seg(in.Name), `{found: true, name, value}`)
				if gh.NotFound(err) {
					return `{"found":false}`, nil
				}
				return out, err
			})

		add("gh_workflow_runs", "List a workflow's recent runs, newest first. workflow is a file name (release.yml) or an id. status filters, e.g. completed, in_progress, success.",
			schema(ghProps(map[string]any{
				"workflow": strProp(), "status": strProp(), "branch": strProp(),
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}), "workflow"), true,
			func(ctx context.Context, call registry.GatewayCall) (string, error) {
				var in struct {
					ghArgs
					Workflow string `json:"workflow"`
					Status   string `json:"status"`
					Branch   string `json:"branch"`
					Limit    int    `json:"limit"`
				}
				if err := decodeArgs(call.Args, &in); err != nil {
					return "", err
				}
				c, err := newGHCaller(call, in.Repo)
				if err != nil {
					return "", err
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
				return c.api(ctx, "actions/workflows/"+seg(in.Workflow)+"/runs",
					`[.workflow_runs[] | {id, headBranch: .head_branch, headSha: .head_sha, status, conclusion, url: .html_url, createdAt: .created_at}]`, q...)
			})

	})
}

// ghRefTools compare refs, find tags and create one.
func ghRefTools() []registry.GatewayTool {
	return ghTools(func(add addTool) {
		add("gh_compare", "Compare two refs: the commits in head that base lacks. Returns totalCommits and each commit's sha and subject; truncated is set when GitHub cut the list short.",
			schema(ghProps(map[string]any{"base": strProp(), "head": strProp()}), "base", "head"), true,
			func(ctx context.Context, call registry.GatewayCall) (string, error) {
				var in struct {
					ghArgs
					Base string `json:"base"`
					Head string `json:"head"`
				}
				if err := decodeArgs(call.Args, &in); err != nil {
					return "", err
				}
				c, err := newGHCaller(call, in.Repo)
				if err != nil {
					return "", err
				}
				return c.api(ctx, "compare/"+seg(in.Base)+"..."+seg(in.Head),
					`{totalCommits: .total_commits, commits: [.commits[] | {sha, subject: (.commit.message | split("\n")[0])}]}
				 + (if .total_commits > (.commits | length) then {truncated: true} else {} end)`)
			})

		add("gh_matching_refs", "List the tags whose names start with prefix, e.g. v1.2. Returns each tag's ref and sha.",
			schema(ghProps(map[string]any{"prefix": strProp()}), "prefix"), true,
			func(ctx context.Context, call registry.GatewayCall) (string, error) {
				var in struct {
					ghArgs
					Prefix string `json:"prefix"`
				}
				if err := decodeArgs(call.Args, &in); err != nil {
					return "", err
				}
				c, err := newGHCaller(call, in.Repo)
				if err != nil {
					return "", err
				}
				return c.api(ctx, "git/matching-refs/tags/"+seg(in.Prefix), `[.[] | {ref, sha: .object.sha}]`)
			})

		add("gh_create_tag", "Create a tag on GitHub at a commit. With message it is an annotated tag; without, a lightweight one. Refuses a tag that exists.",
			schema(ghProps(map[string]any{"tag": strProp(), "sha": strProp(), "message": strProp()}), "tag", "sha"), false,
			func(ctx context.Context, call registry.GatewayCall) (string, error) {
				var in struct {
					ghArgs
					Tag     string `json:"tag"`
					SHA     string `json:"sha"`
					Message string `json:"message"`
				}
				if err := decodeArgs(call.Args, &in); err != nil {
					return "", err
				}
				if in.Tag == "" || in.SHA == "" {
					return "", errors.New("tag and sha are required")
				}
				c, err := newGHCaller(call, in.Repo)
				if err != nil {
					return "", err
				}
				if _, err := c.api(ctx, "git/ref/tags/"+seg(in.Tag), `.ref`); err == nil {
					return "", fmt.Errorf("tag %s already exists", in.Tag)
				} else if !gh.NotFound(err) {
					return "", err
				}
				target := in.SHA
				if in.Message != "" {
					target, err = c.api(ctx, "git/tags", `.sha`, "--method", "POST",
						"-f", "tag="+in.Tag, "-f", "message="+in.Message, "-f", "object="+in.SHA, "-f", "type=commit")
					if err != nil {
						return "", err
					}
				}
				return c.api(ctx, "git/refs", `{ref, sha: .object.sha}`, "--method", "POST",
					"-f", "ref=refs/tags/"+in.Tag, "-f", "sha="+target)
			})
	})
}
