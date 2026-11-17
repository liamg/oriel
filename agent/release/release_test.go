package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liamg/oriel/internal/isolate"
)

// A JSON answer decodes, with the headers asked for.
func TestGetJSON(t *testing.T) {
	isolate.Test(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`{"accept":"` + r.Header.Get("Accept") + `"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	var v struct{ Accept string }
	if err := GetJSON(context.Background(), srv.Client(), srv.URL, http.Header{"Accept": {"application/vnd.github+json"}}, &v); err != nil ||
		v.Accept != "application/vnd.github+json" {
		t.Errorf("GetJSON = %+v, %v", v, err)
	}
}
