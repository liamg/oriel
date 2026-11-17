// Package fetch takes what's downloaded only once it checks: no more than a limit, and with the sha256 it should
// have. A file is written beside its place and renamed in only then, so it's never partial or unchecked.
package fetch

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var (
	// ErrTooLarge is a download over its limit.
	ErrTooLarge = errors.New("over its size limit")
	// ErrMismatch is a download whose sha256 isn't the one it should have.
	ErrMismatch = errors.New("doesn't match its checksum")
)

// Sum is b's sha256, in hex.
func Sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Matches says got, a hex sha256, is want: hex in either case, with or without a "sha256:" prefix.
func Matches(got, want string) bool {
	return got != "" && strings.EqualFold(got, strings.TrimPrefix(want, "sha256:"))
}

// Read is what r gives, no more than max bytes, once its sha256 is want.
func Read(r io.Reader, want string, max int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := check(&buf, r, want, max); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write streams what r gives, no more than max bytes, to dst with mode perm once its sha256 is want: it's written
// beside dst, and renamed into place only when it checks.
func Write(r io.Reader, want, dst string, perm os.FileMode, max int64) error {
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = check(f, r, want, max)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// check copies r to w, no more than max bytes, and says whether its sha256 is want.
func check(w io.Writer, r io.Reader, want string, max int64) error {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(r, max+1))
	switch {
	case err != nil:
		return err
	case n > max:
		return fmt.Errorf("%w of %d bytes", ErrTooLarge, max)
	case !Matches(hex.EncodeToString(h.Sum(nil)), want):
		return ErrMismatch
	}
	return nil
}

// Get is url's answer, once it's 200 OK. header is sent with the request; c is http.DefaultClient if nil.
func Get(ctx context.Context, c *http.Client, url string, header http.Header) (*http.Response, error) {
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

// Verified is what url answers, no more than max bytes, once its sha256 is want.
func Verified(ctx context.Context, c *http.Client, url, want string, max int64) ([]byte, error) {
	resp, err := Get(ctx, c, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return Read(resp.Body, want, max)
}

// Download streams what url answers, no more than max bytes, to dst with mode perm once its sha256 is want (Write).
func Download(ctx context.Context, c *http.Client, url, want, dst string, perm os.FileMode, max int64) error {
	resp, err := Get(ctx, c, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return Write(resp.Body, want, dst, perm, max)
}
