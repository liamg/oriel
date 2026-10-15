package nativebuild

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, src string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseWith(t *testing.T) {
	for in, want := range map[string]Module{
		"github.com/someone/oriel-emacs@v1.2": {Path: "github.com/someone/oriel-emacs", Version: "v1.2"},
		"github.com/someone/oriel-emacs":      {Path: "github.com/someone/oriel-emacs"},
		"example.com/p=/src/p":                {Path: "example.com/p", Dir: "/src/p"},
	} {
		if got, err := ParseWith(in); err != nil || got != want {
			t.Errorf("ParseWith(%q) = %+v, %v", in, got, err)
		}
	}
	if _, err := ParseWith("@v1"); err == nil {
		t.Error("no path")
	}
}

// plugins.md: "oriel build --with github.com/someone/oriel-emacs@v1.2 compiles a custom oriel binary with those
// packages linked in, the way xcaddy does for Caddy": a main that runs oriel's Main and imports each package for its
// init, and a go.mod replacing what comes from local directories (a checkout's go.work modules, each --with=dir).
func TestMake(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.work"), "go 1.26.0\n\nuse (\n\t./core\n\t./cmd/oriel // the app\n)\n")
	write(t, filepath.Join(root, "core", "go.mod"), "module github.com/liamg/oriel/core\n")
	write(t, filepath.Join(root, "cmd", "oriel", "go.mod"), "module github.com/liamg/oriel/cmd/oriel\n")
	emacs := t.TempDir()
	write(t, filepath.Join(emacs, "go.mod"), "module github.com/someone/oriel-emacs\n")
	p, err := Make(Spec{Base: Module{Dir: root}, With: []Module{{Path: "github.com/someone/oriel-emacs", Dir: emacs},
		{Path: "github.com/x/vim/keys", Version: "v0.3.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"github.com/liamg/oriel/cmd/oriel => " + filepath.Join(root, "cmd", "oriel"),
		"github.com/liamg/oriel/core => " + filepath.Join(root, "core"), "github.com/someone/oriel-emacs => " + emacs,
		"\tgithub.com/liamg/oriel/core v0.0.0\n"} {
		if !strings.Contains(string(p.GoMod), want) {
			t.Errorf("go.mod lacks %q:\n%s", want, p.GoMod)
		}
	}
	for _, want := range []string{`oriel "github.com/liamg/oriel/cmd/oriel/cli"`, `_ "github.com/someone/oriel-emacs"`,
		`_ "github.com/x/vim/keys"`, "func main() { oriel.Main() }"} {
		if !strings.Contains(string(p.MainGo), want) {
			t.Errorf("main.go lacks %q:\n%s", want, p.MainGo)
		}
	}
	if strings.Join(p.Get, " ") != "github.com/x/vim/keys@v0.3.0" {
		t.Errorf("go get: %q", p.Get)
	}
	if p, _ := Make(Spec{Base: Module{Path: "github.com/liamg/oriel/cmd/oriel", Version: "v0.9.0"}}); strings.Join(p.Get, " ") !=
		"github.com/liamg/oriel/cmd/oriel@v0.9.0" {
		t.Errorf("a released base: %q", p.Get)
	}
}

// Live: a build with a stand-in base (its Main lists what registered) and a plugin from a directory, offline.
// The binary it writes runs the plugin's init.
func TestBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	goCmd := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := exec.LookPath(goCmd); err != nil {
		t.Skip(err)
	}
	base, plug := t.TempDir(), t.TempDir()
	write(t, filepath.Join(base, "go.mod"), "module example.com/base\n\ngo 1.26.0\n")
	write(t, filepath.Join(base, "cli", "cli.go"), `package cli

import "fmt"

var natives []string

func Register(name string) { natives = append(natives, name) }

func Main() { fmt.Println("native:", natives) }
`)
	write(t, filepath.Join(plug, "go.mod"), "module example.com/emacs\n\ngo 1.26.0\n\nrequire example.com/base v0.0.0\n")
	write(t, filepath.Join(plug, "emacs.go"), `package emacs

import "example.com/base/cli"

func init() { cli.Register("emacs") }
`)
	out := filepath.Join(t.TempDir(), "oriel")
	var log strings.Builder
	err := Build(context.Background(), Spec{Main: "example.com/base/cli", Base: Module{Dir: base},
		With: []Module{{Path: "example.com/emacs", Dir: plug}}, Output: out, Go: goCmd, Env: []string{"GOPROXY=off"}}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	got, err := exec.Command(out).Output()
	if err != nil || strings.TrimSpace(string(got)) != "native: [emacs]" {
		t.Errorf("the built binary: %q, %v", got, err)
	}
}
