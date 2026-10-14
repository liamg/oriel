package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry/settingsfile"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/toml"
)

// sandboxConfigPath is where the worker keeps the sandbox settings the app last sent, for sessions that start
// while no app is connected.
func (w *Worker) sandboxConfigPath() string { return filepath.Join(w.opts.Home, "sandbox.json") }

func (w *Worker) loadSandboxConfig() error {
	b, err := os.ReadFile(w.sandboxConfigPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg sandbox.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		w.log.Warn("sandbox settings", "path", w.sandboxConfigPath(), "err", err)
		return nil
	}
	w.mu.Lock()
	w.sandboxCfg = cfg
	w.mu.Unlock()
	return nil
}

// SetSandboxConfig is the sandbox's settings, as the app resolved them from its sandbox.* options. Sandboxes
// opened after it use them.
func (w *Worker) SetSandboxConfig(cfg sandbox.Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.sandboxConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, w.sandboxConfigPath()); err != nil {
		return err
	}
	w.mu.Lock()
	w.sandboxCfg = cfg
	w.mu.Unlock()
	return nil
}

// SandboxConfig is the sandbox settings the app last sent; the defaults if it never has.
func (w *Worker) SandboxConfig() sandbox.Config {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sandboxCfg
}

// settingsFile is a repo's project settings file.
func settingsFile(repo string) string { return filepath.Join(repo, ".oriel", "settings.toml") }

func (w *Worker) trust() settingsfile.Trust { return settingsfile.Trust{Path: w.opts.TrustPath} }

// TrustSettings trusts a registered repo's .oriel/settings.toml as it is now, as the app's settings.trust does
// for a file on its machine.
func (w *Worker) TrustSettings(file string) error {
	if !slices.ContainsFunc(w.repos.all(), func(r core.Repo) bool { return settingsFile(r.Path) == file }) {
		return fmt.Errorf("%s isn't the settings file of a repo on this worker", file)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return w.trust().Add(file, src)
}

// sandboxConfigFor is the sandbox settings for se: the app's, and when the app can't read this machine's files,
// each of se's repos' own trusted project settings for the repos it sent none for.
func (w *Worker) sandboxConfigFor(se core.SessionState) sandbox.Config {
	cfg := w.SandboxConfig()
	if !cfg.ReadProjects {
		return cfg
	}
	cfg.Projects = maps.Clone(cfg.Projects)
	for _, p := range se.Projects {
		if _, ok := cfg.Projects[p.Repo]; ok || p.Repo == "" {
			continue
		}
		if pc, ok := w.projectSettings(se.ID, p.Repo); ok {
			if cfg.Projects == nil {
				cfg.Projects = map[string]sandbox.ProjectConfig{}
			}
			cfg.Projects[p.Repo] = pc
		}
	}
	return cfg
}

// projectSettings reads a repo's sandbox settings from its .oriel/settings.toml once it's trusted (settings.md,
// "Project trust"); an untrusted one gets a notice in the session offering to trust it.
func (w *Worker) projectSettings(id core.SessionID, repo string) (sandbox.ProjectConfig, bool) {
	file := settingsFile(repo)
	src, err := os.ReadFile(file)
	if err != nil || len(src) == 0 {
		return sandbox.ProjectConfig{}, false
	}
	if !w.trust().Trusted(file, src) {
		_ = w.emit(core.Notified{Notice: core.Notice{ID: "settings.untrusted:" + file, Severity: core.SevWarning, Source: "worker", Session: id,
			Title: "Project settings", Time: w.opts.Now(),
			Text:    fmt.Sprintf("%s on %s isn't trusted yet, so its sandbox settings aren't applied. Trust it if you know what it does.", file, w.opts.Name),
			Actions: []core.Action{{Title: "Trust", Command: "settings.trust", Args: map[string]any{"path": file, "worker": string(w.opts.ID)}}}}})
		return sandbox.ProjectConfig{}, false
	}
	doc, err := toml.Parse(src)
	if err != nil {
		w.log.Warn("project settings", "path", file, "err", err)
		return sandbox.ProjectConfig{}, false
	}
	var egress, rules []string
	var image string
	for _, k := range settingsfile.Keys(doc.Value(), nil) {
		switch k.Name {
		case "sandbox.egress":
			egress, _ = settingsfile.Strings(k.Value)
		case "sandbox.rules":
			rules, _ = settingsfile.Strings(k.Value)
		case "sandbox.image":
			image, _ = k.Value.(string)
		}
	}
	return sandbox.NewProjectConfig(egress, rules, image)
}
