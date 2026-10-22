package perf

import (
	"hash/fnv"
	"runtime"
	"slices"
	"time"
)

// maxLoad bounds how much a run's time is scaled down for a loaded machine: past it the numbers mean nothing.
const maxLoad = 4

// Times runs step(0..n-1) in each of rounds rounds, with setup before each round, and answers each step's time:
// its best over the rounds. A step's time is the CPU its thread spent on it, so time other processes and tests
// held the CPU isn't counted; and it's scaled by a fixed piece of reference work run before every step, whose
// slowdown over its best is what a loaded machine costs a thread on its share of a core. A step slow in every
// round is still slow.
func Times(rounds, n int, setup func(), step func(i int)) []time.Duration {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	raw := make([][]time.Duration, rounds)
	ref := make([][]time.Duration, rounds)
	refBest := time.Duration(1<<63 - 1)
	buf := make([]byte, 16<<10)
	for r := range rounds {
		if setup != nil {
			setup()
		}
		runtime.GC()
		raw[r], ref[r] = make([]time.Duration, n), make([]time.Duration, n)
		for i := range n {
			t0 := threadTime()
			reference(buf)
			t1 := threadTime()
			step(i)
			raw[r][i], ref[r][i] = threadTime()-t1, t1-t0
			refBest = min(refBest, ref[r][i])
		}
	}
	out := make([]time.Duration, n)
	for i := range n {
		for r := range rounds {
			d := unloaded(raw[r][i], ref[r][i], refBest)
			if r == 0 || d < out[i] {
				out[i] = d
			}
		}
	}
	return out
}

// unloaded is raw, a step's time, scaled down by how much slower its reference work ran than at best.
func unloaded(raw, ref, refBest time.Duration) time.Duration {
	load := min(max(float64(ref)/float64(max(refBest, 1)), 1), maxLoad)
	return time.Duration(float64(raw) / load)
}

// Sampler is Times for a run whose steps aren't known up front: each Time is one step, timed and scaled the same way.
type Sampler struct {
	raw, ref []time.Duration
	refBest  time.Duration
	buf      []byte
}

// Time times step.
func (s *Sampler) Time(step func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if s.buf == nil {
		s.buf, s.refBest = make([]byte, 16<<10), time.Duration(1<<63-1)
	}
	t0 := threadTime()
	reference(s.buf)
	t1 := threadTime()
	step()
	s.raw, s.ref = append(s.raw, threadTime()-t1), append(s.ref, t1-t0)
	s.refBest = min(s.refBest, t1-t0)
}

// Times is each step's time, in the order they ran.
func (s *Sampler) Times() []time.Duration {
	out := make([]time.Duration, len(s.raw))
	for i := range out {
		out[i] = unloaded(s.raw[i], s.ref[i], s.refBest)
	}
	return out
}

// Mean is times' mean.
func Mean(times []time.Duration) time.Duration {
	var sum time.Duration
	for _, d := range times {
		sum += d
	}
	return sum / time.Duration(max(len(times), 1))
}

// Pct is the p-th percentile (0-100) of times.
func Pct(times []time.Duration, p int) time.Duration {
	s := slices.Clone(times)
	slices.Sort(s)
	return s[min(len(s)-1, len(s)*p/100)]
}

// reference is fixed work, CPU and memory, of tens of µs.
func reference(buf []byte) {
	h := fnv.New64a()
	for range 4 {
		h.Write(buf)
	}
	buf[0] = byte(h.Sum64())
}
