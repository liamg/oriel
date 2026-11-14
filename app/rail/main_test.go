package rail_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/plugin/ossandbox"
)

// TestMain fixes the time zone, and the sandbox backends this machine offers.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}
