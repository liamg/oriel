package statefile_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/kit/statefile"
)

// A budget's polls, on screen or behind, stretch by the strain the API's allowance puts on them.
func TestBudgetStretchesByTheStrain(t *testing.T) {
	b := statefile.Budget{PerHour: 1000, Strain: func() float64 { return 4 }}
	release := b.Use("prs", 5)
	defer release()
	if d := b.Paced(30 * time.Second); d != 2*time.Minute {
		t.Errorf("a poll on screen every %v, want 2m", d)
	}
	if d := b.Interval(2 * time.Minute); d != 8*time.Minute {
		t.Errorf("a poll behind every %v, want 8m", d)
	}
	plenty := statefile.Budget{Strain: func() float64 { return 0.5 }}
	if d := plenty.Paced(30 * time.Second); d != 30*time.Second {
		t.Errorf("with plenty left, a poll on screen every %v, want 30s", d)
	}
}
