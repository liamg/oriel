package nvim

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/vim/vimtest"
)

func requireOracle(t *testing.T) {
	if !vimtest.Oracle() {
		t.Skip("oracle mode is off (-oracle or ORIEL_ORACLE=1)")
	}
}

func TestOracleReadsEveryField(t *testing.T) {
	requireOracle(t)
	for _, c := range vimtest.Load(t, "testdata/*.vt") {
		t.Run(c.Name, func(t *testing.T) {
			c.RequireRef(t)
			c.Compare(t, vimtest.Run(t, New(), c))
		})
	}
}

// A wrong expectation must fail: the oracle is only useful if it disagrees.
func TestOracleCatchesWrongExpectations(t *testing.T) {
	requireOracle(t)
	cs, err := vimtest.Parse("wrong.vt", strings.NewReader(`=== wrong
ref:    oracle nvim v0.11.7
given:  |foo bar
keys:   cwxy<Esc>
expect: xy| bar
mode:   insert
regs:   "" = "bar"   "1 = "foo"
undo:   foo |bar
dot:    |foo bar
`))
	if err != nil {
		t.Fatal(err)
	}
	diffs := cs[0].Diff(vimtest.Run(t, New(), cs[0]))
	want := []string{"buffer", "mode", `register '"'`, "register '1'", "undo", "dot"}
	if len(diffs) != len(want) {
		t.Fatalf("got %d diffs, want %d: %q", len(diffs), len(want), diffs)
	}
	for i, w := range want {
		if !strings.HasPrefix(diffs[i], w) {
			t.Errorf("diff %d = %q, want it about %s", i, diffs[i], w)
		}
	}
}

func TestPendingKeysFail(t *testing.T) {
	requireOracle(t)
	e, err := start()
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.Feed(vimtest.ParseKeys("2")); err == nil || !strings.Contains(err.Error(), "waiting for more keys") {
		t.Errorf("feeding a lone count: got %v, want a waiting-for-keys error", err)
	}
}
