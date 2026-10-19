package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// engineHost is a home directory and a PATH holding only the given CLIs, with no DOCKER_HOST.
func engineHost(t *testing.T, clis ...string) string {
	t.Helper()
	home, bin := t.TempDir(), t.TempDir()
	for _, c := range clis {
		if err := os.WriteFile(filepath.Join(bin, c), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	old := dockerSockets
	dockerSockets = []string{"var-run/docker.sock", ".docker/run/docker.sock"}
	t.Cleanup(func() { dockerSockets = old })
	return home
}

func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func available(e Engine) bool { return e.Available() == nil }

// With only OrbStack, docker's default socket is OrbStack's: OrbStack is offered, Docker isn't.
func TestEnginesOrbStackOnly(t *testing.T) {
	home := engineHost(t, "docker")
	sock := filepath.Join(home, ".orbstack/run/docker.sock")
	touchFile(t, sock)
	if err := os.MkdirAll(filepath.Join(home, "var-run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sock, filepath.Join(home, "var-run/docker.sock")); err != nil {
		t.Fatal(err)
	}
	if !available(OrbStack) || available(Docker) || available(Colima) || available(Podman) {
		t.Errorf("orbstack %v, docker %v, colima %v, podman %v", OrbStack.Available(), Docker.Available(), Colima.Available(), Podman.Available())
	}
	e, _ := OrbStack.Find()
	if e.Host != sock || !e.VM || e.Bin != "docker" {
		t.Errorf("orbstack's engine = %+v", e)
	}
}

// OrbStack without Docker's CLI on PATH uses the copy it ships.
func TestEnginesOrbStackOwnCLI(t *testing.T) {
	home := engineHost(t)
	touchFile(t, filepath.Join(home, ".orbstack/run/docker.sock"))
	touchFile(t, filepath.Join(home, ".orbstack/bin/docker"))
	e, err := OrbStack.Find()
	if err != nil || e.Bin != filepath.Join(home, ".orbstack/bin/docker") {
		t.Errorf("orbstack's engine = %+v, %v", e, err)
	}
}

// Docker Desktop and Colima side by side are both offered.
func TestEnginesDockerAndColima(t *testing.T) {
	home := engineHost(t, "docker")
	touchFile(t, filepath.Join(home, ".docker/run/docker.sock"))
	touchFile(t, filepath.Join(home, ".colima/default/docker.sock"))
	if !available(Docker) || !available(Colima) || available(OrbStack) {
		t.Errorf("docker %v, colima %v, orbstack %v", Docker.Available(), Colima.Available(), OrbStack.Available())
	}
}

// A docker CLI with no daemon is not Docker; DOCKER_HOST says there is one.
func TestEnginesDockerNeedsADaemon(t *testing.T) {
	engineHost(t, "docker", "podman")
	if available(Docker) || !available(Podman) {
		t.Errorf("docker %v, podman %v", Docker.Available(), Podman.Available())
	}
	t.Setenv("DOCKER_HOST", "tcp://build:2375")
	if !available(Docker) {
		t.Errorf("docker with DOCKER_HOST: %v", Docker.Available())
	}
}
