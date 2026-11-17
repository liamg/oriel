package cli

import (
	"log/slog"
	"path/filepath"

	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/term/ghostty"
)

// wasmCaches points the process's grammar and terminal runtimes at the cache, before any window can use them.
func wasmCaches(dir string, log *slog.Logger) {
	if dir == "" {
		return
	}
	for _, err := range []error{syntax.SetCacheDir(dir), ghostty.SetCacheDir(filepath.Join(dir, "wasm"))} {
		if err != nil {
			log.Warn("wasm cache", "err", err)
		}
	}
}
