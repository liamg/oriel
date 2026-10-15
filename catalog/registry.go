package catalog

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/toml"
)

// The registry repo's layout and the files its CI publishes (catalog.md, "The registry repo").
const (
	PluginsDir  = "plugins"     // plugins/<owner>/<name>/plugin.toml
	PluginFile  = "plugin.toml" //
	IndexFile   = "index.json"
	IndexSig    = "index.sig"
	DefaultRepo = "liamg/oriel-plugins"
)

// Listing is one plugin.toml: a plugin as the registry lists it.
type Listing struct {
	ID          string            `json:"id"` // its path: <owner>/<name>
	Repo        string            `json:"repo"`
	Description string            `json:"description,omitempty"`
	Trusted     bool              `json:"trusted,omitempty"`
	Since       string            `json:"since,omitempty"` // when it was trusted
	Versions    []Version         `json:"versions,omitempty"`
	Revoked     map[string]string `json:"revoked,omitempty"` // version → why
}

// Version is one [[versions]] entry: a reviewed version, or a yanked one.
type Version struct {
	Version  string            `json:"version"`
	Commit   string            `json:"commit,omitempty"`
	Reviewed string            `json:"reviewed,omitempty"` // the PR it was reviewed in
	Archives map[string]string `json:"archives,omitempty"` // platform → "sha256:…"
	Built    string            `json:"built,omitempty"`    // ci: rebuilt and matched; author: not reproducible
	Yanked   string            `json:"yanked,omitempty"`   // why it isn't offered for new installs
}

// Owner is the GitHub owner of the plugin's repository: github.com/<owner>/….
func (l Listing) Owner() string {
	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(l.Repo, "https://"), "github.com/"), "/")
	if !strings.HasPrefix(strings.TrimPrefix(l.Repo, "https://"), "github.com/") || len(parts) < 2 {
		return ""
	}
	return parts[0]
}

// RepoName is owner/name of its repository.
func (l Listing) RepoName() string {
	r := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(l.Repo, "https://"), "github.com/"), ".git")
	return r
}

// Version is the entry for v.
func (l Listing) Version(v string) (Version, bool) {
	for _, x := range l.Versions {
		if x.Version == v {
			return x, true
		}
	}
	return Version{}, false
}

// ParseListing reads a plugin.toml, whose id is its path in the registry.
func ParseListing(id string, src []byte) (Listing, error) {
	doc, err := toml.Parse(src)
	if err != nil {
		return Listing{}, fmt.Errorf("%s: %w", id, err)
	}
	v := doc.Value()
	l := Listing{ID: id, Repo: str(v["repo"]), Description: str(v["description"]), Since: str(v["since"])}
	l.Trusted, _ = v["trusted"].(bool)
	for k := range v {
		switch k {
		case "repo", "description", "trusted", "since", "versions", "revoked":
		default:
			return l, fmt.Errorf("%s: plugin.toml has no %q", id, k)
		}
	}
	if l.Owner() == "" {
		return l, fmt.Errorf("%s: repo %q isn't github.com/<owner>/<name>", id, l.Repo)
	}
	if vs, ok := v["versions"].([]any); ok {
		for _, x := range vs {
			t, ok := x.(map[string]any)
			if !ok {
				return l, fmt.Errorf("%s: [[versions]] are tables", id)
			}
			ver := Version{Version: str(t["version"]), Commit: str(t["commit"]), Reviewed: str(t["reviewed"]), Built: str(t["built"]),
				Yanked: str(t["yanked"])}
			if _, err := plugin.ParseVersion(ver.Version); err != nil {
				return l, fmt.Errorf("%s: %w", id, err)
			}
			if a, ok := t["archives"].(map[string]any); ok {
				ver.Archives = map[string]string{}
				for p, s := range a {
					ver.Archives[p] = str(s)
				}
			}
			if ver.Yanked == "" && !isCommit(ver.Commit) {
				return l, fmt.Errorf("%s %s: commit %q isn't a full commit hash", id, ver.Version, ver.Commit)
			}
			switch ver.Built {
			case "", "ci", "author":
			default:
				return l, fmt.Errorf("%s %s: built is ci or author, not %q", id, ver.Version, ver.Built)
			}
			l.Versions = append(l.Versions, ver)
		}
	}
	if r, ok := v["revoked"].(map[string]any); ok {
		l.Revoked = map[string]string{}
		for ver, why := range r {
			l.Revoked[ver] = str(why)
		}
	}
	return l, nil
}

// ReadListings reads every plugins/<owner>/<name>/plugin.toml under a registry checkout, by id.
func ReadListings(root string) (map[string]Listing, error) {
	files, err := filepath.Glob(filepath.Join(root, PluginsDir, "*", "*", PluginFile))
	if err != nil {
		return nil, err
	}
	out := map[string]Listing{}
	for _, f := range files {
		rel, _ := filepath.Rel(filepath.Join(root, PluginsDir), filepath.Dir(f))
		id := filepath.ToSlash(rel)
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		l, err := ParseListing(id, src)
		if err != nil {
			return nil, err
		}
		out[id] = l
	}
	return out, nil
}

// Index is what the registry publishes: every listing, flattened, signed as a whole (catalog.md, The index).
type Index struct {
	Registry string    `json:"registry"` // owner/name of the registry repo
	Built    time.Time `json:"built"`
	Plugins  []Listing `json:"plugins"`
}

// Plugin is the listing for id.
func (ix Index) Plugin(id string) (Listing, bool) {
	for _, l := range ix.Plugins {
		if l.ID == id {
			return l, true
		}
	}
	return Listing{}, false
}

// BuildIndex flattens a registry checkout's listings into its index.
func BuildIndex(root, registry string, now time.Time) (Index, error) {
	ls, err := ReadListings(root)
	if err != nil {
		return Index{}, err
	}
	ix := Index{Registry: registry, Built: now.UTC()}
	for _, id := range sortedKeys(ls) {
		ix.Plugins = append(ix.Plugins, ls[id])
	}
	return ix, nil
}

// SignIndex is the index's bytes and their ed25519 signature: index.json and index.sig.
func SignIndex(key ed25519.PrivateKey, ix Index) (body, sig []byte, err error) {
	if body, err = json.MarshalIndent(ix, "", "  "); err != nil {
		return nil, nil, err
	}
	return body, ed25519.Sign(key, body), nil
}

// PublicKey is a registry's ed25519 public key.
type PublicKey = ed25519.PublicKey

// ErrBadSignature is an index no trusted key signed.
var ErrBadSignature = errors.New("the index's signature doesn't verify")

// VerifyIndex reads index.json once its signature verifies with one of keys; nothing in an unverified index is read.
func VerifyIndex(body, sig []byte, keys []ed25519.PublicKey) (Index, error) {
	ok := false
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, body, sig) {
			ok = true
			break
		}
	}
	if !ok {
		return Index{}, ErrBadSignature
	}
	var ix Index
	if err := json.Unmarshal(body, &ix); err != nil {
		return Index{}, fmt.Errorf("the index: %w", err)
	}
	return ix, nil
}

// ParseKey reads a public key as [registry] keys write it: "ed25519:<hex>", or the hex alone.
func ParseKey(s string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "ed25519:"))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%q isn't an ed25519 public key (ed25519:<64 hex digits>)", s)
	}
	return ed25519.PublicKey(b), nil
}

// FormatKey writes a public key as ParseKey reads it.
func FormatKey(k ed25519.PublicKey) string { return "ed25519:" + hex.EncodeToString(k) }

// ArchiveName is the release asset a platform's archive is published as: <name>-<version>-<os>-<arch>.tar.gz.
func ArchiveName(id, version, platform string) string {
	name := path.Base(id)
	if platform == AnyPlatform || platform == "" {
		return name + "-" + version + ".tar.gz"
	}
	return name + "-" + version + "-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz"
}

// sortVersions orders versions newest first.
func sortVersions(vs []string) {
	sort.SliceStable(vs, func(i, j int) bool {
		a, aerr := plugin.ParseVersion(vs[i])
		b, berr := plugin.ParseVersion(vs[j])
		if aerr != nil || berr != nil {
			return aerr == nil
		}
		return a.Compare(b) > 0
	})
}
