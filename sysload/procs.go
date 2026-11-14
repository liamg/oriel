package sysload

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SessionEnv is the variable a session's agent runs with, naming the session; SessionKeyEnv tells it from another
// Oriel's session of the same name.
const (
	SessionEnv    = "ORIEL_SESSION"
	SessionKeyEnv = "ORIEL_SESSION_KEY"
)

// Proc is a process as the OS counts it.
type Proc struct {
	PID, PPID int
	Command   string
	CPU       time.Duration // user and system time over its life
	RSS       uint64        // bytes resident
	Session   string        // its SessionEnv: the session it runs for, read by Family
	Key       string        // its SessionKeyEnv, read by Family
}

// Family is pid and every process under it, pid first, each with its command line and session.
func Family(pid int) ([]Proc, error) {
	all, err := procs()
	if err != nil {
		return nil, err
	}
	fam := descendants(all, pid)
	details(fam)
	return fam, nil
}

// Keyed is every process whose environment carries SessionKeyEnv=key, and what each started, but this one and
// the processes it runs under.
func Keyed(key string) ([]Proc, error) {
	all, err := procs()
	if err != nil {
		return nil, err
	}
	details(all)
	mine := map[int]bool{}
	by := map[int]Proc{}
	for _, p := range all {
		by[p.PID] = p
	}
	for pid := os.Getpid(); pid > 1 && !mine[pid]; pid = by[pid].PPID {
		mine[pid] = true
	}
	var out []Proc
	seen := map[int]bool{}
	for _, p := range all {
		if p.Key != key || seen[p.PID] {
			continue
		}
		for _, d := range descendants(all, p.PID) {
			if !seen[d.PID] && !mine[d.PID] {
				seen[d.PID] = true
				out = append(out, d)
			}
		}
	}
	return out, nil
}

// descendants is root and the processes under it in all, root first.
func descendants(all []Proc, root int) []Proc {
	kids := map[int][]int{}
	by := map[int]Proc{}
	for _, p := range all {
		kids[p.PPID] = append(kids[p.PPID], p.PID)
		by[p.PID] = p
	}
	var out []Proc
	seen := map[int]bool{}
	next := []int{root}
	for len(next) > 0 {
		pid := next[0]
		next = next[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if p, ok := by[pid]; ok {
			out = append(out, p)
		}
		next = append(next, kids[pid]...)
	}
	return out
}

// ProcLoad is a process's share of one core and its memory between two readings of its family.
type ProcLoad struct {
	Proc
	Share float64 // of one core, 0–1 each core it kept busy
}

// Loads is each process in now with its CPU since prev, over d; heaviest first, by CPU then memory.
func Loads(prev, now []Proc, d time.Duration) []ProcLoad {
	before := map[int]time.Duration{}
	for _, p := range prev {
		before[p.PID] = p.CPU
	}
	out := make([]ProcLoad, 0, len(now))
	for _, p := range now {
		l := ProcLoad{Proc: p}
		if was, ok := before[p.PID]; ok && d > 0 && p.CPU >= was {
			l.Share = float64(p.CPU-was) / float64(d)
		}
		out = append(out, l)
	}
	slices.SortStableFunc(out, func(a, b ProcLoad) int {
		return cmp.Or(cmp.Compare(b.Share, a.Share), cmp.Compare(b.RSS, a.RSS), cmp.Compare(a.PID, b.PID))
	})
	return out
}

// parseCPUTime reads ps's time: [[dd-]hh:]mm:ss[.cc].
func parseCPUTime(s string) (time.Duration, bool) {
	var days int
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	secs, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, false
	}
	total := time.Duration(secs * float64(time.Second))
	for i, unit := range []time.Duration{time.Minute, time.Hour} {
		if j := len(parts) - 2 - i; j >= 0 {
			n, err := strconv.Atoi(parts[j])
			if err != nil {
				return 0, false
			}
			total += time.Duration(n) * unit
		}
	}
	return total + time.Duration(days)*24*time.Hour, true
}

// sessionIn is SessionEnv's and SessionKeyEnv's values among words: an environment, or a command line with it after.
func sessionIn(words []string) (session, key string) {
	for _, w := range words {
		if v, ok := strings.CutPrefix(w, SessionEnv+"="); ok && session == "" {
			session = v
		}
		if v, ok := strings.CutPrefix(w, SessionKeyEnv+"="); ok && key == "" {
			key = v
		}
	}
	return session, key
}

// parseProcArgs reads macOS's KERN_PROCARGS2: argc, the executable's path, padding, the arguments, then the
// environment, each NUL-terminated.
func parseProcArgs(b []byte) (args, env []string) {
	if len(b) < 4 {
		return nil, nil
	}
	argc := int(binary.NativeEndian.Uint32(b))
	rest := b[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, nil
	}
	rest = bytes.TrimLeft(rest[i:], "\x00")
	for _, s := range strings.Split(string(rest), "\x00") {
		switch {
		case len(args) < argc:
			args = append(args, s)
		case s == "":
			return args, env
		default:
			env = append(env, s)
		}
	}
	return args, env
}
