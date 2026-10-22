package shell

import (
	"context"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// lazyPlugin is a Go plugin whose manifest has lazy, as a Lua plugin's can: it starts as a placeholder for its
// triggers, and the first use of one activates the plugin itself, on the host it would have started on.
type lazyPlugin struct {
	plugin.Plugin
	sh     *shell
	ctx    context.Context
	host   *pluginHost
	holder registry.Disposable
	loaded bool
}

func (l *lazyPlugin) Activate(ctx context.Context, _ plugin.Host) error {
	l.ctx = ctx
	m := l.Manifest()
	l.holder = l.host.Placeholder(m.Lazy.Placeholder(l.host, m.ID, l.host.source, l.load))
	return nil
}

func (l *lazyPlugin) load() error {
	if err := l.Plugin.Activate(l.ctx, l.host); err != nil {
		return err
	}
	l.loaded = true
	if l.sh.cfg.Activated != nil {
		l.sh.cfg.Activated(l.Plugin)
	}
	l.sh.poke()
	return nil
}

func (l *lazyPlugin) Deactivate(ctx context.Context) error {
	if !l.loaded {
		l.holder.Dispose()
		return nil
	}
	return l.Plugin.Deactivate(ctx)
}

// waits says p is a lazy plugin not used yet.
func waits(p plugin.Plugin) bool {
	l, ok := p.(*lazyPlugin)
	return ok && !l.loaded
}

// unwrapped is the plugin itself, behind its placeholder if it's lazy.
func unwrapped(p plugin.Plugin) plugin.Plugin {
	if l, ok := p.(*lazyPlugin); ok {
		return l.Plugin
	}
	return p
}

// start activates e on h and adds it to active; a lazy one waits behind a placeholder for its first use.
func (sh *shell) start(ctx context.Context, e plugin.Plugin, h *pluginHost, active *[]plugin.Plugin) error {
	if !e.Manifest().Lazy.Empty() {
		l := &lazyPlugin{Plugin: e, sh: sh, host: h}
		*active = append(*active, l)
		return l.Activate(ctx, h)
	}
	if err := e.Activate(ctx, h); err != nil {
		return err
	}
	*active = append(*active, e)
	if sh.cfg.Activated != nil {
		sh.cfg.Activated(e)
	}
	return nil
}
