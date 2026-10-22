package rig

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

// RoundBase is internal/pricing/round.go on main, in the review mockup's change.
const RoundBase = `package pricing

import "github.com/shopspring/decimal"

// roundHalfEven rounds v to p places, ties to even.
func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {
	scaled := v.Shift(p)
	r := scaled.Round(0)
	return r.Shift(-p)
}

// Money is an amount in a currency.
type Money struct {
	Currency string
	V        decimal.Decimal
}

var precision = map[string]int32{"USD": 2, "JPY": 0}

// RoundMinor rounds an amount to its currency's minor unit.
func RoundMinor(amt Money) Money {
	p := precision[amt.Currency]
	return Money{amt.Currency, roundHalfEven(amt.V, p)}
}
`

// RoundAgent is round.go after the agent's commit: roundHalfEven truncates and keeps the even neighbour on a tie.
var RoundAgent = strings.Replace(RoundBase, "\tr := scaled.Round(0)\n\treturn r.Shift(-p)\n",
	"\tt := scaled.Truncate(0)\n\tdiff := scaled.Sub(t).Abs()\n\tif diff.Equal(half) && t.IntPart()%2 == 0 {\n\t\treturn t.Shift(-p)\n\t}\n\treturn scaled.Round(0).Shift(-p)\n", 1)

// RoundWork is round.go in the work tree: RoundMinor reads the precision with a fallback, not committed yet.
var RoundWork = strings.Replace(RoundAgent, "\tp := precision[amt.Currency]\n", "\tp, ok := currency.Precision(amt.Currency)\n\tif !ok { p = 2 }\n", 1)

// DiffRepo is a branch off main whose agent commit changes roundHalfEven and adds currency.go, with RoundMinor
// changed on top and not committed: the review mockup's change.
func DiffRepo(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{"internal/pricing/round.go": RoundBase, "go.mod": "module example.com/pricing\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "pricing-rounding")
	r.Write(map[string]string{"internal/pricing/round.go": RoundAgent, "internal/pricing/currency.go": "package pricing\n\nvar half = 1\n"})
	r.Commit("Claude", "round half to even")
	r.Write(map[string]string{"internal/pricing/round.go": RoundWork})
	return r
}

// ChangedRepo is a repo with n files of 100 lines changed since its one commit, every fifth line.
func ChangedRepo(t *testing.T, n int) string {
	r := gittest.New(t)
	before, after := map[string]string{}, map[string]string{}
	for i := range n {
		var b, a strings.Builder
		for l := range 100 {
			fmt.Fprintf(&b, "line %d\n", l)
			if l%5 == 0 {
				fmt.Fprintf(&a, "line %d, changed\n", l)
			} else {
				fmt.Fprintf(&a, "line %d\n", l)
			}
		}
		p := fmt.Sprintf("pkg%d/f%03d.txt", i%20, i)
		before[p], after[p] = b.String(), a.String()
	}
	r.Write(before)
	r.Commit("Ada", "base")
	r.Write(after)
	return r.Dir
}
