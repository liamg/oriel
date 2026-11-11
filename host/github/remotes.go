package github

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/liamg/oriel/gitx"
)

// remotes keeps each checkout's origin URL as its repo's config had it: a sync, a read and a PR lookup each want
// it, for every project, and it changes only when the config does.
type remotes struct {
	mu   sync.Mutex
	urls map[string]remoteURL
}

type remoteURL struct {
	url    string
	config time.Time // the config file's time when it was read
}

// remoteURL is repo's origin URL, read with git only when its config changed since it was last read. A read that
// fails isn't kept: the next asks git again.
func (x *GitHub) remoteURL(ctx context.Context, repo gitx.Repo) string {
	_, common := gitx.Dirs(repo.Dir)
	var at time.Time
	if fi, err := os.Stat(filepath.Join(common, "config")); err == nil {
		at = fi.ModTime()
	}
	r := &x.remotes
	r.mu.Lock()
	got, ok := r.urls[repo.Dir]
	r.mu.Unlock()
	if ok && !at.IsZero() && got.config.Equal(at) {
		return got.url
	}
	url, err := repo.Origin(ctx)
	if err != nil {
		return got.url // as last read: a git that failed says nothing of the remote
	}
	r.mu.Lock()
	if r.urls == nil {
		r.urls = map[string]remoteURL{}
	}
	r.urls[repo.Dir] = remoteURL{url: url, config: at}
	r.mu.Unlock()
	return url
}
