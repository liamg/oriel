package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExe(t *testing.T, path, script string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A server is found in the project's node_modules/.bin (its root's, or a workspace's above it) before PATH, and in
// the directory Oriel installed it into after; one found nowhere isn't.
func TestLocateLooksInTheProjectThenPathThenOrielsOwn(t *testing.T) {
	bin, installed := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin)
	spec := ServerSpec{Name: "tsls", Cmd: []string{"tsls", "--stdio"}}
	ws := t.TempDir()
	app := filepath.Join(ws, "packages", "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Locate(app, installed); ok {
		t.Fatal("found a server that isn't anywhere")
	}
	own := filepath.Join(spec.InstallDir(installed), "node_modules", ".bin", "tsls")
	writeExe(t, own, "")
	if p, ok := spec.Locate(app, installed); !ok || p != own {
		t.Errorf("Oriel's own: %q %v", p, ok)
	}
	if _, ok := spec.Locate(app, ""); ok {
		t.Error("found Oriel's own with no install directory")
	}
	global := filepath.Join(bin, "tsls")
	writeExe(t, global, "")
	if p, _ := spec.Locate(app, installed); p != global {
		t.Errorf("PATH before Oriel's own: %q", p)
	}
	workspace := filepath.Join(ws, "node_modules", ".bin", "tsls")
	writeExe(t, workspace, "")
	if p, _ := spec.Locate(app, installed); p != workspace {
		t.Errorf("the workspace's before PATH: %q", p)
	}
	project := filepath.Join(app, "node_modules", ".bin", "tsls")
	writeExe(t, project, "")
	if p, _ := spec.Locate(app, installed); p != project {
		t.Errorf("the project's first: %q", p)
	}
}

// Installing runs the spec's tool into the server's own directory, where Locate then finds it; with no tool on this
// machine, or no way to install, it says so.
func TestInstallIntoRunsTheToolIntoItsDirectory(t *testing.T) {
	bin, installed := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	spec := ServerSpec{Name: "tsls", Cmd: []string{"tsls"}, Install: npm("tsls-pkg", "typescript")}
	var out strings.Builder
	if err := spec.InstallInto(context.Background(), installed, &out); err == nil || !strings.Contains(err.Error(), "needs npm") {
		t.Errorf("without npm: %v", err)
	}
	writeExe(t, filepath.Join(bin, "npm"), `echo "$@" > "$5/args"
mkdir -p "$5/node_modules/.bin" && printf '#!/bin/sh\n' > "$5/node_modules/.bin/tsls" && chmod +x "$5/node_modules/.bin/tsls"
`)
	if err := spec.InstallInto(context.Background(), installed, &out); err != nil {
		t.Fatal(err, out.String())
	}
	args, _ := os.ReadFile(filepath.Join(spec.InstallDir(installed), "args"))
	if want := "install --no-fund --no-audit --prefix " + spec.InstallDir(installed) + " tsls-pkg typescript"; strings.TrimSpace(string(args)) != want {
		t.Errorf("npm %s, want %s", args, want)
	}
	if p, ok := spec.Locate(t.TempDir(), installed); !ok || !strings.HasPrefix(p, spec.InstallDir(installed)) {
		t.Errorf("after installing: %q %v", p, ok)
	}
	if err := (ServerSpec{Name: "clangd", Cmd: []string{"clangd"}}).InstallInto(context.Background(), installed, &out); err == nil {
		t.Error("installed a server with no way to")
	}
}
