package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/gateway"
	"github.com/liamg/oriel/sandbox/mcp"
)

// gatewayHost runs the gateway's own tools for one session, with the user's credentials.
type gatewayHost struct{ b *box }

var _ gateway.Host = gatewayHost{}

// Approve is layer 3 for a gateway call: it is held while the session's rules, the app's hooks or the user decide.
func (g gatewayHost) Approve(ctx context.Context, r gateway.Request) (gateway.Answer, error) {
	ask := core.Ask{Kind: core.AskGateway, Tool: r.Tool, Summary: callSummary(r.Tool, r.Args), Input: string(r.Args), Detail: r.Description}
	if !r.AlwaysAsk {
		ask.Rule = r.Tool
	}
	v, err := g.b.approve(ctx, ask)
	if err != nil {
		return gateway.Answer{}, err
	}
	if !v.Allow {
		return gateway.Answer{Decision: gateway.Refuse, By: v.By, Note: v.Note}, nil
	}
	ans := gateway.Answer{Decision: gateway.Once, Run: r.Tool == gateway.Prefix+"ask_user_to_run"}
	if g.b.ruled(ask.Rule) {
		ans.Decision = gateway.Session
	}
	return ans, nil
}

// callSummary is a gateway call in one line: "oriel__push {"project":"api"}".
func callSummary(tool string, args json.RawMessage) string {
	s := strings.TrimSpace(string(args))
	if s == "" || s == "{}" || s == "null" {
		return tool
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return tool + " " + s
}

// project is a project of the session by name, the primary one for "".
func (g gatewayHost) project(name string) (core.Project, error) {
	se, err := g.b.session()
	if err != nil {
		return core.Project{}, err
	}
	if name == "" {
		if p, ok := se.Primary(); ok {
			return p, nil
		}
	}
	for _, p := range se.Projects {
		if p.Name == name {
			return p, nil
		}
	}
	return core.Project{}, fmt.Errorf("%w %q", gateway.ErrNoProject, name)
}

func (g gatewayHost) Commit(ctx context.Context, project, message string, files []string) (int, error) {
	p, err := g.project(project)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(message) == "" {
		return 0, errors.New("a commit needs a message")
	}
	add := []string{"add", "-A"}
	if len(files) > 0 {
		add = append([]string{"add", "--"}, files...)
	}
	if _, err := sandbox.Git(p.Dir, add...); err != nil {
		return 0, err
	}
	staged, err := sandbox.Git(p.Dir, "diff", "--cached", "--name-only")
	if err != nil {
		return 0, err
	}
	n := len(strings.Fields(staged))
	if n == 0 {
		return 0, errors.New("nothing to commit")
	}
	if err := g.keyed(ctx, p, "git commit", "commit", "-m", message); err != nil {
		return 0, err
	}
	return n, nil
}

func (g gatewayHost) Push(ctx context.Context, project string) error {
	p, err := g.project(project)
	if err != nil {
		return err
	}
	branch, err := ownBranch(p)
	if err != nil {
		return err
	}
	return g.keyed(ctx, p, "git push", "push", "origin", "HEAD:refs/heads/"+branch)
}

func (g gatewayHost) Pull(ctx context.Context, project string) error {
	p, err := g.project(project)
	if err != nil {
		return err
	}
	branch, err := ownBranch(p)
	if err != nil {
		return err
	}
	return g.keyed(ctx, p, "git pull", "pull", "--ff-only", "origin", branch)
}

func (g gatewayHost) Fetch(ctx context.Context, project string) error {
	p, err := g.project(project)
	if err != nil {
		return err
	}
	return g.keyed(ctx, p, "git fetch origin", "fetch", "origin")
}

// keyed runs host git in a project for the gateway, asking for a security-key touch while it waits on one.
func (g gatewayHost) keyed(ctx context.Context, p core.Project, what string, args ...string) error {
	return g.b.w.keyed(ctx, g.b.id, what, p.Name, func(ctx context.Context) error {
		_, err := sandbox.GitContext(ctx, p.Dir, args...)
		return err
	})
}

func (g gatewayHost) CreatePR(ctx context.Context, project, title, body string) (string, error) {
	p, err := g.project(project)
	if err != nil {
		return "", err
	}
	branch, err := ownBranch(p)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(title) == "" {
		title, body = g.prDraft(p.Name)
	}
	if strings.TrimSpace(title) == "" {
		return "", gateway.ErrNoDraft
	}
	out, err := g.GH(ctx, p.Name, "pr", "create", "--title", title, "--body", body, "--head", branch)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1]), nil
}

// prDraft is the draft the agent saved with pr_draft.
func (g gatewayHost) prDraft(project string) (title, body string) {
	d := g.b.w.readDraft(g.b.id, project, "pr")
	return d.Title, d.Body
}

func (g gatewayHost) GH(ctx context.Context, project string, args ...string) (string, error) {
	p, err := g.project(project)
	if err != nil {
		return "", err
	}
	env, err := sandbox.GitEnv(p.Dir)
	if err != nil {
		return "", err
	}
	return gateway.RunGH(ctx, p.Dir, env, args...)
}

func (g gatewayHost) Repos(context.Context) ([]gateway.Repo, error) {
	se, err := g.b.session()
	if err != nil {
		return nil, err
	}
	var out []gateway.Repo
	for _, r := range g.b.w.repos.all() {
		if slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Repo == r.Path || p.Dir == r.Path }) {
			continue
		}
		out = append(out, gateway.Repo{Name: string(r.ID), Path: r.Path})
	}
	return out, nil
}

func (g gatewayHost) AddProject(ctx context.Context, repo string) (gateway.Added, error) {
	before, err := g.b.session()
	if err != nil {
		return gateway.Added{}, err
	}
	if err := g.b.w.AddRepo(ctx, g.b.id, core.RepoID(repo)); err != nil {
		return gateway.Added{}, err
	}
	se, err := g.b.session()
	if err != nil {
		return gateway.Added{}, err
	}
	if err := sandbox.SaveManifest(g.b.root, manifestOf(g.b.root, se)); err != nil {
		return gateway.Added{}, err
	}
	var added core.Project
	for _, p := range se.Projects {
		if !slices.ContainsFunc(before.Projects, func(o core.Project) bool { return o.Name == p.Name }) {
			added = p
		}
	}
	g.b.restartWhenIdle("a project joined the sandbox")
	return gateway.Added{Name: added.Name, Dir: added.Dir}, nil
}

func (g gatewayHost) RunOnHost(ctx context.Context, command, cwd string) mcp.CallResult {
	dir := g.b.root
	switch {
	case filepath.IsAbs(cwd):
		dir = cwd
	case cwd != "":
		dir = filepath.Join(g.b.root, cwd)
	}
	return gateway.HostRun(ctx, dir, command)
}

func (g gatewayHost) RunTail() string { return "" }

func (g gatewayHost) SSHAgent(_ context.Context, d time.Duration) (time.Time, error) {
	return g.b.ssh.Grant(d)
}

// RequestHost asks the user to let the session leave the sandbox. A refusal holds for the rest of the session;
// an allow restarts the agent on the host, resumed, once its turn ends.
func (g gatewayHost) RequestHost(ctx context.Context, why string) (bool, string) {
	if rec, ok := sandbox.Load(g.b.root); ok && rec.HostDenied {
		return false, "The user already refused to let this session leave the sandbox. Carry on inside it, or ask them to run what needs the host with oriel__ask_user_to_run."
	}
	ask := core.Ask{Kind: core.AskGateway, Tool: gateway.Prefix + "request_host", Summary: "Leave the sandbox: " + why,
		Detail: "The agent would run on this machine with your full access."}
	v, err := g.b.approve(ctx, ask)
	if err != nil {
		return false, "Nobody answered the request to leave the sandbox, so it was refused."
	}
	if !v.Allow {
		_, _ = sandbox.Update(g.b.root, func(r *sandbox.Record) { r.HostDenied = true })
		return false, "The user refused to let this session leave the sandbox. Carry on inside it, or ask them to run what needs the host with oriel__ask_user_to_run."
	}
	if _, err := sandbox.Update(g.b.root, func(r *sandbox.Record) { r.Escaped = true }); err != nil {
		return false, err.Error()
	}
	if h, _ := g.b.w.sessionFor(g.b.id, false); h != nil {
		h.setAgent(func(a *core.AgentState) {
			a.Sandboxed = false
			if a.Policy == "bypass" {
				a.Policy = "ask"
			}
		})
	}
	g.b.restartWhenIdle("the user let the session leave the sandbox")
	return true, "The user allowed it. End your turn now: you restart on the host, with this conversation, once it ends."
}
