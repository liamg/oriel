package runner

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// minDocker is the oldest daemon with everything a sandbox uses.
const minDocker = 20

var (
	buildMu  sync.Mutex
	building bool
	buildErr error
)

// Health checks this machine can run sandboxed agents on ctx's engine, starting
// the base image build in the background if it is missing. fix says how to
// install or start the engine.
func Health(ctx context.Context, cfg sandbox.Config, fix string) []sandbox.Problem {
	var out []sandbox.Problem
	info, err := container.Ping(ctx)
	if err != nil {
		code := "engine-down"
		if strings.Contains(err.Error(), "not installed") {
			code = "engine-missing"
		}
		return append(out, sandbox.Problem{Code: code, Message: err.Error(), Fix: fix, Blocking: true})
	}
	major, _ := strconv.Atoi(strings.SplitN(info.Server, ".", 2)[0])
	if !container.EngineOf(ctx).Podman() && major > 0 && major < minDocker {
		out = append(out, sandbox.Problem{Code: "docker-old", Blocking: true,
			Message: fmt.Sprintf("Docker %s is too old for sandboxes", info.Server),
			Fix:     fmt.Sprintf("Upgrade to Docker %d or newer.", minDocker)})
	}
	if cfg.Image == "" {
		if p, ok := baseImage(ctx); !ok {
			out = append(out, p)
		}
	}
	if p, ok := helperProblem(); !ok {
		out = append(out, p)
	}
	return out
}

// baseImage is ok when the base image exists; otherwise it starts the build
// and reports it.
func baseImage(ctx context.Context) (sandbox.Problem, bool) {
	tag := container.BaseImage()
	if container.ImageExists(ctx, tag) {
		return sandbox.Problem{}, true
	}
	buildMu.Lock()
	defer buildMu.Unlock()
	if !building {
		building, buildErr = true, nil
		go func() {
			bctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			err := container.EnsureImage(bctx, tag, container.BaseDockerfile, BuildLog(tag))
			buildMu.Lock()
			building, buildErr = false, err
			buildMu.Unlock()
		}()
	}
	if buildErr != nil {
		return sandbox.Problem{Code: "image-failed", Blocking: true, Message: "Building the sandbox image failed: " + buildErr.Error(),
			Fix: "Check Docker can pull debian:bookworm-slim; Oriel retries on the next check."}, false
	}
	return sandbox.Problem{Code: "image-building", Message: "Building the sandbox base image — the first sandboxed launch waits for it"}, false
}
