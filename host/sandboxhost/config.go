package sandboxhost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/sandbox"
)

// configPath is where the host keeps the sandbox settings the app last sent, for sessions that start while no app is
// connected.
func (x *Boxes) configPath() string { return filepath.Join(x.deps.Env.Home, "sandbox.json") }

func (x *Boxes) load() error {
	b, err := os.ReadFile(x.configPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg sandbox.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		x.deps.Env.Log.Warn("sandbox settings", "path", x.configPath(), "err", err)
		return nil
	}
	x.cfgMu.Lock()
	x.cfg = cfg
	x.cfgMu.Unlock()
	return nil
}

// SetConfig is the sandbox's settings, as the app resolved them from its sandbox.* options. Sandboxes opened after
// it use them.
func (x *Boxes) SetConfig(cfg sandbox.Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := x.configPath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, x.configPath()); err != nil {
		return err
	}
	x.cfgMu.Lock()
	x.cfg = cfg
	x.cfgMu.Unlock()
	return nil
}

// Config is the sandbox settings the app last sent; the defaults if it never has.
func (x *Boxes) Config() sandbox.Config {
	x.cfgMu.Lock()
	defer x.cfgMu.Unlock()
	return x.cfg
}
