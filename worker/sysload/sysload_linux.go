package sysload

import (
	"errors"
	"os"
	"sync"
	"time"
)

// stale is how old the previous sample may be before a fresh pair is taken:
// a difference over longer than this is not "now".
const stale = 30 * time.Second

var (
	mu   sync.Mutex
	last cpuTimes
	at   time.Time
)

// readCPU is the busy share since the previous call, or over a short pause
// when there is none recent enough.
func readCPU() (float64, error) {
	mu.Lock()
	defer mu.Unlock()
	now, err := procStat()
	if err != nil {
		return 0, err
	}
	if time.Since(at) > stale {
		last = now
		time.Sleep(250 * time.Millisecond)
		if now, err = procStat(); err != nil {
			return 0, err
		}
	}
	p := busyPercent(last, now)
	last, at = now, time.Now()
	return p, nil
}

func procStat() (cpuTimes, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, err
	}
	t, ok := parseProcStat(string(b))
	if !ok {
		return cpuTimes{}, errors.New("cannot read /proc/stat")
	}
	return t, nil
}

func readMem() (used, total uint64, err error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	used, total, ok := parseMeminfo(string(b))
	if !ok {
		return 0, 0, errors.New("cannot read /proc/meminfo")
	}
	return used, total, nil
}
