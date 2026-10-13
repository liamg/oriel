package app

import (
	"os"
	"testing"
	"time"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}
