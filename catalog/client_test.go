package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureRegistry is a registry's published release: its signed index.
type fixtureRegistry struct {
	key   ed25519.PrivateKey
	files map[string][]byte // index.json, index.sig
}

func newFixtureRegistry(t *testing.T, repo string, listings map[string]string) fixtureRegistry {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	files := map[string]string{}
	for id, src := range listings {
		files[filepath.Join(PluginsDir, id, PluginFile)] = src
	}
	ix, err := BuildIndex(writeFiles(t, files), repo, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, sig, err := SignIndex(key, ix)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureRegistry{key: key, files: map[string][]byte{IndexFile: body, IndexSig: sig}}
}

func (f fixtureRegistry) source(repo string) Source {
	return Source{Repo: repo, Key: f.key.Public().(ed25519.PublicKey)}
}

func fixtureClient(t *testing.T, regs map[string]fixtureRegistry, order ...string) *Client {
	c := &Client{Cache: t.TempDir(), Fetch: func(_ context.Context, repo, name string) ([]byte, error) {
		r, ok := regs[repo]
		if !ok {
			return nil, fmt.Errorf("no %s", repo)
		}
		return r.files[name], nil
	}}
	for _, repo := range order {
		c.Sources = append(c.Sources, regs[repo].source(repo))
	}
	return c
}

// fakeTags is a plugin repository's tags, as GitHub lists them, each with its commit's time.
type fakeTags struct {
	tags  map[string]string
	times map[string]time.Time
}

func (f fakeTags) Tags(context.Context, string) (map[string]string, error) { return f.tags, nil }
func (f fakeTags) CommitTime(_ context.Context, _, commit string) (time.Time, error) {
	return f.times[commit], nil
}
func (f fakeTags) Asset(context.Context, string, string, string) ([]byte, error) {
	return nil, errors.New("404 Not Found")
}

var (
	commitA = strings.Repeat("a", 40)
	commitB = strings.Repeat("b", 40)
	commitC = strings.Repeat("c", 40)
)

// PL-21 done when: "an index with a bad signature is refused". catalog.md: "The client fetches and verifies the
// index on launch and then hourly, and caches it."
func TestIndexWithABadSignature(t *testing.T) {
	good := newFixtureRegistry(t, DefaultRepo, map[string]string{"acme/jira": "repo = \"github.com/acme/oriel-jira\"\ntrusted = true\n"})
	c := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: good}, DefaultRepo)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Find("acme/jira"); !ok {
		t.Fatal("the verified index isn't read")
	}
	other := newFixtureRegistry(t, DefaultRepo, map[string]string{"evil/x": "repo = \"github.com/evil/x\"\ntrusted = true\n"})
	forged := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: {key: good.key, files: other.files}}, DefaultRepo)
	forged.Cache = c.Cache
	err := forged.Refresh(context.Background())
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("a forged index: %v, want ErrBadSignature", err)
	}
	if _, ok := forged.Find("evil/x"); ok {
		t.Error("a forged index's plugin is offered")
	}
	if _, ok := forged.Find("acme/jira"); !ok {
		t.Error("the last verified index, cached, isn't kept")
	}
}

// PL-21 done when: "an id in two registries resolves to the first". catalog.md: "An id found in more than one
// registry resolves to the first source listed, and the GUI shows which."
func TestFirstRegistryWins(t *testing.T) {
	ours := newFixtureRegistry(t, "liamg/oriel-plugins", map[string]string{
		"acme/jira": "repo = \"github.com/acme/oriel-jira\"\n\n[[versions]]\nversion = \"0.4.2\"\ncommit = \"" + commitA + "\"\n"})
	theirs := newFixtureRegistry(t, "acme/oriel-plugins", map[string]string{
		"acme/jira": "repo = \"github.com/acme/oriel-jira\"\n\n[[versions]]\nversion = \"9.0.0\"\ncommit = \"" + commitB + "\"\n"})
	regs := map[string]fixtureRegistry{"liamg/oriel-plugins": ours, "acme/oriel-plugins": theirs}
	c := fixtureClient(t, regs, "acme/oriel-plugins", "liamg/oriel-plugins")
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	f, _ := c.Find("acme/jira")
	if f.Registry != "acme/oriel-plugins" || strings.Join(f.Also, ",") != "liamg/oriel-plugins" {
		t.Errorf("found in %s, also %v", f.Registry, f.Also)
	}
	r := &RegistryResolver{Client: c, GitHub: fakeTags{}, Platform: "linux/amd64"}
	res, err := r.Resolve(context.Background(), Entry{ID: "acme/jira", Range: "*"}, nil)
	if err != nil || res.Version != "9.0.0" || res.Source != "registry:acme/oriel-plugins" || res.Badge != BadgeReviewed {
		t.Errorf("resolved %+v, %v", res.Locked, err)
	}
}

// PL-21 done when: "a trusted tag isn't offered until trusted_hold has passed". catalog.md: "A trusted plugin's tag
// counts only after trusted_hold has passed."
func TestTrustedHold(t *testing.T) {
	reg := newFixtureRegistry(t, DefaultRepo, map[string]string{
		"liamg/surround": "repo = \"github.com/liamg/oriel-surround\"\ntrusted = true\nsince = \"2026-10-13\"\n\n[revoked]\n\"0.3.0\" = \"bad\"\n"})
	c := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: reg}, DefaultRepo)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	tags := fakeTags{tags: map[string]string{"v0.2.0": commitA, "v0.3.0": commitB, "v0.4.0": commitC},
		times: map[string]time.Time{commitA: now.Add(-72 * time.Hour), commitB: now.Add(-48 * time.Hour), commitC: now.Add(-time.Hour)}}
	r := &RegistryResolver{Client: c, GitHub: tags, Hold: 24 * time.Hour, Platform: "linux/amd64", Now: func() time.Time { return now }}
	res, err := r.Resolve(context.Background(), Entry{ID: "liamg/surround", Range: "*"}, nil)
	if err != nil || res.Version != "0.2.0" || res.Badge != BadgeTrusted || res.Commit != commitA {
		t.Errorf("an hour-old tag and a revoked one: resolved %+v, %v; want 0.2.0", res.Locked, err)
	}
	r.Now = func() time.Time { return now.Add(24 * time.Hour) }
	if res, _ := r.Resolve(context.Background(), Entry{ID: "liamg/surround", Range: "*"}, nil); res.Version != "0.4.0" {
		t.Errorf("after the hold: %s, want 0.4.0", res.Version)
	}
}

// catalog.md: "Yanked: stays installed, but isn't offered for new installs." "Revoked: disabled at the next index
// fetch". Updates follow [registry] updates: with ask, the locked version stays and the newer one is offered.
func TestYankRevokeAndUpdates(t *testing.T) {
	reg := newFixtureRegistry(t, DefaultRepo, map[string]string{"acme/jira": `repo = "github.com/acme/oriel-jira"

[[versions]]
version = "0.4.2"
commit = "` + commitC + `"

[[versions]]
version = "0.4.1"
yanked = "crashes on Linux"

[[versions]]
version = "0.4.0"
commit = "` + commitA + `"

[revoked]
"0.3.0" = "exfiltrated tokens"
`})
	c := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: reg}, DefaultRepo)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := &RegistryResolver{Client: c, GitHub: fakeTags{}, Platform: "linux/amd64", Updates: "ask"}
	e := Entry{ID: "acme/jira", Range: "^0.4"}
	if res, _ := r.Resolve(context.Background(), Entry{ID: "acme/jira", Range: "~0.4.1 <0.4.2"}, nil); res.Version != "" {
		t.Errorf("only a yanked version in range resolved to %s", res.Version)
	}
	res, err := r.Resolve(context.Background(), e, &Locked{Version: "0.4.0", Commit: commitA, Badge: BadgeReviewed})
	if err != nil || res.Version != "0.4.0" || res.Update != "0.4.2" {
		t.Errorf("updates = ask: %+v (update %q), %v", res.Locked, res.Update, err)
	}
	r.Updates = "auto"
	if res, _ := r.Resolve(context.Background(), e, &Locked{Version: "0.4.0", Commit: commitA}); res.Version != "0.4.2" {
		t.Errorf("updates = auto: %s", res.Version)
	}
	res, _ = r.Resolve(context.Background(), Entry{ID: "acme/jira", Range: "*"}, &Locked{Version: "0.3.0", Commit: commitB})
	if res.Revoked != "exfiltrated tokens" {
		t.Errorf("a revoked version: %+v", res)
	}
}

// The default registry's key is embedded, so its index is accepted out of the box.
func TestDefaultKeyParses(t *testing.T) {
	if DefaultKey() == nil {
		t.Fatalf("defaultKey %q doesn't parse", defaultKey)
	}
}
