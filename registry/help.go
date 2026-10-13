package registry

import (
	"io/fs"
	"slices"

	"github.com/liamg/oriel/ui"
)

// HelpSource is the help pages a contributor ships: markdown files, each with an id in its front matter (help.md).
type HelpSource struct {
	FS     fs.FS
	Source string
}

func (r *reg) HelpPages(s HelpSource) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := tokens.Add(1)
	r.help = append(r.help, entry[HelpSource]{tok, s})
	r.helpGen++
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.help = slices.DeleteFunc(r.help, func(e entry[HelpSource]) bool { return e.token == tok })
		r.helpGen++
	})
}

// HelpSources are every contributor's pages, in registration order, and a number that changes when they do.
func (r *reg) HelpSources() ([]HelpSource, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]HelpSource, len(r.help))
	for i, e := range r.help {
		out[i] = e.value
	}
	return out, r.helpGen
}

// HelpLive registers a "Right now" block a page shows with {{live "name"}}: whoever owns the data draws it.
func (r *reg) HelpLive(name string, fn func(c *ui.Ctx)) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.live.push(name, fn)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.live.remove(name, tok)
	})
}

func (r *reg) HelpLiveByName(name string) (func(c *ui.Ctx), bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.live.top(name)
}
