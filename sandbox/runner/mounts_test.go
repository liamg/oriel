package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

func TestMountsShareAttachments(t *testing.T) {
	if !SocketsMount(container.Docker) {
		t.Skip("needs the session socket on the host")
	}
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	run := sandbox.RunDir(root)
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "oriel.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Mounts(root, root, sandbox.Identity{Home: "/home/u", UID: 501}, "/bin/claude", "/bin/oriel", "")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := sandbox.StateDir(root)
	want := filepath.Join(state, "attachments")
	for _, mt := range m {
		if mt.Target == want {
			if mt.Source != want || !mt.ReadOnly {
				t.Fatalf("attachments mounted as %+v", mt)
			}
			return
		}
	}
	t.Fatalf("no mount for %s in %+v", want, m)
}

func TestMountsShareGoCache(t *testing.T) {
	if !SocketsMount(container.Docker) {
		t.Skip("needs the session socket on the host")
	}
	t.Setenv("HOME", t.TempDir())
	id := sandbox.Identity{Home: "/home/u", UID: 501}
	var sources []string
	for range 2 {
		root := t.TempDir()
		if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sandbox.RunDir(root), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sandbox.SocketPath(root), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := Mounts(root, root, id, "/bin/claude", "/bin/oriel", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, mt := range m {
			if mt.Target == GoCacheDir {
				if mt.ReadOnly {
					t.Fatalf("go cache mounted read-only: %+v", mt)
				}
				sources = append(sources, mt.Source)
			}
		}
	}
	if len(sources) != 2 || sources[0] != sources[1] {
		t.Fatalf("go cache sources %v, want one shared dir", sources)
	}
	if fi, err := os.Stat(sources[0]); err != nil || !fi.IsDir() {
		t.Fatalf("go cache dir: %v", err)
	}
	env := agentEnv(t.TempDir(), id, sandbox.LaunchOptions{Environ: []string{"GOMODCACHE=/host/mod"}})
	if env["GOMODCACHE"] != GoCacheDir+"/mod" || env["GOCACHE"] != GoCacheDir+"/build" {
		t.Fatalf("GOMODCACHE=%q GOCACHE=%q", env["GOMODCACHE"], env["GOCACHE"])
	}
}
