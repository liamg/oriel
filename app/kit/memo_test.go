package kit

import "testing"

// A memo makes each key's value once, and past its size drops the one used longest ago, not all of them.
func TestMemoKeepsTheMostRecentlyUsed(t *testing.T) {
	m := NewMemo[string, int](2)
	made := map[string]int{}
	get := func(k string) int { return m.Get(k, func() int { made[k]++; return len(k) }) }
	get("a")
	get("bb")
	get("a")
	get("ccc") // drops bb, used longest ago
	get("a")
	get("bb")
	if made["a"] != 1 || made["bb"] != 2 || made["ccc"] != 1 {
		t.Errorf("made %v, want a once, bb twice, ccc once", made)
	}
}
