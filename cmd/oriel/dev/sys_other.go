//go:build !(linux || darwin)

package dev

import "time"

func cpuTime() time.Duration { return 0 }
func peakRSS() float64       { return -1 }
func currentRSS() float64    { return -1 }
