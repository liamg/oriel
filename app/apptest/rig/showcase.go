package rig

import (
	"context"

	"github.com/liamg/oriel/registry"
)

// ShowExplainer answers like an agent would, by what it's asked about and how deep.
func ShowExplainer(_ context.Context, r registry.ExplainRequest) (string, error) {
	switch {
	case r.Kind == "test-failure":
		return "`RoundHalfEven(-3.5)` returned **-3**, the test wants **-4**.\n\n" +
			"On a tie the code adds `sign(v)` only when the integer part is odd, but `IntPart()` of a negative " +
			"truncates towards zero, so `-3` reads as odd and the tie goes the wrong way. Compare `t.Abs()` instead.", nil
	case r.Kind == "diagnostic":
		return "`greting` is a typo: nothing in package `greet` declares it, so the build stops here. You meant `greeting`, as on line 9.", nil
	case r.Kind == "ci-failure":
		return "- `TestUsageBackoff` wants `Retry-After: 2`, gets `1`.\n" +
			"- The reset is rounded **down** to whole seconds.\n- Round up with `math.Ceil` in `retryAfter()`.", nil
	case r.Depth == registry.ExplainDetailed:
		return "On a tie (`diff == half`) the old code always floored, which biased totals downwards.\n\n" +
			"Now it looks at the integer part: if it's **even** the tie rounds towards it (`t.Shift(-p)`), otherwise it " +
			"steps away from zero with `sign(v)`. That's banker's rounding: over many invoice lines the ups and downs cancel.\n\n" +
			"`diff` is worked out once above the switch, so the comparison and this branch agree.", nil
	case r.Depth == registry.ExplainWhy:
		return "Added in **round half to even** (Claude, this branch) for ENG-2231: finance reported month totals drifting by a cent " +
			"against the ledger. The thread on L12 settled on half-to-even over half-up.", nil
	}
	return "On an exact tie this rounds to the **even** neighbour: `2.5 → 2`, `3.5 → 4`. Before, every tie went down " +
		"(`t.Floor()`), which skewed sums of many lines.", nil
}
