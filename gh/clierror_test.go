package gh

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// A gh command's failure reads as GitHub's refusal, with its status, however far it's wrapped; gh not running at all
// is no refusal.
func TestCLIError(t *testing.T) {
	cmd := exec.Command("sh", "-c", `echo "gh: Not Found (HTTP 404)" >&2; exit 1`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := CLIError(cmd.Run(), nil, stderr.String())
	if !NotFound(fmt.Errorf("gh api: %w", err)) {
		t.Errorf("a 404 wrapped: %v", err)
	}
	if e := err.Error(); e != "GitHub: Not Found (HTTP 404)" {
		t.Errorf("said %q", e)
	}
	missing := CLIError(exec.Command("/no/such/gh").Run(), nil, "")
	var ge *Error
	if errors.As(missing, &ge) || NotFound(missing) {
		t.Errorf("gh not found reads as GitHub's answer: %v", missing)
	}
}
