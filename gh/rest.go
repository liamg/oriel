package gh

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// REST is GET over GitHub's REST API with one set of rules for every caller: the API version header, the token's
// Authorization when there is one, and a public repo read again without a token GitHub refused (401).
type REST struct {
	Base   string                                    // https://api.github.com if ""
	Token  func(ctx context.Context) (string, error) // none when nil, or it gives ""
	Client *http.Client                              // http.DefaultClient if nil
}

// Get asks for path (under Base, or a whole URL such as an asset's) as accept, and answers GitHub's response, which
// the caller closes: a status GitHub refused with is the caller's to read.
func (r REST) Get(ctx context.Context, path, accept string) (*http.Response, error) {
	url := path
	if !strings.HasPrefix(url, "http") {
		url = strings.TrimSuffix(cmp.Or(r.Base, "https://api.github.com"), "/") + "/" + strings.TrimPrefix(path, "/")
	}
	tok := ""
	if r.Token != nil {
		var err error
		if tok, err = r.Token(ctx); err != nil {
			return nil, err
		}
	}
	resp, err := r.do(ctx, url, accept, tok)
	if err == nil && tok != "" && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		resp, err = r.do(ctx, url, accept, "") // a public repo reads without the token GitHub refused
	}
	return resp, err
}

func (r REST) do(ctx context.Context, url, accept, tok string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return cmp.Or(r.Client, http.DefaultClient).Do(req)
}

// JSON gets path and decodes it into out; a status GitHub refused with is an *Error.
func (r REST) JSON(ctx context.Context, path string, out any) error {
	resp, err := r.Get(ctx, path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := refused(resp); err != nil {
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// refused is GitHub's refusal in resp, as an *Error; nil for a success.
func refused(resp *http.Response) error {
	if resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &e) != nil || e.Message == "" {
		e.Message = strings.TrimSpace(string(b))
	}
	return &Error{Status: resp.StatusCode, Message: e.Message}
}

// Release is one of a repo's releases, and its files.
type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Asset is a release's file: its name, and the API URL it's read from.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Asset is the release's file called name.
func (rel Release) Asset(name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Releases are repo's newest n releases, newest first.
func (r REST) Releases(ctx context.Context, repo string, n int) ([]Release, error) {
	var out []Release
	return out, r.JSON(ctx, fmt.Sprintf("/repos/%s/releases?per_page=%d", repo, n), &out)
}

// ReleaseByTag is repo's release for tag.
func (r REST) ReleaseByTag(ctx context.Context, repo, tag string) (Release, error) {
	var out Release
	return out, r.JSON(ctx, "/repos/"+repo+"/releases/tags/"+tag, &out)
}

// OpenAsset reads a release's file, which the caller closes.
func (r REST) OpenAsset(ctx context.Context, a Asset) (io.ReadCloser, error) {
	resp, err := r.Get(ctx, a.URL, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if err := refused(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

// ReadAsset is a release's file, up to limit bytes.
func (r REST) ReadAsset(ctx context.Context, a Asset, limit int64) ([]byte, error) {
	rc, err := r.OpenAsset(ctx, a)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}
