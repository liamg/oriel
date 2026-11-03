package stats

import (
	"math"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
)

// The projection from fixture readings: 71% used, 1.1%/h over the last 24h, resetting in 44h 43m: it runs out
// after (1-.71)/.011 = 26.4 hours, Thu 14:38, 18h 21m before the reset.
func TestUsageProjection(t *testing.T) {
	reset := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	w := core.UsageWindow{Name: "weekly", Used: .71, Resets: reset, Length: 7 * 24 * time.Hour}
	hist := []core.ProviderUsage{
		{Sampled: rig.StatsNow.Add(-48 * time.Hour), Windows: []core.UsageWindow{{Name: "weekly", Used: .30, Resets: reset}}},
		{Sampled: rig.StatsNow.Add(-24 * time.Hour), Windows: []core.UsageWindow{{Name: "weekly", Used: .446, Resets: reset}}},
		{Sampled: rig.StatsNow, Windows: []core.UsageWindow{w}},
	}
	r := readWindow(rig.StatsNow, w, hist)
	if r.burn < .0109 || r.burn > .0111 {
		t.Errorf("burn = %.4f/h, want .011", r.burn)
	}
	out := rig.StatsNow.Add(time.Duration((1 - .71) / r.burn * float64(time.Hour)))
	if absDur(r.runsOut.Sub(out)) > time.Second || r.runsOut.Format("Mon 15:04") != "Thu 14:38" {
		t.Errorf("runs out %v, want %v", r.runsOut, out)
	}
	v, _ := r.verdict(rig.StatsNow, time.UTC)
	if v != "at this rate the weekly window runs out Thu 14:38 · 18h 21m before the reset" {
		t.Errorf("verdict = %q", v)
	}
	five := core.UsageWindow{Name: "5h", Used: .38, Resets: rig.StatsNow.Add(2*time.Hour + 23*time.Minute), Length: 5 * time.Hour}
	r = readWindow(rig.StatsNow, five, []core.ProviderUsage{
		{Sampled: rig.StatsNow.Add(-30 * time.Minute), Windows: []core.UsageWindow{{Name: "5h", Used: .31, Resets: five.Resets}}},
	})
	if v, _ := r.verdict(rig.StatsNow, time.UTC); r.burn < .139 || r.burn > .141 || v != "≈ 71% at the reset · won't run out" {
		t.Errorf("5h: burn %.3f, %q", r.burn, v)
	}
}

// A window's points go to the repos that used tokens while it rose; a rise with none behind it is outside Oriel.
func TestBurnedSpreadsPointsByTokens(t *testing.T) {
	opened := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	w := core.UsageWindow{Used: .30, Resets: opened.Add(5 * time.Hour), Length: 5 * time.Hour}
	r := stats.Report{Points: []time.Time{opened, opened.Add(time.Hour), opened.Add(2 * time.Hour)}, Groups: []stats.Group{
		{Key: "api", Used: []int64{300, 0, 0}}, {Key: "web", Used: []int64{100, 0, 0}},
	}}
	rows, outside := burned(r, w, []reading{{opened.Add(time.Hour), .2}, {opened.Add(150 * time.Minute), .3}})
	if math.Abs(rows[0].points-15) > 1e-9 || math.Abs(rows[1].points-5) > 1e-9 || math.Abs(outside-10) > 1e-9 {
		t.Errorf("rows %+v, outside %v; want api 15, web 5, 10 outside", rows, outside)
	}
}
