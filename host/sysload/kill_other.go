//go:build !unix

package sysload

import "os"

// Kill ends each of ps at once.
func Kill(ps []Proc) {
	for _, p := range ps {
		if pr, err := os.FindProcess(p.PID); err == nil {
			_ = pr.Kill()
		}
	}
}
