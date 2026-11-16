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
	p, ok := sandbox.Blocking(Health(ctx, sandbox.Config{}, "install it", nil))
	if !ok || p.Code != "engine-missing" {
		t.Errorf("health = %+v", p)
	}
}
