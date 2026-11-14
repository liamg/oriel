// Package sysload reads how busy this machine is: CPU across all cores, and
// memory in use. The HUD and the statusline show it.
package sysload

import (
	"bufio"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reading is one sample of the machine.
type Reading struct {
	// CPU is the share of all cores busy since the previous sample, 0–100.
	CPU      float64 `json:"cpu"`
	Cores    int     `json:"cores"`
	MemUsed  uint64  `json:"memUsed"`
	MemTotal uint64  `json:"memTotal"`
	Err      string  `json:"err,omitempty"`
}

// Reader samples the machine, each CPU reading over the time since its last.
type Reader struct {
	mu    sync.Mutex
	last  cpuTimes
	at    time.Time
	carry tickCarry // the platform's counters carried past their wrap
}

// NewReader is a Reader with no reading yet.
func NewReader() *Reader { return &Reader{} }

// Read samples the machine.
func (x *Reader) Read() Reading {
	r := Reading{Cores: runtime.NumCPU()}
	cpu, err := x.readCPU()
	if err != nil {
		r.Err = err.Error()
	}
	r.CPU = min(100, max(0, cpu))
	used, total, err := readMem()
	if err != nil && r.Err == "" {
		r.Err = err.Error()
	}
	r.MemUsed, r.MemTotal = used, total
	return r
}

// cpuTimes are cumulative counters: busy and total ticks since boot.
type cpuTimes struct{ busy, total uint64 }

// stale is how old the previous sample may be before a fresh pair is taken:
// a difference over longer than this is not "now".
const stale = 30 * time.Second

// readCPU is the busy share since the previous call, or over a short pause
// when there is none recent enough.
func (x *Reader) readCPU() (float64, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	now, err := ticks(&x.carry)
	if err != nil {
		return 0, err
	}
	if time.Since(x.at) > stale {
		x.last = now
		time.Sleep(250 * time.Millisecond)
		if now, err = ticks(&x.carry); err != nil {
			return 0, err
		}
	}
	p := busyPercent(x.last, now)
	x.last, x.at = now, time.Now()
	return p, nil
}

func busyPercent(a, b cpuTimes) float64 {
	if b.total <= a.total || b.busy < a.busy {
		return 0
	}
	return float64(b.busy-a.busy) / float64(b.total-a.total) * 100
}

// parseProcStat reads the aggregate "cpu" line of /proc/stat.
func parseProcStat(s string) (cpuTimes, bool) {
	for line := range strings.Lines(s) {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var t cpuTimes
		for i, v := range f[1:] {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return cpuTimes{}, false
			}
			// guest and guest_nice are already counted in user and nice.
			if i >= 8 {
				break
			}
			t.total += n
			// idle and iowait are the time not spent working.
			if i != 3 && i != 4 {
				t.busy += n
			}
		}
		return t, true
	}
	return cpuTimes{}, false
}

// parseMeminfo reads /proc/meminfo: in use is everything not available.
func parseMeminfo(s string) (used, total uint64, ok bool) {
	var avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, haveTotal = n*1024, true
		case "MemAvailable:":
			avail, haveAvail = n*1024, true
		}
	}
	if !haveTotal || !haveAvail || avail > total {
		return 0, 0, false
	}
	return total - avail, total, true
}
