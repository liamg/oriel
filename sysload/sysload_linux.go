package sysload

import (
	"errors"
	"os"
)

// tickCarry is nothing on Linux: /proc/stat's counters don't wrap.
type tickCarry struct{}

func ticks(*tickCarry) (cpuTimes, error) {
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
