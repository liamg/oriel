package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/mcp"
)

// Prefix starts the name of each of Oriel's own tools.
const Prefix = "oriel__"

// SSHSocket is the forwarded ssh-agent's socket inside the container.
const SSHSocket = sandbox.ContainerRunDir + "/ssh-agent.sock"

// toolResult turns the outcome of a host action into a tool result.
func toolResult(err error, ok string) mcp.CallResult {
	if err != nil {
		return mcp.TextResult(err.Error(), true)
	}
	return mcp.TextResult(ok, false)
}

type projectArg struct {
	Project string `json:"project"`
}

func schema(props map[string]any, required ...string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": append([]string{}, required...)})
	return b
}

var (
	strProp     = map[string]any{"type": "string"}
	projectProp = map[string]any{"type": "string", "description": "The project, in a workspace with more than one"}
)

// Builtins are Oriel's own host actions, run by h with the host's credentials.
func Builtins(h Host) map[string]Builtin {
	b := map[string]Builtin{}
	add := func(name, desc string, sch json.RawMessage, alwaysAsk bool, run func(context.Context, json.RawMessage) (mcp.CallResult, error)) {
		full := Prefix + name
		b[full] = Builtin{Tool: mcp.Tool{Name: full, Description: desc, InputSchema: sch}, AlwaysAsk: alwaysAsk, Run: run}
	}
	sync := func(run func(context.Context, string) error, done string) func(context.Context, json.RawMessage) (mcp.CallResult, error) {
		return func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in projectArg
			_ = json.Unmarshal(args, &in)
			return toolResult(run(ctx, in.Project), done), nil
		}
	}

	add("commit", "Commit changes in a project on the host, with the user's git identity and signing. files defaults to every changed file.",
		schema(map[string]any{"message": strProp, "files": map[string]any{"type": "array", "items": strProp}, "project": projectProp}, "message"), false,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				Message string   `json:"message"`
				Files   []string `json:"files"`
				Project string   `json:"project"`
			}
			_ = json.Unmarshal(args, &in)
			n, err := h.Commit(ctx, in.Project, in.Message, in.Files)
			return toolResult(err, fmt.Sprintf("Committed %d file(s).", n)), nil
		})
	add("push", "Push this workspace's branch to origin, from the host.", schema(map[string]any{"project": projectProp}), false,
		sync(h.Push, "Pushed."))
	add("pull", "Pull the branch from origin, fast-forward only, on the host.", schema(map[string]any{"project": projectProp}), false,
		sync(h.Pull, "Pulled."))
	add("fetch", "Fetch origin, on the host.", schema(map[string]any{"project": projectProp}), false,
		sync(h.Fetch, "Fetched."))
	add("pr_create", "Open the pull request, from the saved PR draft unless a title and body are given.",
		schema(map[string]any{"title": strProp, "body": strProp, "project": projectProp}), false,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				Title   string `json:"title"`
				Body    string `json:"body"`
				Project string `json:"project"`
			}
			_ = json.Unmarshal(args, &in)
			url, err := h.CreatePR(ctx, in.Project, in.Title, in.Body)
			if errors.Is(err, ErrVerify) {
				return mcp.TextResult("The user has to verify this change in Oriel before it is raised. Ask them to press Verify & raise on the Pull request page.", true), nil
			}
			if err != nil {
				return toolResult(err, ""), nil
			}
			return mcp.TextResult("Opened "+url, false), nil
		})
	add("ci", "The pull request's checks, and with logs=true the failed jobs' logs.",
		schema(map[string]any{"project": projectProp, "logs": map[string]any{"type": "boolean"}}), false,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				Project string `json:"project"`
				Logs    bool   `json:"logs"`
			}
			_ = json.Unmarshal(args, &in)
			return ciReport(ctx, h, in.Project, in.Logs), nil
		})
	add("repos", "List the user's repositories that could join this workspace, by name and host path. The sandbox cannot see them itself; pass a path to oriel__add_project.",
		schema(map[string]any{}), false,
		func(ctx context.Context, _ json.RawMessage) (mcp.CallResult, error) {
			repos, err := h.Repos(ctx)
			if err != nil {
				return toolResult(err, ""), nil
			}
			if len(repos) == 0 {
				return mcp.TextResult("No repositories available to add: none found under the repository roots in Oriel's settings.", false), nil
			}
			var sb strings.Builder
			for _, r := range repos {
				fmt.Fprintf(&sb, "%s\t%s\n", r.Name, r.Path)
			}
			return mcp.TextResult(sb.String(), false), nil
		})
	add("add_project", "Bring another of the user's repositories into this workspace. The agent restarts, resumed, once its turn ends, to see it.",
		schema(map[string]any{"repo": map[string]any{"type": "string", "description": "Path of the repository on the host, as oriel__repos lists it"}}, "repo"), false,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				Repo string `json:"repo"`
			}
			_ = json.Unmarshal(args, &in)
			a, err := h.AddProject(ctx, in.Repo)
			if err != nil {
				return mcp.TextResult(err.Error(), true), nil
			}
			return mcp.TextResult(addedText(a), false), nil
		})
	add("ask_user_to_run", "Ask the user to run a command on their machine that the sandbox cannot (docker, tilt, anything needing the host). They can run it themselves or have Oriel run it on the host for them, once. cwd is the workspace root by default, a path relative to it, or an absolute host path. Blocks until they answer; returns the output, or their note. Never use it to install dependencies: that runs the repo's own scripts on the host; use oriel__ssh_agent and install in the sandbox.",
		schema(map[string]any{"command": strProp, "why": strProp, "cwd": strProp}, "command", "why"), true,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			ans, _ := AnswerFrom(ctx)
			if ans.Run {
				var in struct {
					Command string `json:"command"`
					Cwd     string `json:"cwd"`
				}
				_ = json.Unmarshal(args, &in)
				return h.RunOnHost(ctx, in.Command, in.Cwd), nil
			}
			var sb strings.Builder
			sb.WriteString("The user says they ran it.")
			if n := strings.TrimSpace(ans.Note); n != "" {
				sb.WriteString(" Their note: " + n)
			}
			if out := h.RunTail(); out != "" {
				sb.WriteString("\n\nThe end of the terminal:\n" + out)
			}
			return mcp.TextResult(sb.String(), false), nil
		})
	add("ssh_agent", "Let the sandbox use the user's ssh-agent for a few minutes (at most 5): for cloning or installing dependencies from private repositories over ssh. The keys stay on the host; each use may need the user's security-key touch.",
		schema(map[string]any{"why": strProp, "minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 5}}, "why"), false,
		func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				Minutes int `json:"minutes"`
			}
			_ = json.Unmarshal(args, &in)
			until, err := h.SSHAgent(ctx, time.Duration(in.Minutes)*time.Minute)
			if err != nil {
				return mcp.TextResult(err.Error(), true), nil
			}
			return mcp.TextResult(fmt.Sprintf("The ssh-agent is available until %s (SSH_AUTH_SOCK=%s). Run what needs it now; after that it is closed again.",
				until.Format("15:04:05"), SSHSocket), false), nil
		})
	ghBuiltins(h, func(name, desc string, sch json.RawMessage, readOnly bool, run func(context.Context, json.RawMessage) (mcp.CallResult, error)) {
		add(name, desc, sch, false, run)
		t := b[Prefix+name]
		t.Tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: &readOnly}
		b[Prefix+name] = t
	})
	b[Prefix+"request_host"] = Builtin{
		Tool: mcp.Tool{Name: Prefix + "request_host", InputSchema: schema(map[string]any{"why": strProp}, "why"),
			Description: "Ask the user to run this session outside the sandbox, with their full access. Only for a task that cannot be done in the sandbox at all; say exactly why. A refusal holds for the rest of the session."},
		Direct: true,
		Run: func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error) {
			var in struct {
				Why string `json:"why"`
			}
			_ = json.Unmarshal(args, &in)
			ok, msg := h.RequestHost(ctx, in.Why)
			return mcp.TextResult(msg, !ok), nil
		},
	}
	return b
}

// addedText is add_project's answer. The sandbox the agent is in cannot see
// the project or its toolchains, so it says so plainly: nothing more can be
// done here before the restart.
func addedText(a Added) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Added %s at %s.", a.Name, a.Dir)
	switch {
	case a.ToolsErr != nil:
		fmt.Fprintf(&sb, " Its toolchains did not install: %v. The restart tries again.", a.ToolsErr)
	case len(a.Tools) > 0:
		fmt.Fprintf(&sb, " Installed %s.", strings.Join(a.Tools, ", "))
	}
	sb.WriteString(" This sandbox has neither the project nor its toolchains. Run nothing else: end your turn now. You restart, with this conversation, in a sandbox that has both.")
	return sb.String()
}

// ciReport is the pull request's checks as text, with the failed logs if
// asked for.
func ciReport(ctx context.Context, h Host, project string, logs bool) mcp.CallResult {
	out, err := h.GH(ctx, project, "pr", "checks")
	if strings.TrimSpace(out) == "" && err != nil {
		if errors.Is(err, ErrNoProject) {
			return mcp.TextResult(err.Error(), true)
		}
		return mcp.TextResult("no checks: "+err.Error(), true)
	}
	if !logs {
		return mcp.TextResult(out, false)
	}
	var sb strings.Builder
	sb.WriteString(out)
	for _, id := range failedRunIDs(out) {
		log, _ := h.GH(ctx, project, "run", "view", id, "--log-failed")
		sb.WriteString("\n\n--- failed jobs in run " + id + " ---\n" + tailLines(log, 200))
	}
	return mcp.TextResult(sb.String(), false)
}

var runID = regexp.MustCompile(`/actions/runs/(\d+)`)

// failedRunIDs picks the workflow runs of failing checks out of `gh pr checks`.
func failedRunIDs(checks string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(checks, "\n") {
		if !strings.Contains(line, "fail") {
			continue
		}
		if m := runID.FindStringSubmatch(line); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
