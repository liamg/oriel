package update_test

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/update"
)

// An update shows what every version it goes past changed, not only the newest: each published release after the
// running one, up to and including the newest, newest first. A draft, a pre-release on stable, an older release and
// one signed by a key this build doesn't trust are left out.
func TestChangesAreEveryReleaseAnUpdateGoesPast(t *testing.T) {
	key, other := testKey(t), testKey(t)
	notes := func(k ed25519.PrivateKey, version, text string) update.Release {
		t.Helper()
		rel, err := update.Sign(k, update.Manifest{Version: version, Notes: text})
		if err != nil {
			t.Fatal(err)
		}
		return rel
	}
	rels := map[string]update.Release{
		"v0.0.11": notes(key, "v0.0.11", "older"), "v0.0.13": notes(key, "v0.0.13", "thirteen"),
		"v0.0.14": notes(key, "v0.0.14", "No notable changes."), "v0.0.15": notes(key, "v0.0.15", "fifteen"),
		"rc": notes(key, "v0.0.16-rc.1", "pre"), "forged": notes(other, "v0.0.14", "forged"),
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assets := func(id string) string {
			return `"assets":[{"name":"manifest.json","url":"` + srv.URL + `/a/` + id + `/m"},{"name":"manifest.sig","url":"` + srv.URL + `/a/` + id + `/s"}]`
		}
		if r.URL.Path == "/repos/liamg/oriel/releases" {
			_, _ = w.Write([]byte(`[{"prerelease":true,` + assets("rc") + `},{"draft":true,` + assets("v0.0.16") + `},{` + assets("v0.0.15") +
				`},{` + assets("forged") + `},{` + assets("v0.0.14") + `},{` + assets("v0.0.13") + `},{` + assets("v0.0.11") + `}]`))
			return
		}
		id, file := filepath.Base(filepath.Dir(r.URL.Path)), filepath.Base(r.URL.Path)
		rel, ok := rels[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if file == "m" {
			_, _ = w.Write(rel.Manifest)
		} else {
			_, _ = w.Write(rel.Signature)
		}
	}))
	defer srv.Close()
	gh := &update.GitHub{Repo: "liamg/oriel", Base: srv.URL}
	trust := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	ctx := context.Background()

	_, newest, err := update.Check(ctx, gh, trust, update.Stable, "v0.0.12")
	if err != nil || newest.Version != "v0.0.15" {
		t.Fatalf("newest = %+v, %v", newest, err)
	}
	var got []string
	for _, m := range update.Changes(ctx, gh, trust, update.Stable, "v0.0.12", newest) {
		got = append(got, m.Version+": "+m.Notes)
	}
	want := []string{"v0.0.15: fifteen", "v0.0.14: No notable changes.", "v0.0.13: thirteen"}
	if len(got) != len(want) {
		t.Fatalf("changes = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("changes = %q, want %q", got, want)
			break
		}
	}

	// A source that can't list its releases gives the newest alone.
	if ch := update.Changes(ctx, update.Dir(t.TempDir()), trust, update.Stable, "v0.0.12", newest); len(ch) != 1 || ch[0].Version != "v0.0.15" {
		t.Errorf("a single release's changes = %+v", ch)
	}
}
