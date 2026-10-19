package app

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/plugin/ossandbox"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it, and the sandbox backends
// this machine offers.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	availableBackends = func() []string { return []string{"docker", "seatbelt"} }
	os.Exit(m.Run())
}
