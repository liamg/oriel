//go:build smoke

package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/term/pty"
)

// A real claude in a sandbox, watched for 25 seconds: it must reach its
// prompt, not a dialog. The screen goes to $ORIEL_SMOKE_OUT.
//
//	go test -tags 'docker smoke' -run TestClaudeStartsInSandbox ./sandbox/runner/
func TestClaudeStartsInSandbox(t *testing.T) {
	f := newFixture(t, sandbox.Config{NoEgressProxy: true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	argv, err := Launch(ctx, f.root, f.root, []string{"claude"}, sandbox.LaunchOpts{Environ: os.Environ(), Config: f.cfg})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	tty, err := pty.Start(cmd, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 64*1024)
		for {
			n, err := tty.Read(b)
			mu.Lock()
			out.Write(b[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(25 * time.Second)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	_ = tty.Close()
	mu.Lock()
	defer mu.Unlock()
	if p := os.Getenv("ORIEL_SMOKE_OUT"); p != "" {
		_ = os.WriteFile(p, out.Bytes(), 0o644)
	}
	screen := ansi.ReplaceAllString(out.String(), "")
	if !strings.Contains(screen, "❯") {
		t.Errorf("claude never reached its prompt:\n%s", screen)
	}
	for _, dialog := range []string{"Make auto mode", "trust this folder", "Choose the text style", "Bypass Permissions mode"} {
		if strings.Contains(screen, dialog) {
			t.Errorf("claude stopped at a dialog: %q", dialog)
		}
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07|\x1b[()][A-Z0-9]`)
