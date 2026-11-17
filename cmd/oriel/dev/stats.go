package dev

import (
	"fmt"
	"slices"
	"time"
)

// Durations collects samples and reports percentiles.
type Durations []time.Duration

// Percentile returns the p-th percentile (nearest rank), or 0 with no samples.
func (d Durations) Percentile(p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := slices.Clone(d)
	slices.Sort(s)
	i := int(p/100*float64(len(s))+0.5) - 1
	return s[max(0, min(i, len(s)-1))]
}

func (d Durations) String() string {
	return fmt.Sprintf("n=%d p50=%s p99=%s max=%s", len(d), ms(d.Percentile(50)), ms(d.Percentile(99)), ms(d.Percentile(100)))
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000) }
