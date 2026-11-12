// Package guard is the guard rails every provider's agent is held to, before its own gate has a say: it never posts
// to a code review, never edits the user's checkouts, and in a sandbox reaches GitHub only through the gateway.
package guard

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/gh"
)

// Gateway is the prefix of the sandbox gateway's tools.
const Gateway = "mcp__oriel-gw__oriel__"

// gatewayServer is the prefix of every tool the sandbox gateway carries: its own, and the user's servers behind it.
const gatewayServer = "mcp__oriel-gw__"

// NoPosting is why an agent's attempt to post to a pull request's review or conversation is refused.
const NoPosting = "Oriel: agents never post to GitHub reviews. Answer the thread with mcp__oriel__reply: on a GitHub " +
	"thread your reply is a draft, and the user forwards it."

var (
	// In a sandbox gh has no credentials; it's caught as a command opening the line or following ; or &&.
	sandboxGH  = regexp.MustCompile(`(^|;|&&)\s*gh(\s|$)`)
	sandboxGit = regexp.MustCompile("(^|[\\s;&|(`$])git(\\s+-\\S+(\\s+\\S+)?)*\\s+(push|pull|fetch)\\b")
	quoted     = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
)

// EditTools are the tools that write a file named in their input (file_path, or notebook_path).
var EditTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

// Decision is a guard rail's answer: Allow or Deny with Reason, Ask to put the call to Oriel's permission flow
// whatever the agent's own gate would say, or none to leave it to the agent's gate.
type Decision struct {
	Allow, Deny, Ask bool
	Reason           string
}

// Check decides a tool call, in Oriel's names for tools (Bash, Edit…, mcp__server__tool), for the guard rails.
// Oriel's own tools go ahead, and so do the sandbox gateway's, which gates its own calls. Every other MCP server's
// call asks Oriel, which answers from the session's rules and the app's policy or puts it to the user; an agent
// nobody can ask (PolicyRefuse) is answered here instead: its Allow goes ahead, the rest is refused.
func Check(spec agent.StartSpec, tool string, input json.RawMessage) Decision {
	if strings.HasPrefix(tool, "mcp__oriel__") || spec.Sandbox != nil && strings.HasPrefix(tool, gatewayServer) && !gh.ReviewTool(tool) {
		return Decision{Allow: true}
	}
	var in struct {
		Command  string `json:"command"`
		File     string `json:"file_path"`
		Notebook string `json:"notebook_path"`
	}
	_ = json.Unmarshal(input, &in)
	if gh.ReviewTool(tool) || tool == "Bash" && gh.PostsReview(in.Command) {
		return Decision{Deny: true, Reason: NoPosting}
	}
	if strings.HasPrefix(tool, "mcp__") {
		switch {
		case spec.Policy != agent.PolicyRefuse:
			return Decision{Ask: true, Reason: "Oriel decides every MCP call."}
		case Allowed(spec, tool):
			return Decision{Allow: true}
		}
		return Decision{Deny: true, Reason: "Oriel: nobody is here to approve " + tool + ". This agent may use only Oriel's " +
			"tools and those it was allowed: carry on without it, or say in your reply what the user should do."}
	}
	if spec.Sandbox != nil && tool == "Bash" {
		cmd := quoted.ReplaceAllString(in.Command, `""`)
		switch {
		case sandboxGH.MatchString(cmd):
			return Decision{Deny: true, Reason: "Oriel sandbox: gh has no credentials here. Use the gateway instead: " +
				Gateway + "pr_create to open the pull request, " + Gateway + "ci for checks and logs, " + Gateway +
				"push to push, " + Gateway + "gh_* for tags, workflow runs, compares and repo variables, or the " +
				"mcp__oriel-gw__github__* tools when the GitHub server is installed."}
		case sandboxGit.MatchString(cmd):
			return Decision{Deny: true, Reason: "Oriel sandbox: git has no credentials here. Use " + Gateway + "push or " +
				Gateway + "fetch, which run on the host. For private dependencies over ssh, ask for " +
				Gateway + "ssh_agent."}
		}
	}
	for _, t := range EditTools {
		if tool != t {
			continue
		}
		if d := Protected(spec, cmp.Or(in.File, in.Notebook)); d.Deny {
			return d
		}
	}
	return Decision{}
}

// Allowed says a tool goes ahead under agent.PolicyRefuse, which refuses the rest: Oriel's own, and spec's Allow.
func Allowed(spec agent.StartSpec, tool string) bool {
	return strings.HasPrefix(tool, "mcp__oriel__") || slices.Contains(spec.Allow, tool)
}

// Protected refuses a write to file when it's in one of spec's protected checkouts and not in the session's own
// copy. A relative file is the session's.
func Protected(spec agent.StartSpec, file string) Decision {
	if file == "" {
		return Decision{}
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(spec.Cwd, file)
	}
	file = filepath.Clean(file)
	if Under(file, spec.Cwd) {
		return Decision{}
	}
	for _, root := range spec.Protect {
		if Under(file, root) {
			return Decision{Deny: true, Reason: "Oriel: " + root + " is the original checkout, which this session " +
				"must not edit. Edit the copy under " + spec.Cwd + " instead."}
		}
	}
	return Decision{}
}

// Under says p is root or inside it.
func Under(p, root string) bool {
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}
