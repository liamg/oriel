package ghostty

import (
	"testing"

	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/term/vttest"
)

func newEmulator(t testing.TB) func(cols, rows int, reply func([]byte)) term.Emulator {
	return func(cols, rows int, reply func([]byte)) term.Emulator {
		e, err := New(cols, rows, Options{Reply: reply})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
}

// TestConformance runs the vttest cases in term/testdata: bytes in, the screen and state they cite out.
func TestConformance(t *testing.T) {
	for _, c := range vttest.Load(t, "../testdata/*.vt") {
		t.Run(c.Name, func(t *testing.T) { c.Run(t, newEmulator(t)) })
	}
}
