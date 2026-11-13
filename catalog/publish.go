package catalog

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
)

// Registrar is what publishing needs of the registry repo on GitHub.
type Registrar interface {
	// Listing reads plugins/<id>/plugin.toml from the registry's default branch; ok is false when it isn't there.
	Listing(ctx context.Context, registry, id string) (src []byte, ok bool, err error)
	// OpenPR forks the registry, puts file at path on a new branch, and opens a PR from it. file is asked for again
	// with the PR's URL once it's open, and pushed again if that changes it.
	OpenPR(ctx context.Context, registry, branch, path, title, body string, file func(pr string) []byte) (url string, err error)
}

// PublishOpts shape oriel plugin publish.
type PublishOpts struct {
	Dir       string // the plugin's repository
	Registry  string // DefaultRepo if ""
	Registrar Registrar
	Go        string
}

// Published is what publish did.
type Published struct {
	ID, Version, Commit string
	PR                  string // the PR it opened; "" for a trusted plugin
	Note                string // why it opened none
	File                []byte // the plugin.toml it proposed
}

// Publish proposes the plugin in o.Dir's current tag to the registry: it reads oriel.toml
// and the tag at HEAD, packs it as the registry's CI will, and opens a PR that adds a [[versions]] entry, or the
// plugin.toml of a new plugin. A trusted plugin has nothing to publish: its tag is checked and Oriel follows it.
func Publish(ctx context.Context, o PublishOpts) (Published, error) {
	reg := cmp.Or(o.Registry, DefaultRepo)
	m, err := plugin.LoadManifest(o.Dir)
	if err != nil {
		return Published{}, err
	}
	if m.Version == "" {
		return Published{}, errors.New("oriel.toml has no version")
	}
	r := gitx.Repo{Dir: o.Dir}
	commit, err := r.Head(ctx)
	if err != nil {
		return Published{}, err
	}
	out := Published{ID: m.ID, Version: m.Version, Commit: commit}
	tag := "v" + m.Version
	if c, err := r.Run(ctx, "rev-parse", "--verify", "-q", tag+"^{commit}"); err != nil || strings.TrimSpace(c) != commit {
		return out, fmt.Errorf("HEAD isn't tagged %s: tag the version you publish (git tag %s && git push origin %s)", tag, tag, tag)
	}
	if dirty, err := r.Dirty(ctx); err != nil {
		return out, err
	} else if dirty {
		return out, errors.New("the work tree has changes: publish exactly what's tagged")
	}
	repo, ok := githubRepo(r.RemoteURL(ctx))
	if !ok {
		return out, errors.New("origin isn't a GitHub repository")
	}
	if owner, _, _ := strings.Cut(m.ID, "/"); !strings.EqualFold(owner, strings.Split(repo, "/")[0]) {
		return out, fmt.Errorf("%s must be published from a repository of %s's, not %s", m.ID, owner, repo)
	}
	if remote, err := LsRemoteTag(ctx, o.Dir, "origin", tag); err != nil || remote != commit {
		return out, fmt.Errorf("origin's %s isn't HEAD: push the tag (git push origin %s)", tag, tag)
	}
	src, exists, err := o.Registrar.Listing(ctx, reg, m.ID)
	if err != nil {
		return out, err
	}
	var old Listing
	if exists {
		if old, err = ParseListing(m.ID, src); err != nil {
			return out, err
		}
		if old.Trusted {
			out.Note = fmt.Sprintf("%s is trusted: Oriel follows its tags, so %s needs no PR. It's offered once trusted_hold has passed.", m.ID, tag)
			return out, nil
		}
		if _, ok := old.Version(m.Version); ok {
			return out, fmt.Errorf("%s %s is in the registry already", m.ID, m.Version)
		}
	}
	tmp, err := os.MkdirTemp("", "oriel-publish-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	packed, err := Pack(ctx, o.Dir, PackOpts{Out: tmp, Go: o.Go})
	if err != nil {
		return out, fmt.Errorf("pack: %w", err)
	}
	built := ""
	archives := map[string]string{}
	if len(m.Binaries) > 0 {
		built = "author"
		if _, err := os.Stat(filepath.Join(o.Dir, "go.mod")); err == nil {
			built = "ci"
		}
		for _, a := range packed.Archives {
			archives[a.Platform] = "sha256:" + a.SHA256
		}
	}
	file := func(pr string) []byte {
		entry := versionEntry(m.Version, commit, pr, archives, built)
		if !exists {
			return []byte(fmt.Sprintf("repo        = %q\ndescription = %q\n\n%s", "github.com/"+repo, m.Description, entry))
		}
		return insertVersion(src, entry)
	}
	title := fmt.Sprintf("%s %s", m.ID, m.Version)
	body := fmt.Sprintf("Publishes %s %s, tagged %s at %s in https://github.com/%s.\n\nOpened by `oriel plugin publish`.\n", m.ID, m.Version, tag, commit, repo)
	if !exists {
		title = "New plugin: " + title
	}
	path := PluginsDir + "/" + m.ID + "/" + PluginFile
	branch := "publish/" + strings.ReplaceAll(m.ID, "/", "-") + "-" + m.Version
	url, err := o.Registrar.OpenPR(ctx, reg, branch, path, title, body, file)
	if err != nil {
		return out, err
	}
	out.PR, out.File = url, file(url)
	return out, nil
}

// versionEntry is a [[versions]] table for plugin.toml.
func versionEntry(version, commit, pr string, archives map[string]string, built string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[[versions]]\nversion  = %q\ncommit   = %q\n", version, commit)
	if pr != "" {
		fmt.Fprintf(&b, "reviewed = %q\n", pr)
	}
	if len(archives) > 0 {
		var parts []string
		for _, p := range slices.Sorted(maps.Keys(archives)) {
			parts = append(parts, fmt.Sprintf("%q = %q", p, archives[p]))
		}
		fmt.Fprintf(&b, "archives = { %s }\n", strings.Join(parts, ", "))
	}
	if built != "" {
		fmt.Fprintf(&b, "built    = %q\n", built)
	}
	return b.String()
}

var tableHeader = regexp.MustCompile(`(?m)^[ \t]*\[\[versions\]\]|^[ \t]*\[revoked\]`)

// insertVersion puts a new [[versions]] entry first among a plugin.toml's versions, newest first, or at its end.
func insertVersion(src []byte, entry string) []byte {
	s := string(src)
	if loc := tableHeader.FindStringIndex(s); loc != nil {
		return []byte(s[:loc[0]] + entry + "\n" + s[loc[0]:])
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return []byte(s + "\n" + entry)
}

var ghRemote = regexp.MustCompile(`github\.com[:/]([^/]+/[^/]+?)(\.git)?/?$`)

// githubRepo is owner/name of a GitHub remote URL.
func githubRepo(url string) (string, bool) {
	m := ghRemote.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// GH is the Registrar through the gh CLI: the user's own login, as Homebrew's bump-formula-pr works.
type GH struct{}

func (GH) gh(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g GH) Listing(ctx context.Context, registry, id string) ([]byte, bool, error) {
	out, err := g.gh(ctx, "", "api", "-H", "Accept: application/vnd.github.raw", "repos/"+registry+"/contents/"+PluginsDir+"/"+id+"/"+PluginFile)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, false, nil
		}
		return nil, false, err
	}
	return []byte(out + "\n"), true, nil
}

func (g GH) OpenPR(ctx context.Context, registry, branch, path, title, body string, file func(pr string) []byte) (string, error) {
	user, err := g.gh(ctx, "", "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	if _, err := g.gh(ctx, "", "repo", "fork", registry, "--clone=false"); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "oriel-registry-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	_, name, _ := strings.Cut(registry, "/")
	if _, err := g.gh(ctx, "", "repo", "clone", user+"/"+name, tmp, "--", "--depth", "1"); err != nil {
		return "", err
	}
	r := gitx.Repo{Dir: tmp}
	if _, err := r.Run(ctx, "fetch", "--depth", "1", "upstream"); err != nil {
		return "", err
	}
	def := r.DefaultBranch(ctx)
	if _, err := r.Run(ctx, "checkout", "-q", "-B", branch, "upstream/"+cmp.Or(def, "main")); err != nil {
		return "", err
	}
	write := func(b []byte, msg string) error {
		p := filepath.Join(tmp, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
		if _, err := r.Run(ctx, "add", path); err != nil {
			return err
		}
		if _, err := r.Run(ctx, "commit", "-q", "-m", msg); err != nil {
			return err
		}
		_, err := r.Run(ctx, "push", "-q", "--force", "origin", branch)
		return err
	}
	first := file("")
	if err := write(first, title); err != nil {
		return "", err
	}
	url, err := g.gh(ctx, tmp, "pr", "create", "--repo", registry, "--head", user+":"+branch, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	if final := file(url); string(final) != string(first) {
		if err := write(final, "Record the review PR"); err != nil {
			return url, err
		}
	}
	return url, nil
}
