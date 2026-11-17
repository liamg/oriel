package runner

import (
	"context"
	"errors"
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

// Builds builds the base image in the background, one build at a time, and keeps how the last one ended. Its owner
// closes it, which cancels a build under way and waits for it. The zero value is ready.
type Builds struct {
	mu       sync.Mutex
	building bool
	err      error // how the last build failed, until a check reports it
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// Close cancels a build under way and waits for it to stop.
func (b *Builds) Close() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
	b.wg.Wait()
}

// Wait waits for a build under way.
func (b *Builds) Wait() { b.wg.Wait() }

// base is ok when the base image exists. Otherwise it reports a build that failed since the last check, or starts
// one (on ctx's engine) if none is under way, and says it's building: a failure is reported once, and the next
// check builds again.
func (b *Builds) base(ctx context.Context) (sandbox.Problem, bool) {
	tag := container.BaseImage()
	if container.ImageExists(ctx, tag) {
		return sandbox.Problem{}, true
	}
	if b == nil {
		return sandbox.Problem{Code: "image-missing", Blocking: true, Message: "The sandbox base image isn't built"}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.building && b.err != nil {
		err := b.err
		b.err = nil
		return sandbox.Problem{Code: "image-failed", Blocking: true, Message: "Building the sandbox image failed: " + err.Error(),
			Fix: "Check Docker can pull debian:bookworm-slim; Oriel retries on the next check."}, false
	}
	if !b.building {
		if b.ctx == nil {
			b.ctx, b.cancel = context.WithCancel(context.Background())
		}
		b.building = true
		bctx, cancel := context.WithTimeout(container.WithEngine(b.ctx, container.EngineOf(ctx)), 20*time.Minute)
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			defer cancel()
			err := container.EnsureImage(bctx, tag, container.BaseDockerfile, BuildLog(tag))
			b.mu.Lock()
			b.building, b.err = false, err
			b.mu.Unlock()
		}()
	}
	return sandbox.Problem{Code: "image-building", Message: "Building the sandbox base image — the first sandboxed launch waits for it"}, false
}

// Health checks this machine can run sandboxed agents on ctx's engine, starting the base image build in the
// background with builds if it is missing. fix says how to install or start the engine.
func Health(ctx context.Context, cfg sandbox.Config, fix string, helper func() ([]byte, error), builds *Builds) []sandbox.Problem {
	var out []sandbox.Problem
	info, err := container.Ping(ctx)
	if err != nil {
		code := "engine-down"
		if errors.Is(err, container.ErrNotInstalled) {
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
		if p, ok := builds.base(ctx); !ok {
			out = append(out, p)
		}
	}
	if p, ok := helperProblem(helper); !ok {
		out = append(out, p)
	}
	return out
}
