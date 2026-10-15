package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/plugin"
)

// defaultKey is liamg/oriel-plugins' public key, embedded in the binary (catalog.md, "More than one registry"); a
// build can set it with -ldflags "-X github.com/liamg/oriel/catalog.defaultKey=ed25519:…".
var defaultKey = ""

// DefaultKey is the default registry's key; nil until one is embedded, which leaves the default registry refused.
func DefaultKey() PublicKey {
	k, err := ParseKey(defaultKey)
	if err != nil {
		return nil
	}
	return k
}

// Source is one registry: its repo and the key its index must be signed with.
type Source struct {
	Repo string // owner/name
	Key  PublicKey
}

// Client fetches, verifies and caches each registry's index (catalog.md, The index). A fetch that fails, or an
// index whose signature doesn't verify, leaves the last verified one cached in use.
type Client struct {
	Sources []Source
	// Fetch reads one asset of a registry's newest release: index.json and index.sig.
	Fetch func(ctx context.Context, repo, name string) ([]byte, error)
	Cache string // where verified indexes are kept: <Cache>/<owner>/<name>/

	mu      sync.Mutex
	indexes map[string]Index // by repo
	errs    map[string]error // why the last fetch of a repo's index failed
	fetched time.Time
}

// Refresh fetches every registry's index, and says what went wrong with each that failed.
func (c *Client) Refresh(ctx context.Context) error {
	var errs []error
	for _, s := range c.Sources {
		ix, err := c.fetch(ctx, s)
		c.mu.Lock()
		if c.indexes == nil {
			c.indexes, c.errs = map[string]Index{}, map[string]error{}
		}
		if err != nil {
			c.errs[s.Repo] = err
			errs = append(errs, fmt.Errorf("%s: %w", s.Repo, err))
			if _, ok := c.indexes[s.Repo]; !ok {
				if cached, cerr := c.cached(s); cerr == nil {
					c.indexes[s.Repo] = cached
				}
			}
		} else {
			delete(c.errs, s.Repo)
			c.indexes[s.Repo] = ix
		}
		c.fetched = time.Now()
		c.mu.Unlock()
	}
	return errors.Join(errs...)
}

func (c *Client) fetch(ctx context.Context, s Source) (Index, error) {
	if len(s.Key) == 0 {
		return Index{}, errors.New("no key to check its index with: give it one in [registry] keys")
	}
	body, err := c.Fetch(ctx, s.Repo, IndexFile)
	if err != nil {
		return Index{}, err
	}
	sig, err := c.Fetch(ctx, s.Repo, IndexSig)
	if err != nil {
		return Index{}, err
	}
	ix, err := VerifyIndex(body, sig, []PublicKey{s.Key})
	if err != nil {
		return Index{}, err
	}
	if c.Cache != "" {
		dir := filepath.Join(c.Cache, filepath.FromSlash(s.Repo))
		if err := os.MkdirAll(dir, 0o755); err == nil {
			os.WriteFile(filepath.Join(dir, IndexFile), body, 0o644)
			os.WriteFile(filepath.Join(dir, IndexSig), sig, 0o644)
		}
	}
	return ix, nil
}

// cached is the index last fetched for s, verified again.
func (c *Client) cached(s Source) (Index, error) {
	if c.Cache == "" {
		return Index{}, errors.New("no cache")
	}
	dir := filepath.Join(c.Cache, filepath.FromSlash(s.Repo))
	body, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		return Index{}, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, IndexSig))
	if err != nil {
		return Index{}, err
	}
	return VerifyIndex(body, sig, []PublicKey{s.Key})
}

// Err is why repo's index couldn't be fetched last time; nil when it was.
func (c *Client) Err(repo string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errs[repo]
}

// Found is a listing and the registry it's from, with the others that list it too.
type Found struct {
	Listing
	Registry string
	Also     []string
}

// Find is id's listing in the first registry, in Sources' order, that has it.
func (c *Client) Find(id string) (Found, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var f Found
	ok := false
	for _, s := range c.Sources {
		l, has := c.indexes[s.Repo].Plugin(id)
		if !has {
			continue
		}
		if !ok {
			f, ok = Found{Listing: l, Registry: s.Repo}, true
		} else {
			f.Also = append(f.Also, s.Repo)
		}
	}
	return f, ok
}

// All is every plugin the registries list, each from the first that has it, sorted by id.
func (c *Client) All() []Found {
	c.mu.Lock()
	ids := map[string]bool{}
	for _, s := range c.Sources {
		for _, l := range c.indexes[s.Repo].Plugins {
			ids[l.ID] = true
		}
	}
	c.mu.Unlock()
	var out []Found
	for _, id := range sortedKeys(ids) {
		if f, ok := c.Find(id); ok {
			out = append(out, f)
		}
	}
	return out
}

// RegistryResolver resolves registry entries against a Client (catalog.md, Updates): reviewed versions in the range, or a
// trusted plugin's tags once trusted_hold has passed; yanked versions aren't offered, and a revoked one is
// refused. What the lockfile pins stays unless Updates is "auto" or Update asks for the newest.
type RegistryResolver struct {
	Client *Client
	GitHub interface {
		Tags(ctx context.Context, repo string) (map[string]string, error)
		CommitTime(ctx context.Context, repo, commit string) (time.Time, error)
		Asset(ctx context.Context, repo, tag, name string) ([]byte, error)
	}
	Hold     time.Duration // how long a trusted tag waits before it's offered
	Updates  string        // auto, ask or manual
	Platform string        // os/arch
	Now      func() time.Time
	Temp     string // where archives are downloaded

	mu     sync.Mutex
	update map[string]bool // ids to take the newest of at the next sync
}

// Update takes id's newest version at the next sync, as an update the user asked for.
func (r *RegistryResolver) Update(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.update == nil {
		r.update = map[string]bool{}
	}
	r.update[id] = true
}

// candidate is a version that may be installed.
type candidate struct {
	v      plugin.Version
	commit string
	badge  string
	entry  Version // a reviewed version's entry
	tag    string
}

// Resolve picks e's version.
func (r *RegistryResolver) Resolve(ctx context.Context, e Entry, locked *Locked) (Resolution, error) {
	f, ok := r.Client.Find(e.ID)
	if !ok {
		return Resolution{}, fmt.Errorf("%s isn't in any registry", e.ID)
	}
	rng, err := plugin.ParseRange(e.Range)
	if err != nil {
		return Resolution{}, err
	}
	source := "registry:" + f.Registry
	if locked != nil {
		if why, revoked := f.Revoked[locked.Version]; revoked {
			return Resolution{Locked: *locked, Revoked: why}, nil
		}
	}
	cands, err := r.candidates(ctx, f)
	if err != nil {
		return Resolution{}, err
	}
	var best *candidate
	for i := range cands {
		c := &cands[i]
		if rng.Match(c.v) && (best == nil || c.v.Compare(best.v) > 0) {
			best = c
		}
	}
	var pick *candidate
	r.mu.Lock()
	wantNewest := r.update[e.ID]
	delete(r.update, e.ID)
	r.mu.Unlock()
	if locked != nil && !wantNewest && r.Updates != "auto" {
		if lv, err := plugin.ParseVersion(locked.Version); err == nil && rng.Match(lv) {
			pick = &candidate{v: lv, commit: locked.Commit, badge: locked.Badge, tag: "v" + locked.Version}
			if ent, ok := f.Version(locked.Version); ok {
				pick.entry = ent
			}
		}
	}
	res := Resolution{Platform: r.Platform}
	if pick == nil {
		if best == nil {
			return Resolution{}, fmt.Errorf("no version of %s matches %s", e.ID, e.Range)
		}
		pick = best
	} else if best != nil && best.v.Compare(pick.v) > 0 {
		res.Update = best.v.String()
	}
	res.Locked = Locked{Source: source, Version: pick.v.String(), Commit: pick.commit, Badge: pick.badge}
	res.Repo = "https://" + strings.TrimPrefix(f.Repo, "https://")
	sum := pick.entry.Archives[r.Platform]
	if sum == "" && locked != nil && locked.Commit == pick.commit {
		sum = locked.Archives[r.Platform]
	}
	if sum != "" {
		res.Locked.Archives = map[string]string{r.Platform: sum}
		res.Facts = append(res.Facts, plugin.Fact{Label: "sha256", Value: strings.TrimPrefix(sum, "sha256:")[:min(12, len(strings.TrimPrefix(sum, "sha256:")))] + " ok"})
	}
	id, version, tag, repo := e.ID, res.Version, pick.tag, f.RepoName()
	res.Fetch = func(ctx context.Context) (string, string, error) {
		b, err := r.GitHub.Asset(ctx, repo, tag, ArchiveName(id, version, r.Platform))
		if err != nil {
			if sum != "" {
				return "", "", fmt.Errorf("%s %s's %s archive: %w", id, version, r.Platform, err)
			}
			return "", "", nil
		}
		got := sha256.Sum256(b)
		have := "sha256:" + hex.EncodeToString(got[:])
		if sum != "" && have != sum {
			return "", "", fmt.Errorf("%s %s's %s archive is %s, not %s as recorded", id, version, r.Platform, have, sum)
		}
		tmp, err := os.CreateTemp(r.Temp, "oriel-plugin-*.tar.gz")
		if err != nil {
			return "", "", err
		}
		_, err = tmp.Write(b)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		return tmp.Name(), have, err
	}
	res.Facts = append([]plugin.Fact{{Label: "pinned", Value: short(pick.commit)}, {Label: "badge", Value: pick.badge}}, res.Facts...)
	switch pick.badge {
	case BadgeReviewed:
		if pick.entry.Reviewed != "" {
			res.Facts = append(res.Facts, plugin.Fact{Label: "reviewed", Value: prNumber(pick.entry.Reviewed)})
		}
		if pick.entry.Built == "author" {
			res.Facts = append(res.Facts, plugin.Fact{Label: "built", Value: "by the author, not reproduced"})
		}
	case BadgeTrusted:
		res.Facts = append(res.Facts, plugin.Fact{Label: "author", Value: "trusted since " + f.Since})
	}
	res.Facts = append(res.Facts, plugin.Fact{Label: "registry", Value: f.Registry})
	return res, nil
}

// candidates are the versions f offers: its reviewed versions, and a trusted plugin's tags that have waited out the
// hold. Yanked and revoked versions aren't.
func (r *RegistryResolver) candidates(ctx context.Context, f Found) ([]candidate, error) {
	var out []candidate
	seen := map[string]bool{}
	for _, v := range f.Versions {
		pv, err := plugin.ParseVersion(v.Version)
		if err != nil || v.Yanked != "" || f.Revoked[v.Version] != "" {
			continue
		}
		seen[pv.String()] = true
		out = append(out, candidate{v: pv, commit: v.Commit, badge: BadgeReviewed, entry: v, tag: "v" + v.Version})
	}
	if !f.Trusted {
		return out, nil
	}
	tags, err := r.GitHub.Tags(ctx, f.RepoName())
	if err != nil {
		return out, fmt.Errorf("%s's tags: %w", f.ID, err)
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	for tag, commit := range tags {
		pv, err := plugin.ParseVersion(tag)
		if err != nil || seen[pv.String()] || f.Revoked[pv.String()] != "" {
			continue
		}
		at, err := r.GitHub.CommitTime(ctx, f.RepoName(), commit)
		if err != nil || now.Sub(at) < r.Hold {
			continue
		}
		out = append(out, candidate{v: pv, commit: commit, badge: BadgeTrusted, tag: tag})
	}
	return out, nil
}

func prNumber(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 {
		return "#" + url[i+1:]
	}
	return url
}
