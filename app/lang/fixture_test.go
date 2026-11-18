package lang_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/lsp/lsptest"
	"github.com/liamg/oriel/sandbox/ossandbox"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it, and runs the sandbox's helper
// or a fake language server when a test starts this binary as one.
func TestMain(m *testing.M) {
	ossandbox.Main()
	lsptest.ServeProcess()
	time.Local = time.UTC
	os.Exit(m.Run())
}
