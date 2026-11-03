package plugintest_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/plugintest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/integ/acp"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// A Lua plugin brings an agent served by its own process: the launcher offers it with what its process detects,
// a session runs on it end to end, and one of Claude's name takes Claude's place.
func TestPluginAgentProvider(t *testing.T) {
	h := rig.NewHostWith(t, host.Options{Exec: hostexec.Exec{}})
	a := plugintest.New(t, plugintest.Options{Config: func(cfg *app.Config) { cfg.Host, cfg.Store = h.W, h.W.Store() }})
	dir := plugintest.Install(t, "testdata/agent/plugin", "./testdata/agent/cmd/oriel-agent")
	a.Load(dir, nil)

	var infos []host.ProviderInfo
	find := func(id string) (host.ProviderInfo, bool) {
		i := slices.IndexFunc(infos, func(p host.ProviderInfo) bool { return p.ID == id })
		if i < 0 {
			return host.ProviderInfo{}, false
		}
		return infos[i], true
	}
	a.Until("the plugin's providers in the launcher's list", func() bool {
		infos = h.W.ProviderInfo(context.Background())
		s, ok := find("scripted")
		c, cok := find("claude")
		return ok && s.Install.Bin == "scripted" && cok && c.Install.Bin == "scripted"
	})
	if s, _ := find("scripted"); len(s.Models) != 1 || s.Models[0].ID != "s1" {
		t.Errorf("scripted = %+v", s)
	}
	if got := registry.ProviderTitle(a.Registry(), "claude"); got != "Not Claude" {
		t.Errorf("claude is called %q", got)
	}

	id, err := h.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "w", Prompt: "say hello",
		Provider: "scripted", Model: "s1", Repos: []core.RepoID{core.RepoID(h.Repo)}})
	if err != nil {
		t.Fatal(err)
	}
	// The agent answers what it was told, as the host put it to it: each turn's words come back.
	said := func(text string) bool {
		for _, se := range h.W.Store().State().Sessions {
			if se.ID != id {
				continue
			}
			for _, it := range se.Transcript {
				if it.Kind == core.ItemAssistant && strings.Contains(it.Text, text) {
					return true
				}
			}
		}
		return false
	}
	a.Until("the agent's reply", func() bool { return said("say hello") })
	if err := h.W.Send(host.SendReq{Session: id, Text: "and goodbye"}); err != nil {
		t.Fatal(err)
	}
	a.Until("the second turn's reply", func() bool { return said("and goodbye") })
}

// A Lua plugin brings an ACP agent with acp = { command = … }: the bundled ACP bridge serves it, the launcher
// offers it, and a session runs on it, the agent run under the plugin's exec grant.
func TestPluginACPAgent(t *testing.T) {
	bridge := plugintest.Install(t, "../../integ/acp/plugin", "../../integ/acp/cmd/oriel-acp")
	dir := plugintest.Install(t, "testdata/acp/plugin", "./testdata/acp/cmd/fake-acp")
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := rig.NewHostWith(t, host.Options{Exec: hostexec.Exec{}})
	a := plugintest.New(t, plugintest.Options{Config: func(cfg *app.Config) {
		cfg.Host, cfg.Store = h.W, h.W.Store()
		cfg.Bundled = []plugin.Bundled{acp.Plugin}
		cfg.Self = []string{filepath.Join(bridge, "bin", "oriel-acp")}
	}})
	a.Load(dir, nil)
	var info host.ProviderInfo
	a.Until("the ACP agent in the launcher's list", func() bool {
		for _, p := range h.W.ProviderInfo(context.Background()) {
			if p.ID == "fakeacp" {
				info = p
			}
		}
		return strings.HasSuffix(info.Install.Bin, "fake-acp")
	})
	if len(info.Models) != 1 || info.Models[0].ID != "m1" {
		t.Errorf("fakeacp = %+v", info)
	}
	id, err := h.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "w", Prompt: "say hello",
		Provider: "fakeacp", Model: "m1", Repos: []core.RepoID{core.RepoID(h.Repo)}})
	if err != nil {
		t.Fatal(err)
	}
	a.Until("the agent's reply", func() bool {
		for _, se := range h.W.Store().State().Sessions {
			for _, it := range se.Transcript {
				if se.ID == id && it.Kind == core.ItemAssistant && strings.Contains(it.Text, "say hello") {
					return true
				}
			}
		}
		return false
	})
}

// docs/examples/acp, as its README says to install it, brings Gemini CLI through the bridge.
func TestACPExample(t *testing.T) {
	bridge := plugintest.Install(t, "../../integ/acp/plugin", "../../integ/acp/cmd/oriel-acp")
	a := plugintest.New(t, plugintest.Options{Config: func(cfg *app.Config) {
		cfg.Bundled = []plugin.Bundled{acp.Plugin}
		cfg.Self = []string{filepath.Join(bridge, "bin", "oriel-acp")}
	}})
	p := a.Load("../../docs/examples/acp", nil)
	if p.ID != "example/gemini" {
		t.Errorf("loaded %q", p.ID)
	}
	ps := registry.AgentProviders(a.Registry())
	i := slices.IndexFunc(ps, func(p registry.AgentProvider) bool { return p.Name == "gemini" })
	if i < 0 || ps[i].Title != "Gemini" || ps[i].Mark != "G" {
		t.Fatalf("providers = %+v", ps)
	}
	ms, err := ps[i].Provider.Models(context.Background())
	if err != nil || len(ms) != 2 || ms[0].ID != "gemini-2.5-pro" || !ms[1].Small {
		t.Errorf("models = %+v %v", ms, err)
	}
}
