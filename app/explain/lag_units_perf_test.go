//go:build perf && !race

package explain_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
)

// unitsPanelScene is the Changed units panel open over dir's n changed files, once it's read them.
func unitsPanelScene(t *testing.T, dir string, n int) *apptest.Scene {
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.WaitGit()
	s.Run("explain.units", nil)
	s.Until(fmt.Sprintf("the Changed units panel to read %d changed files", n), func() bool {
		return strings.Contains(s.Text(), "The branch changes no code units.")
	})
	return s
}

// The Changed units panel's frame doesn't grow with the files the branch changes: what it shows is worked out when
// they change, not every frame.
func TestChangedUnitsFrameWithManyChangesBudget(t *testing.T) {
	f := apptest.MeanFrames(unitsPanelScene(t, rig.ChangedRepo(t, 5), 5), unitsPanelScene(t, rig.ChangedRepo(t, 4000), 4000))
	few, many := f[0], f[1]
	t.Logf("the Changed units panel's frame over 5 changed files: %v; over 4000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("4000 changed files make the Changed units panel's frame %v, against %v over 5", many, few)
	}
}
