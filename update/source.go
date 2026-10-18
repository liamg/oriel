package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The files of a release besides its artifacts.
const (
	ManifestFile  = "manifest.json"
	SignatureFile = "manifest.sig"
)

// Source is where the app finds releases.
type Source interface {
	// Latest is the newest release on channel.
	Latest(ctx context.Context, channel string) (Release, error)
	// Open reads one file of the release Latest last returned.
	Open(ctx context.Context, name string) (io.ReadCloser, error)
}

// Dir is a release laid out in a directory: manifest.json, manifest.sig and the artifacts.
type Dir string

func (d Dir) Latest(context.Context, string) (Release, error) {
	m, err := os.ReadFile(filepath.Join(string(d), ManifestFile))
	if err != nil {
		return Release{}, err
	}
	sig, err := os.ReadFile(filepath.Join(string(d), SignatureFile))
	if err != nil {
		return Release{}, err
	}
	return Release{Manifest: m, Signature: sig}, nil
}

func (d Dir) Open(_ context.Context, name string) (io.ReadCloser, error) {
	if !filepath.IsLocal(name) {
		return nil, fmt.Errorf("bad artifact name %q", name)
	}
	return os.Open(filepath.Join(string(d), name))
}

// Write lays rel out in dir, with its artifacts' bytes by name: what CI publishes.
func Write(dir string, rel Release, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	all := map[string][]byte{ManifestFile: rel.Manifest, SignatureFile: rel.Signature}
	maps.Copy(all, files)
	for name, b := range all {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// GitHub is releases of a GitHub repo, a private one read with the user's token.
type GitHub struct {
	Repo   string // owner/name
	Base   string // https://api.github.com/ if ""
	Token  func(ctx context.Context) (string, error)
	Client *http.Client // http.DefaultClient if nil

	mu     sync.Mutex
	assets map[string]string // the last release's asset API URLs, by name
}

type ghRelease struct {
	Draft      bool `json:"draft"`
	Prerelease bool `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
}

// Latest is the newest release with a manifest: a pre-release counts only on the nightly channel.
func (g *GitHub) Latest(ctx context.Context, channel string) (Release, error) {
	base := g.Base
	if base == "" {
		base = "https://api.github.com/"
	}
	r, err := g.get(ctx, strings.TrimSuffix(base, "/")+"/repos/"+g.Repo+"/releases?per_page=30", "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	defer r.Close()
	var list []ghRelease
	if err := json.NewDecoder(r).Decode(&list); err != nil {
		return Release{}, err
	}
	for _, rel := range list {
		if rel.Draft || rel.Prerelease && channel != Nightly {
			continue
		}
		assets := map[string]string{}
		for _, a := range rel.Assets {
			assets[a.Name] = a.URL
		}
		if assets[ManifestFile] == "" || assets[SignatureFile] == "" {
			continue
		}
		g.mu.Lock()
		g.assets = assets
		g.mu.Unlock()
		m, err := g.read(ctx, ManifestFile)
		if err != nil {
			return Release{}, err
		}
		sig, err := g.read(ctx, SignatureFile)
		if err != nil {
			return Release{}, err
		}
		return Release{Manifest: m, Signature: sig}, nil
	}
	return Release{}, fmt.Errorf("%s has no %s release", g.Repo, channel)
}

func (g *GitHub) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	g.mu.Lock()
	url := g.assets[name]
	g.mu.Unlock()
	if url == "" {
		return nil, fmt.Errorf("the release has no %s", name)
	}
	return g.get(ctx, url, "application/octet-stream")
}

func (g *GitHub) read(ctx context.Context, name string) ([]byte, error) {
	r, err := g.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 1<<20))
}

func (g *GitHub) get(ctx context.Context, url, accept string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if g.Token != nil {
		tok, err := g.Token(ctx)
		if err != nil {
			return nil, err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	c := g.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub: %s (HTTP %d)", strings.TrimSpace(string(b)), resp.StatusCode)
	}
	return resp.Body, nil
}

// Cache keeps each artifact the app downloads, checked against the manifest.
type Cache struct {
	Dir    string
	Source Source
}

// Open reads a from the cache, downloading it first if it isn't there yet.
func (c Cache) Open(ctx context.Context, m Manifest, a Artifact) (io.ReadCloser, error) {
	if !filepath.IsLocal(a.Name) {
		return nil, fmt.Errorf("bad artifact name %q", a.Name)
	}
	path := filepath.Join(c.Dir, m.Version, a.Name)
	if f, err := os.Open(path); err == nil {
		if _, err := readChecked(f, a); err == nil {
			_, _ = f.Seek(0, io.SeekStart)
			return f, nil
		}
		f.Close()
	}
	r, err := c.Source.Open(ctx, a.Name)
	if err != nil {
		return nil, err
	}
	body, err := readChecked(r, a)
	r.Close()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path+".tmp", body, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return nil, err
	}
	return os.Open(path)
}

// ErrUpToDate is Check finding nothing newer.
var ErrUpToDate = errors.New("already up to date")

// Check is the newest verified release on channel newer than current.
func Check(ctx context.Context, src Source, keys []ed25519.PublicKey, channel, current string) (Release, Manifest, error) {
	rel, err := src.Latest(ctx, channel)
	if err != nil {
		return Release{}, Manifest{}, err
	}
	m, err := rel.Verify(keys)
	if err != nil {
		return Release{}, Manifest{}, err
	}
	if !Newer(m.Version, current) {
		return Release{}, Manifest{}, ErrUpToDate
	}
	return rel, m, nil
}
