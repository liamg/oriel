package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/tools/depcheck"
)

// Org answers whether a GitHub user may publish for an owner: the owner, or a member of the org.
type Org interface {
	Member(ctx context.Context, org, user string) (bool, error)
}

// PRCheck is the registry's CI on a pull request. Its fields that
// reach outside are funcs, so a test can stand in for GitHub.
type PRCheck struct {
	Base, Head string // registry checkouts: the PR's base and its head
	Author     string // who opened the PR
	Org        Org
	Allow      depcheck.Allowlist // Oriel's tools/depcheck/allowed.txt
	Go         string             // the go command; "go" if ""
	// Fetch checks a plugin's repository out at a commit, into a directory of its own. Git over https if nil.
	Fetch func(ctx context.Context, repo, commit string) (string, error)
	// Tag is the commit a tag points at. git ls-remote if nil.
	Tag func(ctx context.Context, repo, tag string) (string, error)
	// Asset reads a release asset of the plugin's repo, for archives the author built; unchecked if nil.
	Asset func(ctx context.Context, repo, tag, name string) ([]byte, error)
}

// PRReport is what the check found: the problems that fail it, and the comment it posts.
type PRReport struct {
	Problems []Problem
	Comment  string
}

// Run checks every plugin.toml the PR changes.
func (c PRCheck) Run(ctx context.Context) (PRReport, error) {
	base, err := ReadListings(c.Base)
	if err != nil {
		return PRReport{}, fmt.Errorf("the base: %w", err)
	}
	head, err := ReadListings(c.Head)
	if err != nil {
		return PRReport{}, err
	}
	var rep PRReport
	var comment strings.Builder
	for _, id := range sortedKeys(head) {
		h := head[id]
		b, had := base[id]
		if had && sameListing(b, h) {
			continue
		}
		var prev *Listing
		if had {
			prev = &b
		}
		probs, section := c.listing(ctx, id, prev, h)
		rep.Problems = append(rep.Problems, probs...)
		comment.WriteString(section)
	}
	for _, id := range sortedKeys(base) {
		if _, ok := head[id]; !ok {
			fmt.Fprintf(&comment, "## %s\n\nRemoved from the registry: it's no longer offered, and installs of it stop resolving.\n\n", id)
		}
	}
	if len(rep.Problems) > 0 {
		comment.WriteString("### Problems\n\n")
		for _, p := range rep.Problems {
			fmt.Fprintf(&comment, "- %s\n", p)
		}
		comment.WriteString("\n")
	}
	if comment.Len() == 0 {
		comment.WriteString("No plugin.toml changes.\n")
	}
	rep.Comment = comment.String()
	return rep, nil
}

func sameListing(a, b Listing) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// listing checks one changed plugin.toml, and writes its part of the comment.
func (c PRCheck) listing(ctx context.Context, id string, prev *Listing, l Listing) ([]Problem, string) {
	file := PluginsDir + "/" + id + "/" + PluginFile
	var probs []Problem
	fail := func(format string, a ...any) {
		probs = append(probs, Problem{File: file, Text: fmt.Sprintf(format, a...)})
	}
	var s strings.Builder
	owner, _, _ := strings.Cut(id, "/")
	if !strings.EqualFold(owner, l.Owner()) {
		fail("the path's owner %s isn't the owner of %s", owner, l.Repo)
	}
	switch ok, err := c.mayPublish(ctx, owner); {
	case err != nil:
		fail("can't check that %s may publish for %s: %v", c.Author, owner, err)
	case !ok:
		fail("%s isn't %s or a member of it, so can't publish %s/*", c.Author, owner, owner)
	}
	if prev != nil && prev.Trusted {
		for v := range l.Revoked {
			if _, was := prev.Revoked[v]; !was && l.Trusted {
				fail("revoking %s of a trusted plugin needs trusted removed in the same PR", v)
			}
		}
	}
	added := addedVersions(prev, l)
	if len(added) == 0 {
		fmt.Fprintf(&s, "## %s\n\n", id)
	}
	for _, v := range added {
		vp, sec := c.version(ctx, id, prev, l, v)
		probs = append(probs, vp...)
		s.WriteString(sec)
	}
	switch {
	case l.Trusted && (prev == nil || !prev.Trusted):
		fmt.Fprintf(&s, "**Trusted from now on**: Oriel will follow %s's semver tags, unreviewed (after `trusted_hold`). Trust is a decision about the author, not one version.\n\n", l.Owner())
	case !l.Trusted && prev != nil && prev.Trusted:
		s.WriteString("**No longer trusted**: only reviewed versions are offered.\n\n")
	}
	for _, v := range sortedKeys(l.Revoked) {
		if prev == nil || prev.Revoked[v] == "" {
			fmt.Fprintf(&s, "**Revoked %s**: %s. It's disabled wherever it's installed at the next index fetch.\n\n", v, l.Revoked[v])
		}
	}
	for _, v := range l.Versions {
		if v.Yanked != "" && (prev == nil || !wasYanked(*prev, v.Version)) {
			fmt.Fprintf(&s, "**Yanked %s**: %s. It stays installed where it is, but isn't offered.\n\n", v.Version, v.Yanked)
		}
	}
	return probs, s.String()
}

func wasYanked(l Listing, version string) bool {
	v, ok := l.Version(version)
	return ok && v.Yanked != ""
}

// addedVersions are the reviewed versions l lists that prev doesn't.
func addedVersions(prev *Listing, l Listing) []Version {
	var out []Version
	for _, v := range l.Versions {
		if v.Yanked != "" {
			continue
		}
		if prev != nil {
			if _, ok := prev.Version(v.Version); ok {
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

func (c PRCheck) mayPublish(ctx context.Context, owner string) (bool, error) {
	if strings.EqualFold(c.Author, owner) {
		return true, nil
	}
	if c.Org == nil {
		return false, nil
	}
	return c.Org.Member(ctx, owner, c.Author)
}

// version checks one new reviewed version: its tag, pack at its commit, its archives and its dependencies, and
// writes its section, which leads with the capability diff since the last reviewed version.
func (c PRCheck) version(ctx context.Context, id string, prev *Listing, l Listing, v Version) ([]Problem, string) {
	file := PluginsDir + "/" + id + "/" + PluginFile
	var probs []Problem
	fail := func(format string, a ...any) {
		probs = append(probs, Problem{File: file, Text: v.Version + ": " + fmt.Sprintf(format, a...)})
	}
	var s strings.Builder
	fmt.Fprintf(&s, "## %s %s\n\n", id, v.Version)
	dir, err := c.fetch(ctx, l.Repo, v.Commit)
	if err != nil {
		fail("can't check out %s at %s: %v", l.Repo, short(v.Commit), err)
		return probs, s.String()
	}
	defer os.RemoveAll(dir)
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		fail("%v", err)
		return probs, s.String()
	}
	var old *plugin.Manifest
	var oldVer Version
	if prev != nil {
		if pv, ok := lastReviewed(*prev, v.Version); ok {
			oldVer = pv
			if od, err := c.fetch(ctx, l.Repo, pv.Commit); err == nil {
				if om, err := plugin.LoadManifest(od); err == nil {
					old = &om
				}
				os.RemoveAll(od)
			}
		}
	}
	s.WriteString(capabilityDiff(m, old, oldVer.Version))
	if old != nil {
		fmt.Fprintf(&s, "[Source diff %s → %s](https://github.com/%s/compare/%s...%s)\n\n", oldVer.Version, v.Version, l.RepoName(), oldVer.Commit, v.Commit)
	} else {
		fmt.Fprintf(&s, "A new plugin: review all of [%s at %s](https://github.com/%s/tree/%s).\n\n", l.RepoName(), short(v.Commit), l.RepoName(), v.Commit)
	}
	var rows [][2]string
	row := func(k, v string) { rows = append(rows, [2]string{k, v}) }
	if m.ID != id {
		fail("its oriel.toml says it's %s", m.ID)
	}
	if m.Version != v.Version {
		fail("its oriel.toml says version %s", m.Version)
	}
	tag, tagged := "", ""
	for _, t := range []string{"v" + v.Version, v.Version} {
		if got, err := c.tag(ctx, l.Repo, t); err == nil && got != "" {
			tag, tagged = t, got
			break
		}
	}
	switch {
	case tag == "":
		fail("%s has no tag v%s", l.Repo, v.Version)
		row("tag", "✗ none")
	case tagged != v.Commit:
		fail("tag %s points at %s, not %s", tag, short(tagged), short(v.Commit))
		row("tag", "✗ "+tag+" → "+short(tagged))
	default:
		row("tag", "✓ "+tag+" → "+short(v.Commit))
	}
	out, err := os.MkdirTemp("", "oriel-registry-")
	if err != nil {
		fail("%v", err)
		return probs, s.String()
	}
	defer os.RemoveAll(out)
	packed, err := Pack(ctx, dir, PackOpts{Out: out, Go: c.Go})
	var pe *PackError
	switch {
	case errors.As(err, &pe):
		for _, p := range pe.Problems {
			fail("pack: %s", p)
		}
		row("pack", "✗")
	case err != nil:
		fail("pack: %v", err)
		row("pack", "✗")
	default:
		row("pack", "✓")
	}
	_, goErr := os.Stat(filepath.Join(dir, "go.mod"))
	isGo := goErr == nil
	if len(m.Binaries) > 0 {
		var notes []string
		for _, plat := range m.Binaries {
			rec := v.Archives[plat]
			if rec == "" {
				fail("no archive for %s", plat)
				continue
			}
			i := slices.IndexFunc(packed.Archives, func(a Archive) bool { return a.Platform == plat })
			switch {
			case isGo && v.Built != "ci":
				fail("a Go plugin's archives are rebuilt and matched: built = \"ci\"")
			case isGo && i >= 0 && "sha256:"+packed.Archives[i].SHA256 != rec:
				fail("%s rebuilt with -trimpath is sha256:%s, not %s", plat, packed.Archives[i].SHA256, rec)
			case isGo && i >= 0:
				notes = append(notes, plat+" rebuilt and matched")
			case !isGo && v.Built != "author":
				fail("archives that can't be rebuilt are recorded as built = \"author\"")
			case !isGo && c.Asset != nil:
				b, err := c.Asset(ctx, l.RepoName(), tag, ArchiveName(id, v.Version, plat))
				if err != nil {
					fail("%s's release archive: %v", plat, err)
					continue
				}
				sum := sha256.Sum256(b)
				if "sha256:"+hex.EncodeToString(sum[:]) != rec {
					fail("%s's release archive doesn't match %s", plat, rec)
					continue
				}
				notes = append(notes, plat+" as the author built it")
			}
		}
		if len(notes) > 0 {
			row("archives", "✓ "+strings.Join(notes, ", "))
		}
		var sizes []string
		for _, a := range packed.Archives {
			sizes = append(sizes, fmt.Sprintf("%s %s", a.Platform, mb(a.Size)))
		}
		if len(sizes) > 0 {
			row("sizes", strings.Join(sizes, " · "))
		}
	} else {
		for _, a := range packed.Archives {
			row("size", mb(a.Size))
		}
	}
	if isGo {
		mods, err := depcheck.Requires(filepath.Join(dir, "go.mod"))
		if err != nil {
			fail("go.mod: %v", err)
		}
		for i := range mods {
			mods[i].From = "go.mod"
		}
		outside := depcheck.Check(c.Allow, mods)
		if len(outside) == 0 {
			row("go deps", fmt.Sprintf("%d · all allowlisted", len(mods)))
		} else {
			row("go deps", fmt.Sprintf("%d · %d outside the allowlist (shown at install)", len(mods), len(outside)))
			for _, o := range outside {
				fmt.Fprintf(&s, "- ⚠ %s\n", strings.SplitN(o, " (", 2)[0])
			}
			s.WriteString("\n")
		}
	}
	s.WriteString("| check | |\n|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&s, "| %s | %s |\n", r[0], r[1])
	}
	s.WriteString("\n")
	return probs, s.String()
}

// lastReviewed is the newest reviewed version before v.
func lastReviewed(l Listing, v string) (Version, bool) {
	want, err := plugin.ParseVersion(v)
	if err != nil {
		return Version{}, false
	}
	var best Version
	var bestV plugin.Version
	found := false
	for _, x := range l.Versions {
		xv, err := plugin.ParseVersion(x.Version)
		if err != nil || x.Yanked != "" || xv.Compare(want) >= 0 {
			continue
		}
		if !found || xv.Compare(bestV) > 0 {
			best, bestV, found = x, xv, true
		}
	}
	return best, found
}

// capabilityDiff is the comment's lead: every capability m asks for, what's new since old marked, what it drops,
// and any gate called out.
func capabilityDiff(m plugin.Manifest, old *plugin.Manifest, oldVersion string) string {
	var s strings.Builder
	c := plugin.Ask(m, old)
	more := 0
	for _, r := range c.Rows {
		if old == nil || len(r.Added) > 0 {
			more++
		}
	}
	switch {
	case old == nil:
		fmt.Fprintf(&s, "**Capabilities**: %d\n\n", len(c.Rows))
	case more == 0 && len(c.Removed) == 0:
		fmt.Fprintf(&s, "**Capabilities**: the same as %s\n\n", oldVersion)
	default:
		fmt.Fprintf(&s, "**Capabilities**: %d more than %s\n\n", more, oldVersion)
	}
	for _, r := range c.Rows {
		mark := ""
		switch {
		case old == nil:
		case r.New():
			mark = " **(new)**"
		case len(r.Added) > 0:
			mark = " **(more: " + strings.Join(r.Added, ", ") + ")**"
		}
		fmt.Fprintf(&s, "- `%s`%s: %s\n", strings.Join(r.Grant.Lock(), "`, `"), mark, r.Explain)
	}
	for _, r := range c.Removed {
		fmt.Fprintf(&s, "- ~~`%s`~~ no longer asked for\n", r)
	}
	for _, r := range c.Rows {
		if r.Cap == plugin.CapGate {
			fmt.Fprintf(&s, "\n> ⚠ **gate**: this plugin can %s.\n", r.Explain)
		}
	}
	s.WriteString("\n")
	return s.String()
}

func (c PRCheck) fetch(ctx context.Context, repo, commit string) (string, error) {
	if c.Fetch != nil {
		return c.Fetch(ctx, repo, commit)
	}
	dir, err := os.MkdirTemp("", "oriel-plugin-")
	if err != nil {
		return "", err
	}
	if err := gitx.Clone(ctx, "https://"+strings.TrimPrefix(repo, "https://"), dir); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if _, err := (gitx.Repo{Dir: dir}).Run(ctx, "checkout", "--quiet", "--detach", commit); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func (c PRCheck) tag(ctx context.Context, repo, tag string) (string, error) {
	if c.Tag != nil {
		return c.Tag(ctx, repo, tag)
	}
	return LsRemoteTag(ctx, ".", "https://"+strings.TrimPrefix(repo, "https://"), tag)
}

// LsRemoteTag is the commit a remote's tag points at, peeled through an annotated tag; "" if there's no such tag.
// remote is a URL, or a remote of the repository in dir.
func LsRemoteTag(ctx context.Context, dir, remote, tag string) (string, error) {
	out, err := (gitx.Repo{Dir: dir}).Run(ctx, "ls-remote", "--tags", remote, "refs/tags/"+tag, "refs/tags/"+tag+"^{}")
	if err != nil {
		return "", err
	}
	commit := ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if strings.HasSuffix(f[1], "^{}") || commit == "" {
			commit = f[0]
		}
	}
	return commit, nil
}
