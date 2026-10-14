package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// sessionWait is how long a launch waits for the session to open its sockets.
const sessionWait = 10 * time.Second

// hostRunDir is where the egress sidecar sees RunDir off Linux, for the
// session's port.
const hostRunDir = "/run/oriel-host"

// RunVolumeName is root's in-memory volume holding the sandbox's session
// sockets off Linux: the sidecar relays them to the session's port.
func RunVolumeName(root string) string { return Name(root) + "-run" }

// runMounts give a container root's session sockets at ContainerRunDir. On
// Linux that is RunDir itself. Elsewhere it is a volume the sidecar fills,
// and the sidecar also gets RunDir for the session's port.
func runMounts(ctx context.Context, root string, id sandbox.Identity, sidecar bool) ([]container.Mount, error) {
	dir := sandbox.RunDir(root)
	if sandbox.SocketsMount() {
		if err := waitFor(filepath.Join(dir, "oriel.sock"), sessionWait); err != nil {
			return nil, err
		}
		return []container.Mount{{Source: dir, Target: sandbox.ContainerRunDir}}, nil
	}
	if err := waitFor(sandbox.PortPath(root), sessionWait); err != nil {
		return nil, err
	}
	if err := container.EnsureTmpfsVolume(ctx, RunVolumeName(root), id.UID, id.GID); err != nil {
		return nil, err
	}
	m := []container.Mount{{Source: RunVolumeName(root), Target: sandbox.ContainerRunDir, Volume: true}}
	if sidecar {
		m = append(m, container.Mount{Source: dir, Target: hostRunDir, ReadOnly: true})
	}
	return m, nil
}

// waitFor waits for the session to make path.
func waitFor(path string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the session has not opened %s: is it running?", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
