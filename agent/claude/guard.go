package claude

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liamg/oriel/agent"
)

// The guard rails Oriel answers tool.check with, before the mode or its dialog have a say.

// gateway is the prefix of the sandbox gateway's tools.
const gateway = "mcp__oriel-gw__oriel__"

var (
	// In a sandbox gh has no credentials; it's caught as a command opening the line or following ; or &&.
	sandboxGH  = regexp.MustCompile(`(^|;|&&)\s*gh(\s|$)`)
	sandboxGit = regexp.MustCompile("(^|[\\s;&|(`$])git(\\s+-\\S+(\\s+\\S+)?)*\\s+(push|pull|fetch)\\b")
	quoted     = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)
)

var editTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

// check decides a tool call for the guard rails: allow, deny with a reason, or "" to leave it to the mode.
func check(spec agent.StartSpec, tool string, input json.RawMessage) checkOut {
	if strings.HasPrefix(tool, "mcp__oriel__") {
		return checkOut{Decision: "allow"}
	}
	var in struct {
		Command  string `json:"command"`
		File     string `json:"file_path"`
		Notebook string `json:"notebook_path"`
	}
	_ = json.Unmarshal(input, &in)
	if spec.Sandbox != nil && tool == "Bash" {
		cmd := quoted.ReplaceAllString(in.Command, `""`)
		switch {
		case sandboxGH.MatchString(cmd):
			return checkOut{Decision: "deny", Reason: "Oriel sandbox: gh has no credentials here. Use the gateway instead: " +
				gateway + "pr_create to open the pull request, " + gateway + "ci for checks and logs, " + gateway +
				"push to push, " + gateway + "gh_* for tags, workflow runs, compares and repo variables, or the " +
				"mcp__oriel-gw__github__* tools when the GitHub server is installed."}
		case sandboxGit.MatchString(cmd):
			return checkOut{Decision: "deny", Reason: "Oriel sandbox: git has no credentials here. Use " + gateway + "push or " +
				gateway + "fetch, which run on the host. For private dependencies over ssh, ask for " +
				gateway + "ssh_agent."}
		}
	}
	for _, t := range editTools {
		if tool != t {
			continue
		}
		file := in.File
		if file == "" {
			file = in.Notebook
		}
		if file == "" {
			break
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(spec.Cwd, file)
		}
		file = filepath.Clean(file)
		if under(file, spec.Cwd) {
			break
		}
		for _, root := range spec.Protect {
			if under(file, root) {
				return checkOut{Decision: "deny", Reason: "Oriel: " + root + " is the original checkout, which this session " +
					"must not edit. Edit the copy under " + spec.Cwd + " instead."}
			}
		}
	}
	return checkOut{}
}

func under(p, root string) bool {
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}
