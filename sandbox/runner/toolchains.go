package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// Toolchains are installed by oriel, never by the agent: a provisioning
// container runs `mise install` for the versions the projects ask for into
// one directory per machine, and every sandbox mounts it read-only. A repo
// cannot plant a trojaned compiler for the next workspace to run.

// MiseDir is where the toolchains are mounted, in the provisioner and in
// every sandbox, so the paths mise records hold in both.
const MiseDir = "/mise"

func toolchainDir() string { return filepath.Join(sandbox.Base(), "toolchains") }

var (
	toolName    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)
	toolVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,40}$`)
	versionNum  = regexp.MustCompile(`\d+(?:\.\d+){0,2}`)
)

// Tool is one toolchain at a version, as mise names it: go@1.25.
type Tool struct{ Name, Version string }

func (t Tool) String() string { return t.Name + "@" + t.Version }

func validTool(t Tool) bool {
	return toolName.MatchString(t.Name) && toolVersion.MatchString(t.Version)
}

// agentTool is a tool an agent's provider names: a mise backend's too (npm:@google/gemini-cli), at a version.
var agentTool = regexp.MustCompile(`^(?:[a-z][a-z0-9-]*:)?@?[A-Za-z0-9][A-Za-z0-9._/-]{0,80}@[A-Za-z0-9][A-Za-z0-9._+-]{0,40}$`)

// agentTools are the tools an agent's provider asks a container for (agent.SandboxNeeds.Tools), each name@version.
func agentTools(names []string) ([]Tool, error) {
	var out []Tool
	for _, n := range names {
		if !agentTool.MatchString(n) {
			return nil, fmt.Errorf("the agent asks for %q, which isn't a mise tool at a version (node@22, npm:@google/gemini-cli@0.9.0)", n)
		}
		i := strings.LastIndex(n, "@")
		out = append(out, Tool{Name: n[:i], Version: n[i+1:]})
	}
	return out, nil
}

// DetectTools works out the toolchains a project needs: from its mise or asdf
// config when it has one, else from Go, Node and Python metadata. Only names
// and versions are taken; nothing in the project is run.
func DetectTools(dir string) []Tool {
	if tools := miseTools(dir); len(tools) > 0 {
		return tools
	}
	var out []Tool
	if v := goVersion(dir); v != "" {
		out = append(out, Tool{"go", v})
	}
	if v := nodeVersion(dir); v != "" {
		out = append(out, Tool{"node", v})
	}
	if v := pythonVersion(dir); v != "" {
		out = append(out, Tool{"python", v})
	}
	return out
}

func readProject(dir, name string) (string, bool) {
	b, err := sandbox.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// miseTools reads [tools] from mise.toml and the lines of .tool-versions.
func miseTools(dir string) []Tool {
	var out []Tool
	add := func(name, version string) {
		t := Tool{strings.TrimSpace(name), strings.Trim(strings.TrimSpace(version), `"'[] `)}
		if i := strings.IndexAny(t.Version, `",' `); i > 0 {
			t.Version = t.Version[:i]
		}
		if validTool(t) {
			out = append(out, t)
		}
	}
	for _, name := range []string{"mise.toml", ".mise.toml"} {
		text, ok := readProject(dir, name)
		if !ok {
			continue
		}
		inTools := false
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				inTools = line == "[tools]"
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok && inTools {
				add(k, v)
			}
		}
		return out
	}
	if text, ok := readProject(dir, ".tool-versions"); ok {
		for _, line := range strings.Split(text, "\n") {
			if f := strings.Fields(line); len(f) >= 2 && !strings.HasPrefix(f[0], "#") {
				add(f[0], f[1])
			}
		}
	}
	return out
}

func goVersion(dir string) string {
	for _, name := range []string{"go.work", "go.mod"} {
		text, ok := readProject(dir, name)
		if !ok {
			continue
		}
		lang := ""
		sc := bufio.NewScanner(strings.NewReader(text))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 2 {
				continue
			}
			switch f[0] {
			case "toolchain":
				if v := versionNum.FindString(strings.TrimPrefix(f[1], "go")); v != "" {
					return v
				}
			case "go":
				lang = versionNum.FindString(f[1])
			}
		}
		if lang != "" {
			return lang
		}
	}
	return ""
}

func nodeVersion(dir string) string {
	for _, name := range []string{".nvmrc", ".node-version"} {
		if text, ok := readProject(dir, name); ok {
			if v := versionNum.FindString(text); v != "" {
				return v
			}
			return "lts"
		}
	}
	text, ok := readProject(dir, "package.json")
	if !ok {
		return ""
	}
	var pkg struct {
		Engines map[string]string `json:"engines"`
	}
	if json.Unmarshal([]byte(text), &pkg) == nil {
		if v := versionNum.FindString(pkg.Engines["node"]); v != "" {
			return v
		}
	}
	return "lts"
}

func pythonVersion(dir string) string {
	if text, ok := readProject(dir, ".python-version"); ok {
		if v := versionNum.FindString(text); v != "" {
			return v
		}
	}
	if text, ok := readProject(dir, "pyproject.toml"); ok {
		for _, line := range strings.Split(text, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "requires-python" {
				if n := versionNum.FindString(v); n != "" {
					return n
				}
			}
		}
		return "latest"
	}
	for _, name := range []string{"requirements.txt", "Pipfile", "uv.lock"} {
		if _, ok := readProject(dir, name); ok {
			return "latest"
		}
	}
	return ""
}

// workspaceTools are every project's toolchains, the primary project's first
// so they win on PATH.
func workspaceTools(root string) []Tool {
	var dirs []string
	if rec, _ := sandbox.Load(root); rec.Repo != "" {
		dirs = append(dirs, root)
	} else if ws, ok := sandbox.LoadManifest(root); ok {
		sort.SliceStable(ws.Projects, func(i, j int) bool { return ws.Projects[i].Primary && !ws.Projects[j].Primary })
		for _, p := range ws.Projects {
			dirs = append(dirs, filepath.Join(root, p.Dir))
		}
	}
	seen := map[string]bool{}
	var out []Tool
	for _, d := range dirs {
		for _, t := range DetectTools(d) {
			if !seen[t.String()] {
				seen[t.String()] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// ProvisionProject installs the toolchains a project just added to root's
// workspace needs, so the restart that follows finds them ready. It returns
// what the project asks for; none for a workspace that is not sandboxed.
func ProvisionProject(ctx context.Context, root, dir string) ([]Tool, error) {
	if rec, ok := sandbox.Load(root); !ok || !rec.Sandboxed() {
		return nil, nil
	}
	tools := DetectTools(dir)
	if len(tools) == 0 {
		return nil, nil
	}
	id, err := sandbox.CurrentIdentity()
	if err != nil {
		return nil, err
	}
	if _, err := Provision(ctx, id, tools); err != nil {
		return nil, err
	}
	return tools, nil
}

var provisionMu sync.Mutex

func resolvedPath() string { return filepath.Join(toolchainDir(), "resolved.json") }

func loadResolved() map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(resolvedPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// Provision installs tools that are not installed yet and returns the bin
// directories to put on PATH, in order, as the sandbox sees them.
func Provision(ctx context.Context, id sandbox.Identity, tools []Tool) ([]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	provisionMu.Lock()
	defer provisionMu.Unlock()
	dir := toolchainDir()
	for _, d := range []string{"data", "cache", "state"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	resolved := loadResolved()
	var missing []string
	for _, t := range tools {
		p, ok := resolved[t.String()]
		if ok {
			if _, err := os.Stat(hostPath(p)); err == nil {
				continue
			}
		}
		missing = append(missing, t.String())
	}
	if len(missing) > 0 {
		sandbox.Report(ctx, sandbox.Progress{Step: "Installing " + strings.Join(missing, ", ") + " in the sandbox",
			Note: "Once for each version: later sessions reuse it."})
		if err := ensureImage(ctx, container.BaseImage(), container.BaseDockerfile); err != nil {
			return nil, err
		}
		out, err := container.Run(ctx, installArgs(id, dir, missing)...)
		if err != nil {
			return nil, fmt.Errorf("installing %s: %w", strings.Join(missing, " "), err)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if k, v, ok := strings.Cut(line, "\t"); ok && strings.HasPrefix(v, MiseDir+"/") {
				resolved[k] = v
			}
		}
		b, _ := json.MarshalIndent(resolved, "", "  ")
		if err := os.WriteFile(resolvedPath(), b, 0o644); err != nil {
			return nil, err
		}
	}
	var bins []string
	for _, t := range tools {
		if p, ok := resolved[t.String()]; ok {
			bins = append(bins, p+"/bin")
		}
	}
	return bins, nil
}

// installArgs runs `mise install` for tools in the base image, writing to dir. A project's image never runs here: it
// would have dir, which every sandbox mounts, open for writing.
func installArgs(id sandbox.Identity, dir string, tools []string) []string {
	args := []string{"run", "--rm", "--user", fmt.Sprintf("%d:%d", id.UID, id.GID),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"-e", "HOME=/tmp", "-e", "MISE_YES=1",
		"-e", "MISE_DATA_DIR=" + MiseDir + "/data", "-e", "MISE_CACHE_DIR=" + MiseDir + "/cache",
		"-e", "MISE_STATE_DIR=" + MiseDir + "/state",
		"--mount", "type=bind,source=" + dir + ",target=" + MiseDir,
		"--entrypoint", "sh", container.BaseImage(), "-c",
		`mise install "$@" >&2 && for t; do printf '%s\t%s\n' "$t" "$(mise where "$t")"; done`, "sh"}
	return append(args, tools...)
}

// hostPath is where a path inside MiseDir is on the host.
func hostPath(p string) string {
	return filepath.Join(toolchainDir(), strings.TrimPrefix(p, MiseDir))
}
