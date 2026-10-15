package ghostty

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/liamg/oriel/tools/perf"
)

// mixedStream is terminal output as a session produces it: log lines, test results, coloured ls, git log and compiler
// errors, about 14 escape sequences per KB.
func mixedStream(size int) []byte {
	var b bytes.Buffer
	files := []string{"main.go", "README.md", "internal", "cmd", "go.mod", "Makefile", "vendor", "term_test.go"}
	for i := 0; b.Len() < size; i++ {
		switch i % 10 {
		case 0, 1, 2, 3:
			fmt.Fprintf(&b, "2026-10-13T20:%02d:%02d.%03dZ INFO  worker/session.go:%d request handled id=%d dur=%dms bytes=%d\r\n", i%60, i%60, i%1000, i%900, i, i%97, i*13%65536)
		case 4:
			fmt.Fprintf(&b, "\x1b[1;32mok\x1b[0m  \tgithub.com/liamg/oriel/pkg%d\t%d.%03ds\r\n", i%40, i%9, i%1000)
		case 5:
			fmt.Fprintf(&b, "\x1b[0m\x1b[01;34m%s\x1b[0m  \x1b[01;32m%s\x1b[0m  %s  %s\r\n", files[i%8], files[(i+1)%8], files[(i+2)%8], files[(i+3)%8])
		case 6:
			fmt.Fprintf(&b, "\x1b[33mcommit %040x\x1b[m\r\nAuthor: Someone <someone@example.com>\r\n\r\n    fix(term): handle case %d\r\n\r\n", i*2654435761, i)
		case 7:
			fmt.Fprintf(&b, "term/ghostty/ghostty.go:%d:%d: \x1b[1;31merror:\x1b[0m undefined: \x1b[1mfoo%d\x1b[0m\r\n", i%400, i%80, i)
		default:
			fmt.Fprintf(&b, "    %d | \tfor i := range lines { if lines[i].Dirty { redraw(i, lines[i].Cells) } }\r\n", i%1000)
		}
	}
	return b.Bytes()
}

// write feeds p in chunks of n, as reads from a PTY arrive.
func write(e *Terminal, p []byte, n int) {
	for len(p) > 0 {
		k := min(n, len(p))
		e.Write(p[:k])
		p = p[k:]
	}
}

func BenchmarkParse(b *testing.B) {
	p := mixedStream(8 << 20)
	for _, be := range backends() {
		for _, chunk := range []int{4 << 10, 64 << 10} {
			b.Run(fmt.Sprintf("%s/chunk=%dK", be, chunk>>10), func(b *testing.B) {
				e := mustNew(b, 120, 40, Options{Backend: be})
				write(e, p, chunk)
				b.SetBytes(int64(len(p)))
				b.ResetTimer()
				for range b.N {
					write(e, p, chunk)
				}
			})
		}
	}
}

// budget is the parse throughput _design/ide.md asks for. The native core, which the desktop app runs, must meet it.
const budget = 200e6

// wasmFloor is what the wasm core (the worker's, and any build without cgo) must keep: under wazero it parses about
// 80 MB/s of this stream, under budget, a deviation accepted in FG-4 for builds that can't link the native core.
const wasmFloor = 40e6

func TestParseThroughputBudget(t *testing.T) {
	perf.Timed(t)
	if testing.Short() || raceEnabled {
		t.Skip("throughput isn't measured with -short or -race")
	}
	p := mixedStream(32 << 20)
	for _, be := range backends() {
		t.Run(be, func(t *testing.T) {
			e := mustNew(t, 120, 40, Options{Backend: be})
			write(e, p[:4<<20], 64<<10)
			best := 0.0
			for range 3 {
				start := time.Now()
				write(e, p, 64<<10)
				best = max(best, float64(len(p))/time.Since(start).Seconds())
			}
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
