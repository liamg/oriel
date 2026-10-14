package runner

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/liamg/oriel/sandbox"
)

var (
	orielBuildMu sync.Mutex

	orielMu       sync.Mutex
	orielBuilding bool
	orielErr      error
)

// linuxOriel is where the Linux build of this oriel is kept. Each build gets a
// file of its own: running containers have the old one bind-mounted, and a
// rename over it leaves their `oriel` unable to start.
func linuxOriel() string {
	stamp, _ := buildStamp()
	sum := sha256.Sum256([]byte(stamp))
	return filepath.Join(sandbox.Base(), "bin", fmt.Sprintf("oriel-linux-%s-%x", runtime.GOARCH, sum[:6]))
}

// buildStamp identifies the running oriel, so reinstalling it rebuilds the
// Linux one.
func buildStamp() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %d %d", exe, fi.Size(), fi.ModTime().UnixNano()), nil
}

// linuxOrielCurrent reports whether the Linux build matches the running oriel.
func linuxOrielCurrent() bool {
	stamp, err := buildStamp()
	if err != nil {
		return false
	}
	return stampMatches(linuxOriel(), stamp)
}

func stampMatches(dst, stamp string) bool {
	b, err := os.ReadFile(dst + ".stamp")
	if err != nil || string(b) != stamp {
		return false
	}
	_, err = os.Stat(dst)
	return err == nil
}

// ensureLinuxOriel builds the Linux oriel unless it already matches this one.
func ensureLinuxOriel() (string, error) {
	orielBuildMu.Lock()
	defer orielBuildMu.Unlock()
	stamp, err := buildStamp()
	if err != nil {
		return "", err
	}
	dst := linuxOriel()
	if stampMatches(dst, stamp) {
		return dst, nil
	}
	if err := buildLinuxOriel(dst); err != nil {
		return "", err
	}
	if err := os.WriteFile(dst+".stamp", []byte(stamp), 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

// helperPkg is the in-container helper the Linux build is made from: stdlib
// only, so it cross-compiles without cgo.
const (
	sandboxModule = "github.com/liamg/oriel/sandbox"
	helperPkg     = sandboxModule + "/cmd/oriel-sandbox"
)

// buildLinuxOriel cross-compiles the in-container helper to dst: from the
// source this binary was built from when it is still there, else from the
// sandbox module's published version.
func buildLinuxOriel(dst string) error {
	goBin, err := findGo()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	env := append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0", "GOBIN=")
	tmp := dst + ".part"
	defer func() { _ = os.Remove(tmp) }()
	if src := sourceDir(); src != "" {
		cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", tmp, "./cmd/oriel-sandbox")
		cmd.Dir, cmd.Env = src, env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return os.Rename(tmp, dst)
	}
	version := moduleVersion()
	if version == "" {
		return errors.New("Oriel's source is gone and it has no published version to build from")
	}
	// A cross-compiled install lands in GOPATH/bin/linux_<arch>.
	gopath := filepath.Join(filepath.Dir(dst), "gopath")
	env = append(env, "GOPATH="+gopath)
	cmd := exec.Command(goBin, "install", helperPkg+"@"+version)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go install %s@%s: %w: %s", helperPkg, version, err, strings.TrimSpace(string(out)))
	}
	return os.Rename(filepath.Join(gopath, "bin", "linux_"+runtime.GOARCH, "oriel-sandbox"), dst)
}

// sourceDir is the sandbox module's root this binary was compiled from, ""
// when it is no longer on disk.
func sourceDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(file) {
		return ""
	}
	dir := filepath.Join(filepath.Dir(file), "..")
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return ""
	}
	return dir
}

// moduleVersion is the sandbox module's version in this binary, "" when it
// is not one `go install` can fetch.
func moduleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	v := ""
	if bi.Main.Path == sandboxModule {
		v = bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path == sandboxModule {
			v = d.Version
		}
	}
	if v == "" || v == "(devel)" || strings.Contains(v, "+dirty") {
		return ""
	}
	return v
}

// findGo is the go command, looked for beyond PATH since a dashboard started
// from the desktop has a bare one.
func findGo() (string, error) {
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/usr/local/go/bin/go", "/opt/homebrew/bin/go", "/usr/local/bin/go", filepath.Join(home, "go", "bin", "go")} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("go is not installed, so Oriel cannot build its Linux binary")
}

// linuxOrielHealth is ok when the Linux build is current; otherwise it starts
// the build and reports it.
func linuxOrielHealth() (Problem, bool) {
	if linuxOrielCurrent() {
		return Problem{}, true
	}
	orielMu.Lock()
	defer orielMu.Unlock()
	if !orielBuilding {
		orielBuilding = true
		go func() {
			_, err := ensureLinuxOriel()
			orielMu.Lock()
			orielBuilding, orielErr = false, err
			orielMu.Unlock()
		}()
	}
	if orielErr != nil {
		return Problem{Code: "oriel-linux-failed", Blocking: true, Message: "Building Oriel's sandbox helper failed: " + orielErr.Error(),
			Fix: "Oriel retries on the next check; or build one yourself with `GOOS=linux CGO_ENABLED=0 go build -o " + linuxOriel() + " " + helperPkg + "` and set `ORIEL_SANDBOX_BIN` to it."}, false
	}
	return Problem{Code: "oriel-linux-building", Message: "Building Oriel's sandbox helper — the first sandboxed launch waits for it"}, false
}
