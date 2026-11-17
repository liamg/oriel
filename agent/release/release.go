// Package release reads an agent's release metadata for its sandbox: what names a build and its checksum. The
// build itself is fetched checked against it (internal/fetch). Providers (claudebox, codex) use it with a client of
// their own.
package release

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/liamg/oriel/internal/fetch"
)

// GetJSON decodes what url answers into v. header is sent with the request; c is http.DefaultClient if nil.
func GetJSON(ctx context.Context, c *http.Client, url string, header http.Header, v any) error {
	resp, err := fetch.Get(ctx, c, url, header)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(resp.Body).Decode(v)
}
