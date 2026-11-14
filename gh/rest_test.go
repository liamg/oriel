package gh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// REST sends the API version and the token; a token GitHub refuses is dropped for a public repo's read; a 404 is
// GitHub's NotFound; an asset reads up to its limit.
func TestREST(t *testing.T) {
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-GitHub-Api-Version") == "" {
			t.Errorf("%s asked without the API version", r.URL.Path)
		}
		auths = append(auths, r.Header.Get("Authorization"))
		switch {
		case r.Header.Get("Authorization") != "":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/repos/acme/x/releases":
			w.Write([]byte(`[{"tag_name":"v1","assets":[{"name":"index.json","url":"` + "http://" + r.Host + `/asset/1"}]}]`))
		case r.URL.Path == "/asset/1":
			w.Write([]byte("0123456789"))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()
	rest := REST{Base: srv.URL, Token: func(context.Context) (string, error) { return "stale", nil }}
	ctx := context.Background()
	rels, err := rest.Releases(ctx, "acme/x", 5)
	if err != nil || len(rels) != 1 || rels[0].Tag != "v1" {
		t.Fatalf("releases %+v, %v", rels, err)
	}
	if len(auths) != 2 || auths[0] != "Bearer stale" || auths[1] != "" {
		t.Errorf("the token GitHub refused wasn't dropped for the second try: %q", auths)
	}
	a, _ := rels[0].Asset("index.json")
	if b, err := rest.ReadAsset(ctx, a, 4); err != nil || string(b) != "0123" {
		t.Errorf("the asset to its limit: %q, %v", b, err)
	}
	if _, err := rest.ReleaseByTag(ctx, "acme/x", "v9"); !NotFound(err) {
		t.Errorf("a missing release: %v", err)
	}
}
