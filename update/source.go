package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/internal/atomicfile"
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

// GitHub is releases of a GitHub repo: a public one read without a token, a private one with the user's.
type GitHub struct {
	Repo   string // owner/name
	Base   string // https://api.github.com/ if ""
	Token  func(ctx context.Context) (string, error)
	Client *http.Client // http.DefaultClient if nil

	mu     sync.Mutex
	assets map[string]gh.Asset // the last release's, by name
}

func (g *GitHub) rest() gh.REST { return gh.REST{Base: g.Base, Token: g.Token, Client: g.Client} }

// Latest is the newest release with a manifest: a pre-release counts only on the nightly channel.
func (g *GitHub) Latest(ctx context.Context, channel string) (Release, error) {
	list, err := g.rest().Releases(ctx, g.Repo, 30)
	if err != nil {
		return Release{}, g.private(ctx, err)
	}
	for _, rel := range list {
		if rel.Draft || rel.Prerelease && channel != Nightly {
			continue
		}
		_, m := rel.Asset(ManifestFile)
		_, sig := rel.Asset(SignatureFile)
		if !m || !sig {
			continue
		}
		assets := map[string]gh.Asset{}
		for _, a := range rel.Assets {
			assets[a.Name] = a
		}
		g.mu.Lock()
		g.assets = assets
		g.mu.Unlock()
		man, err := g.read(ctx, ManifestFile)
		if err != nil {
			return Release{}, err
		}
		signature, err := g.read(ctx, SignatureFile)
		if err != nil {
			return Release{}, err
		}
		return Release{Manifest: man, Signature: signature}, nil
	}
	return Release{}, fmt.Errorf("%s has no %s release", g.Repo, channel)
}

func (g *GitHub) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	g.mu.Lock()
	a, ok := g.assets[name]
	g.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("the release has no %s", name)
	}
	rc, err := g.rest().OpenAsset(ctx, a)
	return rc, g.private(ctx, err)
}

func (g *GitHub) read(ctx context.Context, name string) ([]byte, error) {
	r, err := g.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 1<<20))
}

// private says, of GitHub's 404 read with no token, that a private repo needs one.
func (g *GitHub) private(ctx context.Context, err error) error {
	if !gh.NotFound(err) {
		return err
	}
	if g.Token != nil {
		if tok, _ := g.Token(ctx); tok != "" {
			return err
		}
	}
	return fmt.Errorf("%w: a private repo needs a GitHub token (GH_TOKEN, or gh auth login)", err)
}

// Cache keeps each artifact the app downloads, checked against the manifest.
type Cache struct {
	Dir      string
	Source   Source
	Progress Progress
}

// Open reads a from the cache, downloading it first if it isn't there yet.
func (c Cache) Open(ctx context.Context, m Manifest, a Artifact) (io.ReadCloser, error) {
	if !filepath.IsLocal(a.Name) {
		return nil, fmt.Errorf("bad artifact name %q", a.Name)
	}
	path := filepath.Join(c.Dir, m.Version, a.Name)
	if f, err := os.Open(path); err == nil {
		if _, err := readChecked(f, a); err == nil {
			c.Progress.report(Step{Stage: StageVerify, Detail: a.Name + " (downloaded before): sha256 " + short(a.SHA256)})
			_, _ = f.Seek(0, io.SeekStart)
			return f, nil
		}
		f.Close()
	}
	c.Progress.report(Step{Stage: StageDownload, Total: a.Size, Detail: a.Name})
	r, err := c.Source.Open(ctx, a.Name)
	if err != nil {
		return nil, err
	}
	body, err := readChecked(&counting{r: r, total: a.Size, name: a.Name, p: c.Progress}, a)
	r.Close()
	if err != nil {
		return nil, err
	}
	c.Progress.report(Step{Stage: StageVerify, Detail: a.Name + ": sha256 " + short(a.SHA256)})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := atomicfile.Write(path, body, 0o644); err != nil {
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

// short is a checksum cut for showing.
func short(sum string) string { return sum[:min(len(sum), 12)] }
