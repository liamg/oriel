package lang

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	reglang "github.com/liamg/oriel/registry/lang"
)

// installTimeout bounds one server's install: npm fetching a server and its dependencies.
const installTimeout = 10 * time.Minute

// serverInstalls is what LSP knows of servers that weren't found: the languages it has said so for, and the
// servers installing now.
type serverInstalls struct {
	mu         sync.Mutex
	told       map[string]bool // by language
	installing map[string]bool // by server
}

// find finds a server for a project root, if it may start: the project's, then PATH, then Oriel's own install.
func (x *LSP) find(s lsp.ServerSpec, root string) (string, bool) {
	if x.Servers != nil && !x.Servers(s) {
		return "", false
	}
	return s.Locate(root, x.InstallDir)
}

// known are the servers for language that may start, by Order, whether found or not.
func (x *LSP) known(language string) []lsp.ServerSpec {
	var out []lsp.ServerSpec
	for _, s := range reglang.LSPServers(x.h) {
		if spec := lsp.ServerSpec(s); spec.Serves(language) && (x.Servers == nil || x.Servers(spec)) {
			out = append(out, spec)
		}
	}
	return out
}

// installable is the first of specs Oriel can install on this machine.
func (x *LSP) installable(specs []lsp.ServerSpec) (lsp.ServerSpec, bool) {
	if x.InstallDir == "" {
		return lsp.ServerSpec{}, false
	}
	i := slices.IndexFunc(specs, func(s lsp.ServerSpec) bool { return s.Install != nil && s.Install.Can() })
	if i < 0 {
		return lsp.ServerSpec{}, false
	}
	return specs[i], true
}

// missing says, once per language, that a file of it has no server: which servers Oriel knows for it, and an Install
// button when it can install one here, or the command to install one by hand.
func (x *LSP) missing(language string) {
	x.installs.mu.Lock()
	told := x.installs.told[language]
	if x.installs.told == nil {
		x.installs.told = map[string]bool{}
	}
	x.installs.told[language] = true
	x.installs.mu.Unlock()
	specs := x.known(language)
	if on, _ := registry.Option[bool](x.h.Options(), "lsp.missing_notice", registry.At{}); told || !on || len(specs) == 0 {
		return
	}
	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}
	n := core.Notice{ID: "lsp.missing." + language, Severity: core.SevInfo, Source: "lsp", Title: "No language server for " + language + " files",
		Text: strings.Join(names, " or ") + " isn't installed, so there's no completion, hover or go to definition."}
	if s, ok := x.installable(specs); ok {
		n.Text += " Oriel can install " + s.Name + " with " + s.Install.Tool + "."
		n.Actions = []core.Action{{Title: "Install " + s.Name, Command: "lsp.server.install", Args: registry.Args{"name": s.Name}}}
	} else if i := slices.IndexFunc(specs, func(s lsp.ServerSpec) bool { return s.Install != nil }); i >= 0 {
		n.Text += " Install it with " + specs[i].Install.String() + "."
	}
	x.h.Do(func() { x.h.Notify(n) })
}

func (x *LSP) installCommands(h plugin.Host) {
	x.Add(h.Option(registry.OptionSpec{Name: "lsp.missing_notice", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Say when a file's language has language servers but none is installed, with a button to install one when Oriel can."}))
	x.Add(h.Command(registry.Command{ID: "lsp.server.install", UserOnly: true, Title: "Install a language server Oriel can't find: {name}, into Oriel's own directory",
		Available: func(registry.Args) bool { return x.InstallDir != "" },
		Run:       func(_ registry.Ctx, a registry.Args) error { return x.install(a.String("name")) }}))
}

// install installs the server named name off the loop, then opens the files that had none on it.
func (x *LSP) install(name string) error {
	i := slices.IndexFunc(reglang.LSPServers(x.h), func(s reglang.LSPServer) bool { return s.Name == name })
	if i < 0 {
		return fmt.Errorf("no language server called %q", name)
	}
	spec := lsp.ServerSpec(reglang.LSPServers(x.h)[i])
	x.installs.mu.Lock()
	if x.installs.installing[name] {
		x.installs.mu.Unlock()
		return nil
	}
	if x.installs.installing == nil {
		x.installs.installing = map[string]bool{}
	}
	x.installs.installing[name] = true
	x.installs.mu.Unlock()
	for _, l := range spec.Language {
		x.h.Dismiss("lsp.missing." + l)
	}
	id := "lsp.install." + name
	x.h.Notify(core.Notice{ID: id, Severity: core.SevInfo, Source: "lsp", Title: "Installing " + name, Text: "with " + spec.Install.String()})
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, installTimeout)
		defer cancel()
		var out strings.Builder
		err := spec.InstallInto(ctx, x.InstallDir, &out)
		x.h.Do(func() {
			x.installs.mu.Lock()
			delete(x.installs.installing, name)
			x.installs.mu.Unlock()
			if err != nil {
				kit.NotifyFailure(x.h, core.Notice{ID: id, Source: "lsp", Title: "Couldn't install " + name}, err)
				return
			}
			x.h.Notify(core.Notice{ID: id, Severity: core.SevInfo, Source: "lsp", Title: "Installed " + name, AutoClear: true})
			x.retry()
		})
	})
	return nil
}

// retry forgets the files that found no server, so each is opened again as it's next drawn, and starts the project's
// servers again.
func (x *LSP) retry() {
	x.mu.Lock()
	for p, d := range x.docs {
		if d.failed && d.client == nil {
			delete(x.docs, p)
		}
	}
	x.mu.Unlock()
	x.installs.mu.Lock()
	x.installs.told = nil
	x.installs.mu.Unlock()
	x.h.DecorationsChanged("lsp")
	x.work.Go(func(context.Context) { x.warm() })
}
