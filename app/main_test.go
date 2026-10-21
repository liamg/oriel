package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/plugin/ossandbox"
)

// TestMain fixes the time zone for every test's times before any goroutine reads it, and the sandbox backends
// this machine offers, and keeps remote images off the network.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	availableBackends = func() []string { return []string{"docker", "seatbelt"} }
	fetchPicture = func(context.Context, string) ([]byte, error) { return nil, errors.New("tests don't use the network") }
	os.Exit(m.Run())
}
