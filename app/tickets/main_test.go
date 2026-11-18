package tickets_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/sandbox/ossandbox"
)

// TestMain fixes the time zone, and runs the sandbox's helper when the sandbox starts this binary as one.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}
