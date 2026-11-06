package explain_test

import (
	"image/color"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
)

// A run of git bars lines up down the gutter whatever else a line's sign column holds: round.go's ? marks share
// lines 8 and 12 with the bar, and a run of block signs on consecutive lines is one unbroken bar.
func TestGitBarsLineUpBesideOtherSigns(t *testing.T) {
	s, _, _ := explainScene(t)
	s.SettleSigns() // the gutter's hunks are diffed off the loop
	marks := unitMarks(s)
	if _, ok := marks[8]; !ok {
		t.Fatalf("no ? on line 8 to share it with the bar: %v", marks)
	}
	bar := theme.Hex(kit.TokenColour(s.UI().Theme(), "git.modified", color.NRGBA{}))
	xs := map[string]int{}
	for _, l := range strings.Split(snapshot.List(s.List()), "\n") {
		if f := strings.Fields(l); len(f) == 5 && f[0] == "rect" && f[4] == bar && strings.HasPrefix(f[2], "2.1") {
			xs[strings.Split(f[1], ",")[0]]++
		}
	}
	if len(xs) != 1 {
		t.Errorf("the git bars sit at %d places across: %v", len(xs), xs)
	}
}
