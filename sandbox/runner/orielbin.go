package runner

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/liamg/oriel/sandbox"
)

var (
	helperMu sync.Mutex
	helper   func() ([]byte, error)
)

// SetHelper gives the backend the sandbox helper the app carries (package helper): the static Linux oriel its
// containers run.
func SetHelper(f func() ([]byte, error)) {
	helperMu.Lock()
	helper = f
	helperMu.Unlock()
}

// errNoHelper is a backend given no helper.
var errNoHelper = errors.New("this oriel carries no sandbox helper: build it with `make build`, or set ORIEL_SANDBOX_BIN")

// OrielBinary is the oriel the container runs: ORIEL_SANDBOX_BIN when set, else the helper the app carries, written
// out once per build. Each build gets a file of its own: running containers have the old one bind-mounted.
func OrielBinary() (string, error) {
	if p := os.Getenv("ORIEL_SANDBOX_BIN"); p != "" {
		return p, nil
	}
	helperMu.Lock()
	defer helperMu.Unlock()
	if helper == nil {
		return "", errNoHelper
	}
	b, err := helper()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	dst := filepath.Join(sandbox.Base(), "bin", fmt.Sprintf("oriel-sandbox-%x", sum[:6]))
	if fi, err := os.Stat(dst); err == nil && fi.Size() == int64(len(b)) {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return "", err
	}
	return dst, os.Rename(tmp, dst)
}

// helperProblem is what stops the backend handing its containers an oriel.
func helperProblem() (sandbox.Problem, bool) {
	if _, err := OrielBinary(); err != nil {
		return sandbox.Problem{Code: "oriel-helper-missing", Blocking: true, Message: "Sandboxes have no oriel to run: " + err.Error(),
			Fix: "Build Oriel with `make build`, which embeds its sandbox helper."}, false
	}
	return sandbox.Problem{}, true
}
