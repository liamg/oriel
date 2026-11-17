package runner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// An engine whose CLI isn't on this machine is missing, not down: the health check tells them apart by the error,
// whatever its words.
func TestHealthSaysAMissingEngineIsMissing(t *testing.T) {
	ctx := container.WithEngine(context.Background(), container.Engine{Bin: filepath.Join(t.TempDir(), "docker")})
	p, ok := sandbox.Blocking(Health(ctx, sandbox.Config{}, "install it", nil, &Builds{}))
	if !ok || p.Code != "engine-missing" {
		t.Errorf("health = %+v", p)
	}
}

// A missing base image starts one build; a check while it runs says it's building; a build that failed is reported
// once, and the next check builds again. The builds are their owner's, so a test waits for them and Close stops them.
func TestBaseImageBuildsAreTheOwners(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := container.WithEngine(context.Background(), container.Engine{Bin: filepath.Join(t.TempDir(), "docker")})
	b := &Builds{}
	t.Cleanup(b.Close)
	if p, ok := b.base(ctx); ok || p.Code != "image-building" {
		t.Fatalf("a missing image: %+v", p)
	}
	b.Wait()
	if p, ok := b.base(ctx); ok || p.Code != "image-failed" {
		t.Fatalf("after a build with no engine: %+v", p)
	}
	if p, _ := b.base(ctx); p.Code != "image-building" {
		t.Errorf("the check after a failure didn't build again: %+v", p)
	}
	b.Wait()
}
