//go:build docker

package worker

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The real container: the agent's command runs inside it, behind the egress proxy.
//
//	go test -tags docker -run TestSandboxRunsInDocker ./worker/
func TestSandboxRunsInDocker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("no docker")
	}
	s := newSandboxRig(t)
	real := NewSandboxFactory(nil)
	s.f.Launch, s.f.Stop = real.Launch, real.Stop
	_, spec, b := s.sandboxed()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p, err := spec.Exec.Start(ctx, exec.Command("sh", "-c", `echo "$ORIEL_SANDBOX $HTTPS_PROXY"`))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(p.Stdout())
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if f := strings.Fields(string(out)); len(f) != 2 || f[0] != "1" || !strings.Contains(f[1], "-egress") {
		t.Errorf("in the container: %q", out)
	}
}
