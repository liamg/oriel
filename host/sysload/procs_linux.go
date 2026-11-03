package sysload

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is the kernel's USER_HZ, the unit of /proc's CPU times: 100 on every Linux Oriel runs on.
const clockTicks = 100

func procs() ([]Proc, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	page := uint64(os.Getpagesize())
	var out []Proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // gone since the listing
		}
		if p, ok := parsePidStat(string(b), page); ok && p.PID == pid {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("cannot read /proc")
	}
	return out, nil
}

// parsePidStat reads /proc/<pid>/stat: its pid, (comm), parent, CPU times and resident pages.
func parsePidStat(s string, page uint64) (Proc, bool) {
	open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || close < open {
		return Proc{}, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return Proc{}, false
	}
	f := strings.Fields(s[close+1:])
	if len(f) < 22 {
		return Proc{}, false
	}
	n := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
	ppid, _ := strconv.Atoi(f[1])
	return Proc{PID: pid, PPID: ppid, Command: s[open+1 : close], CPU: time.Duration(n(11)+n(12)) * time.Second / clockTicks,
		RSS: n(21) * page}, true
}

func details(ps []Proc) {
	for i := range ps {
		dir := "/proc/" + strconv.Itoa(ps[i].PID)
		if b, err := os.ReadFile(dir + "/cmdline"); err == nil && len(b) > 0 {
			ps[i].Command = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
		}
		if b, err := os.ReadFile(dir + "/environ"); err == nil {
			ps[i].Session = sessionIn(strings.Split(string(b), "\x00"))
		}
	}
}
