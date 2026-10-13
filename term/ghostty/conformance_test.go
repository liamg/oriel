package ghostty

import (
	"testing"

	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/term/vttest"
)

func newEmulator(t testing.TB, backend string) func(cols, rows int, reply func([]byte)) term.Emulator {
	return func(cols, rows int, reply func([]byte)) term.Emulator {
		e, err := New(cols, rows, Options{Reply: reply, Backend: backend})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
}

// TestConformance runs the vttest cases in term/testdata (bytes in, the screen and state they cite out) against every
// core built in, so native and wasm can't drift apart.
func TestConformance(t *testing.T) {
	for _, be := range backends() {
		t.Run(be, func(t *testing.T) {
			for _, c := range vttest.Load(t, "../testdata/*.vt") {
				t.Run(c.Name, func(t *testing.T) { c.Run(t, newEmulator(t, be)) })
			}
		})
	}
}
