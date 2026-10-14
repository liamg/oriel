package worker

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/sandbox"
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
