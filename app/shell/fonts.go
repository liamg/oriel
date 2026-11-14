package shell

import (
	"sync"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
)

// installable is a shaper that can draw its roles with the machine's installed families (typeset.Typesetter).
type installable interface {
	Families() []string
	Use(role, family string) error
}

// fonts is the window's registry.Fonts, over its shaper.
type fonts struct {
	sh   installable
	poke func()

	mu      sync.Mutex
	listed  []string
	read    bool
	reading bool
	using   work.Busy
}

// contributeFonts offers the shaper's fonts as the registry's Fonts, when it can draw with installed ones.
func contributeFonts(r registry.Registry, sh typeset.Shaper, poke func()) func() {
	in, ok := sh.(installable)
	if !ok {
		return func() {}
	}
	f := &fonts{sh: in, poke: poke}
	d := r.Contribute(registry.Service{Name: registry.FontsService, Impl: f, Source: "builtin:window"})
	b := r.Contribute(registry.Busy{Name: "fonts", Busy: f.using.Busy, Source: "builtin:window"})
	return func() {
		d.Dispose()
		b.Dispose()
		f.using.Wait()
	}
}

func (f *fonts) Families(ready func()) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.read || f.reading {
		return f.listed
	}
	f.reading = true
	f.using.Go(func() {
		listed := f.sh.Families()
		f.mu.Lock()
		f.listed, f.read, f.reading = listed, true, false
		f.mu.Unlock()
		if ready != nil {
			ready()
		}
		f.poke()
	})
	return nil
}

func (f *fonts) Use(role, family string, done func(error)) {
	f.using.Go(func() {
		err := f.sh.Use(role, family)
		f.poke()
		if done != nil {
			done(err)
		}
	})
}
