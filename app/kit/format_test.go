package kit

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                "0s",
		400 * time.Microsecond:           "<1ms",
		4100 * time.Microsecond:          "4.1ms",
		1200 * time.Millisecond:          "1.2s",
		38 * time.Second:                 "38s",
		102 * time.Second:                "1m 42s",
		4 * time.Minute:                  "4m",
		65 * time.Minute:                 "1h 05m",
		2 * time.Hour:                    "2h",
		51 * time.Hour:                   "2d 3h",
		48 * time.Hour:                   "2d",
		-time.Second:                     "0s",
		3*time.Minute + 59*time.Second:   "3m 59s",
		23*time.Hour + 59*time.Minute:    "23h 59m",
		10*time.Second - time.Nanosecond: "10.0s",
	} {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{512: "512 B", 4300: "4.2 KB", 48 << 20: "48.0 MB", 11 << 30: "11.0 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}
