//go:build perf

package ghostty

import (
	"testing"

	"github.com/liamg/oriel/tools/perf"
)

// budget is the parse throughput the terminal needs. The native core, which the desktop app runs, must meet it.
const budget = 200e6

// wasmFloor is what the wasm core (the worker's, and any build without cgo) must keep: under wazero it parses about
// 80 MB/s of this stream, under budget, an accepted deviation for builds that can't link the native core.
const wasmFloor = 40e6

func TestParseThroughputBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("throughput isn't measured with -short or -race")
	}
	p := mixedStream(32 << 20)
	for _, be := range backends() {
		t.Run(be, func(t *testing.T) {
			e := mustNew(t, 120, 40, Options{Backend: be})
			write(e, p[:4<<20], 64<<10)
			took := perf.Times(3, 1, nil, func(int) { write(e, p, 64<<10) })[0] // the best of three
			best := float64(len(p)) / took.Seconds()
			t.Logf("%s parse throughput %.0f MB/s (budget %.0f MB/s)", be, best/1e6, budget/1e6)
			want := budget
			if be == "wasm" {
				want = wasmFloor
			}
			if best < want {
				t.Fatalf("%s parse throughput %.0f MB/s, under %.0f MB/s", be, best/1e6, want/1e6)
			}
		})
	}
}
