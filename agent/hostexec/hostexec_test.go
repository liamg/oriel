package hostexec

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// os/exec: "it is incorrect to call Wait before all reads from the pipe have completed". A process that writes and
// exits at once (its output fitting the pipe) still has all of it read after it ends.
func TestOutputOutlivesAQuickExit(t *testing.T) {
	want := strings.Repeat("x", 4000)
	for range 50 {
		p, err := Exec{}.Start(context.Background(), exec.Command("sh", "-c", "printf %04000d 0 | tr 0 x"))
		if err != nil {
			t.Fatal(err)
		}
		p.Wait()
		got, _ := io.ReadAll(p.Stdout())
		if string(got) != want {
			t.Fatalf("read %d bytes of 4000 after the process ended", len(got))
		}
	}
}
