package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/gitx"
)

// Host git in a sandboxed workspace runs on files the agent can write. GitEnv
// pins it to the git directories oriel set up and switches off everything in
// git that runs a command.

// hostGitConfig applies to every host git call in a sandboxed workspace.
// Hooks are off because a repo's hooksPath can point into the worktree.
var hostGitConfig = [][2]string{
	{"core.hooksPath", os.DevNull},
	{"core.fsmonitor", "false"},
}

// Guard vets a sandboxed workspace's git config before host git runs there, and remembers each file's verdict until
// the file changes. The session host keeps one for its life; a nil Guard vets every time.
type Guard struct {
	mu       sync.Mutex
	worktree map[string]wtResult    // a repo's config → whether it turns per-worktree config on
	configs  map[string]checkResult // a config file → whether it holds only safe keys
}

// GitEnv returns the environment host git needs to run in dir, or nil when
// dir is not in a sandboxed workspace. An error means git must not run there.
// It vets afresh each time: the session host's Guard remembers.
func GitEnv(dir string) ([]string, error) { return (*Guard)(nil).GitEnv(dir) }

// GitEnv is the package's GitEnv, remembering what it vetted.
func (g *Guard) GitEnv(dir string) ([]string, error) {
	root, ok := managedRoot(dir)
	if !ok {
		return nil, nil
	}
	gitDir, workTree, err := g.hostGitDirs(root, dir)
	if err != nil {
		return nil, err
	}
	env := []string{"GIT_DIR=" + gitDir, "GIT_WORK_TREE=" + workTree}
	env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(hostGitConfig)))
	for i, kv := range hostGitConfig {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	return env, nil
}

// managedRoot finds the sandboxed workspace dir belongs to.
func managedRoot(dir string) (string, bool) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(recordPath(d)); err == nil && Managed(d) {
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// hostGitDirs picks the git directory and work tree for dir from what oriel
// recorded, never from the .git file in the workspace.
func (g *Guard) hostGitDirs(root, dir string) (gitDir, workTree string, err error) {
	abs, _ := filepath.Abs(dir)
	if rec, ok := Load(root); ok && rec.Repo != "" {
		admin, err := WorktreeAdmin(rec.Repo, root)
		if err != nil {
			return "", "", err
		}
		if err := g.checkWorktreeConfig(rec.Repo, admin); err != nil {
			return "", "", err
		}
		return admin, root, nil
	}
	m, _ := LoadManifest(root)
	for _, p := range m.Projects {
		proj := filepath.Join(root, p.Dir)
		if !within(abs, proj) {
			continue
		}
		admin, err := WorktreeAdmin(p.Repo, proj)
		if err != nil {
			return "", "", err
		}
		if err := g.checkWorktreeConfig(p.Repo, admin); err != nil {
			return "", "", err
		}
		return admin, proj, nil
	}
	gitDir = filepath.Join(root, ".git")
	fi, err := os.Lstat(gitDir)
	if err != nil || !fi.IsDir() {
		return "", "", errors.New("the workspace's .git is not a directory Oriel made")
	}
	for _, f := range []string{"commondir", filepath.Join("objects", "info", "alternates")} {
		if _, err := os.Lstat(filepath.Join(gitDir, f)); err == nil {
			return "", "", fmt.Errorf("the workspace's .git has a %s Oriel did not write", f)
		}
	}
	if err := g.CheckConfig(filepath.Join(gitDir, "config")); err != nil {
		return "", "", err
	}
	return gitDir, root, nil
}

// WorktreeAdmin finds the directory under repo/.git/worktrees that belongs to
// the worktree at wt, by the gitdir file git wrote there.
func WorktreeAdmin(repo, wt string) (string, error) {
	base := filepath.Join(repo, ".git", "worktrees")
	ents, err := os.ReadDir(base)
	if err != nil {
		return "", fmt.Errorf("no worktrees in %s: %w", repo, err)
	}
	want := resolved(filepath.Join(wt, ".git"))
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(base, e.Name(), "gitdir"))
		if err == nil && resolved(strings.TrimSpace(string(b))) == want {
			return filepath.Join(base, e.Name()), nil
		}
	}
	return "", fmt.Errorf("%s is not a worktree of %s", wt, repo)
}

// resolved is path with its symlinks followed, or cleaned when it can't be: git may write either form.
func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// checkWorktreeConfig vets the per-worktree config, which sits in the admin
// directory the agent can write, when the repo turns it on.
func (g *Guard) checkWorktreeConfig(repo, admin string) error {
	cfg := filepath.Join(repo, ".git", "config")
	fi, err := os.Stat(cfg)
	if err != nil {
		return nil
	}
	stamp := fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
	prev, ok := g.lookWorktree(cfg)
	on := prev.on
	if !ok || prev.stamp != stamp {
		out, err := exec.Command("git", "config", "--file", cfg, "--bool", "extensions.worktreeConfig").Output()
		on = err == nil && strings.TrimSpace(string(out)) == "true"
		g.keepWorktree(cfg, wtResult{stamp, on})
	}
	if !on {
		return nil
	}
	return g.CheckConfig(filepath.Join(admin, "config.worktree"))
}

func (g *Guard) lookWorktree(cfg string) (wtResult, bool) {
	if g == nil {
		return wtResult{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.worktree[cfg]
	return r, ok
}

func (g *Guard) keepWorktree(cfg string, r wtResult) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.worktree == nil {
		g.worktree = map[string]wtResult{}
	}
	g.worktree[cfg] = r
}

func (g *Guard) lookConfig(path string) (checkResult, bool) {
	if g == nil {
		return checkResult{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	r, ok := g.configs[path]
	return r, ok
}

func (g *Guard) keepConfig(path string, r checkResult) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.configs == nil {
		g.configs = map[string]checkResult{}
	}
	g.configs[path] = r
}

type wtResult struct {
	stamp string
	on    bool
}

// safeKeys are config keys host git may read from an agent-writable file:
// none of them runs a command or pulls in another file.
var safeKeys = map[string]bool{
	"core.repositoryformatversion": true, "core.filemode": true, "core.bare": true,
	"core.logallrefupdates": true, "core.ignorecase": true, "core.precomposeunicode": true,
	"core.symlinks": true, "core.autocrlf": true, "core.eol": true, "core.safecrlf": true,
	"core.quotepath": true, "core.compression": true, "core.sparsecheckout": true,
	"core.sparsecheckoutcone": true, "core.untrackedcache": true, "core.checkstat": true,
	"core.trustctime": true, "core.abbrev": true, "core.commentchar": true, "core.whitespace": true,
	"extensions.worktreeconfig": true, "extensions.objectformat": true,
	"pull.rebase": true, "pull.ff": true, "fetch.prune": true, "push.default": true,
	"merge.conflictstyle": true, "rerere.enabled": true,
	"diff.renames": true, "diff.algorithm": true, "diff.context": true, "diff.noprefix": true,
	"diff.mnemonicprefix": true, "diff.indentheuristic": true, "diff.colormoved": true,
	"status.showuntrackedfiles": true, "log.date": true, "log.decorate": true,
	"gc.auto": true, "gc.autodetach": true, "index.version": true, "init.defaultbranch": true,
}

// safePrefixes are key families that never run anything.
var safePrefixes = []string{"user.", "author.", "committer.", "color.", "advice.", "i18n."}

// safeBranchKeys are the branch.<name>.* settings that are only data.
var safeBranchKeys = map[string]bool{"remote": true, "merge": true, "rebase": true, "description": true, "pushremote": true}

// safeRemoteKeys are the remote.<name>.* settings that are only data.
var safeRemoteKeys = map[string]bool{"url": true, "fetch": true, "pushurl": true, "push": true, "tagopt": true, "prune": true}

// CheckConfig refuses a config file holding anything outside the safe keys.
// A missing file is fine.
func CheckConfig(path string) error { return (*Guard)(nil).CheckConfig(path) }

// CheckConfig is the package's CheckConfig, remembering its verdict until the file changes.
func (g *Guard) CheckConfig(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	stamp := fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
	if prev, ok := g.lookConfig(path); ok && prev.stamp == stamp {
		return prev.err
	}
	err = checkConfig(path)
	g.keepConfig(path, checkResult{stamp, err})
	return err
}

type checkResult struct {
	stamp string
	err   error
}

func checkConfig(path string) error {
	cmd := exec.Command("git", "config", "--file", path, "--no-includes", "--list", "--name-only")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cannot read %s: %s", path, strings.TrimSpace(errb.String()))
	}
	for _, key := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if key = strings.ToLower(strings.TrimSpace(key)); key != "" && !safeKey(key) {
			return fmt.Errorf("%s sets %s, which Oriel will not run host git with in a sandboxed workspace", path, key)
		}
	}
	return nil
}

func safeKey(key string) bool {
	if safeKeys[key] {
		return true
	}
	for _, p := range safePrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	for prefix, ok := range map[string]map[string]bool{"branch.": safeBranchKeys, "remote.": safeRemoteKeys} {
		if strings.HasPrefix(key, prefix) {
			i := strings.LastIndex(key, ".")
			return ok[key[i+1:]]
		}
	}
	return false
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// Repo is dir as gitx runs git in it, with GitEnv applied: host git in a sandboxed workspace goes through here, or
// through GitEnv.
func Repo(dir string) (gitx.Repo, error) { return (*Guard)(nil).Repo(dir) }

// Repo is the package's Repo, vetting through g.
func (g *Guard) Repo(dir string) (gitx.Repo, error) {
	env, err := g.GitEnv(dir)
	if err != nil {
		return gitx.Repo{}, err
	}
	return gitx.Repo{Dir: dir, Env: env}, nil
}

// Git runs host git in dir with GitEnv applied, as gitx runs it, returning what it printed.
func Git(ctx context.Context, dir string, args ...string) (string, error) {
	return (*Guard)(nil).Git(ctx, dir, args...)
}

// Git is the package's Git, vetting through g.
func (g *Guard) Git(ctx context.Context, dir string, args ...string) (string, error) {
	r, err := g.Repo(dir)
	if err != nil {
		return "", err
	}
	return r.Run(ctx, args...)
}
