package app

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/plugin/ossandbox"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}
