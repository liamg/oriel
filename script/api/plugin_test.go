package api

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/testhook"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/script"
)

type logHost struct{ plugin.Host }

func (logHost) Log() *slog.Logger { return slog.New(slog.DiscardHandler) }

// spinning is a module whose _initialize never returns.
var spinning = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: func()
	0x03, 0x02, 0x01, 0x00, // func 0
	0x07, 0x0f, 0x01, 0x0b, '_', 'i', 'n', 'i', 't', 'i', 'a', 'l', 'i', 'z', 'e', 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b, // loop br 0 end
}

// "The watchdog logs a call past 50 ms as slow and interrupts one past 500 ms (a config load gets
// 2 s)". Loading a module runs its _initialize, so it gets a load's budget.
func TestWASMLoadIsWatchdogged(t *testing.T) {
	testhook.Set(t, &script.LoadBudget, 100*time.Millisecond)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "spin.wasm"), spinning, 0o644)
	a := &APIs{Host: logHost{}, Plugin: plugin.Caller{ID: "acme/spin", Dir: dir}}
	done := make(chan error, 1)
	go func() {
		_, err := WASM{a}.Load("spin.wasm", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
			t.Errorf("a module that never finishes loading: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loading a module that never returns wasn't stopped")
	}
}
