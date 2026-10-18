// Package sysload reads how busy this machine is: CPU across all cores, and
// memory in use. The HUD and the statusline show it.
package sysload

import (
	"bufio"
	"runtime"
	"strconv"
	"strings"
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

// Read samples the machine.
func Read() Reading {
	r := Reading{Cores: runtime.NumCPU()}
	cpu, err := readCPU()
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

// parseVMStat reads macOS's vm_stat: in use is active, wired and compressed.
func parseVMStat(s string) (used uint64, ok bool) {
	page := uint64(4096)
	var pages uint64
	for line := range strings.Lines(s) {
		if i := strings.Index(line, "page size of "); i >= 0 {
			f := strings.Fields(line[i+len("page size of "):])
			if len(f) > 0 {
				if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
					page = n
				}
			}
			continue
		}
		name, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Pages active", "Pages wired down", "Pages occupied by compressor":
			n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(val), "."), 10, 64)
			if err != nil {
				return 0, false
			}
			pages += n
			ok = true
		}
	}
	return pages * page, ok
}
