package lang_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/lang"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/testrun"
)

// A run with a long output builds only the output lines in view, and scrolls to the rest.
func TestTestsOutputBuildsOnlyItsLinesInView(t *testing.T) {
	s, _ := testsScene(t)
	var out []string
	for i := range 20_000 {
		out = append(out, fmt.Sprintf("output line %d", i))
	}
	testsOf(s).History().Add(&testrun.Run{ID: "long", Start: time.Now(), Status: testrun.StatusPassed,
		Tests: []*testrun.Result{{Package: "example.com/pricing", Test: "TestLong", Status: testrun.StatusPassed, Output: out}}})
	s.Run("view.open", registry.Args{"id": lang.TestsView, "place": "dock", "edge": "bottom", "size": 320})
	s.Frame()
	s.ClickOn("whole run")
	s.Frame()
	s.Frame()
	built := strings.Count(s.FullText(), "output line ")
	if built == 0 || built > 200 {
		t.Errorf("built %d of 20000 output lines", built)
	}
}
