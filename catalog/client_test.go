package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// An index with a bad signature is refused. The client fetches and verifies the index on launch and then hourly,
// and caches it.
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

// An id found in more than one registry resolves to the first source listed, and the GUI shows which.
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

// A trusted plugin's tag isn't offered until trusted_hold has passed.
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

// "Yanked: stays installed, but isn't offered for new installs." "Revoked: disabled at the next index
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

// assetTags is fakeTags whose releases have every archive asked for.
type assetTags struct {
	fakeTags
	asked *int
}

func (a assetTags) Asset(context.Context, string, string, string) ([]byte, error) {
	*a.asked++
	return []byte("an archive"), nil
}

// The archive is "checked against the index's sha256. Without an archive, the repo is cloned at
// the commit." One with no sha256 to check it against isn't used: the commit is cloned instead.
func TestArchiveWithNoSha256IsNotUsed(t *testing.T) {
	reg := newFixtureRegistry(t, DefaultRepo, map[string]string{
		"acme/jira": "repo = \"github.com/acme/oriel-jira\"\n\n[[versions]]\nversion = \"0.4.2\"\ncommit = \"" + commitA + "\"\n"})
	c := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: reg}, DefaultRepo)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	asked := 0
	r := &RegistryResolver{Client: c, GitHub: assetTags{asked: &asked}, Platform: "linux/amd64", Temp: t.TempDir()}
	res, err := r.Resolve(context.Background(), Entry{ID: "acme/jira", Range: "*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	file, sum, err := res.Fetch(context.Background())
	if file != "" || sum != "" || err != nil || asked != 0 {
		t.Errorf("an archive with no sha256: %q %q %v, %d downloads", file, sum, err, asked)
	}
}

// signed is an index for registry, built at built, signed with key.
func signed(t *testing.T, key ed25519.PrivateKey, registry string, built time.Time, listings map[string]string) fixtureRegistry {
	t.Helper()
	files := map[string]string{}
	for id, src := range listings {
		files[filepath.Join(PluginsDir, id, PluginFile)] = src
	}
	ix, err := BuildIndex(writeFiles(t, files), registry, built)
	if err != nil {
		t.Fatal(err)
	}
	body, sig, err := SignIndex(key, ix)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureRegistry{key: key, files: map[string][]byte{IndexFile: body, IndexSig: sig}}
}

// An index signed with the right key is still refused when it's an older one than was last seen (a replay that
// would bring back a revoked version), or another registry's.
func TestIndexReplayIsRefused(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	newer := signed(t, key, DefaultRepo, now, map[string]string{"acme/jira": "repo = \"github.com/acme/oriel-jira\"\n\n[revoked]\n\"0.3.0\" = \"bad\"\n"})
	older := signed(t, key, DefaultRepo, now.Add(-time.Hour), map[string]string{"acme/jira": "repo = \"github.com/acme/oriel-jira\"\n"})
	elsewhere := signed(t, key, "acme/oriel-plugins", now.Add(time.Hour), map[string]string{"evil/x": "repo = \"github.com/evil/x\"\n"})

	c := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: newer}, DefaultRepo)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Each launch starts a new client: what was last seen is kept with the cache.
	for name, reg := range map[string]fixtureRegistry{"an older index": older, "another registry's index": elsewhere} {
		again := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: reg}, DefaultRepo)
		again.Cache = c.Cache
		if err := again.Refresh(context.Background()); err == nil {
			t.Errorf("%s is accepted", name)
		}
		if f, ok := again.Find("acme/jira"); !ok || f.Revoked["0.3.0"] != "bad" {
			t.Errorf("%s: the last index seen isn't kept: %+v", name, f)
		}
		if _, ok := again.Find("evil/x"); ok {
			t.Errorf("%s: its plugin is offered", name)
		}
	}
	same := fixtureClient(t, map[string]fixtureRegistry{DefaultRepo: newer}, DefaultRepo)
	same.Cache = c.Cache
	if err := same.Refresh(context.Background()); err != nil {
		t.Errorf("the same index again: %v", err)
	}
}

// A missing or empty index reads as "No plugins in the registry yet", never as an error toast or a log warning. A registry with no release, no index in its releases, or an index listing nothing, is
// empty, not failing.
func TestEmptyRegistryIsNotAnError(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	empty := signed(t, key, DefaultRepo, time.Now(), nil)
	for name, h := range map[string]http.HandlerFunc{
		"no repo or no release yet": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"no releases":               func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("[]")) },
		"a release with no index": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`[{"tag_name":"v1","assets":[{"name":"notes.txt","url":"/x"}]}]`))
		},
		"an empty index.json": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a" {
				return
			}
			w.Write([]byte(`[{"tag_name":"v1","assets":[{"name":"index.json","url":"` + "http://" + r.Host + `/a"}]}]`))
		},
		"an index listing nothing": func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/i":
				w.Write(empty.files[IndexFile])
			case "/s":
				w.Write(empty.files[IndexSig])
			default:
				w.Write([]byte(`[{"tag_name":"v1","assets":[{"name":"index.json","url":"http://` + r.Host + `/i"},{"name":"index.sig","url":"http://` + r.Host + `/s"}]}]`))
			}
		},
	} {
		srv := httptest.NewServer(h)
		c := &Client{Sources: []Source{{Repo: DefaultRepo, Key: key.Public().(ed25519.PublicKey)}}, Cache: t.TempDir(),
			Fetch: GitHub{Base: srv.URL}.LatestAsset}
		if err := c.Refresh(context.Background()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if err := c.Err(DefaultRepo); err != nil || len(c.All()) != 0 {
			t.Errorf("%s: Err %v, %d plugins", name, err, len(c.All()))
		}
		srv.Close()
	}
}
