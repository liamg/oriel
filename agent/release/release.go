// Package release fetches an agent's release for its sandbox: the metadata that names a build and its checksum,
// then the build itself, checked against it. Providers (claudebox, codex) use it with a client of their own.
package release

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// GetJSON decodes what url answers into v. header is sent with the request; c is http.DefaultClient if nil.
func GetJSON(ctx context.Context, c *http.Client, url string, header http.Header, v any) error {
	resp, err := get(ctx, c, url, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// Verified is what url answers, no more than max bytes, once its sha256 is sum (hex); what fails the check is
// refused, naming what.
func Verified(ctx context.Context, c *http.Client, url, sum string, max int64, what string) ([]byte, error) {
	resp, err := get(ctx, c, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("the %s download is over %d bytes", what, max)
	}
	got := sha256.Sum256(b)
	if hex.EncodeToString(got[:]) != strings.ToLower(sum) {
		return nil, fmt.Errorf("the %s download does not match its checksum", what)
	}
	return b, nil
}

// DownloadVerified streams what url answers, no more than max bytes, to dst with mode perm, once its sha256 is sum
// (hex): it's written beside dst and only renamed into place when it checks, so dst is never a partial or unchecked
// file.
func DownloadVerified(ctx context.Context, c *http.Client, url, sum, dst string, perm os.FileMode, max int64, what string) error {
	resp, err := get(ctx, c, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case n > max:
		return fmt.Errorf("the %s download is over %d bytes", what, max)
	case hex.EncodeToString(h.Sum(nil)) != strings.ToLower(sum):
		return fmt.Errorf("the %s download does not match its checksum", what)
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	ok = true
	return nil
}

func get(ctx context.Context, c *http.Client, url string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	resp, err := cmp.Or(c, http.DefaultClient).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}
