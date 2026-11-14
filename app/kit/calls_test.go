package kit

import (
	"reflect"
	"testing"

	"github.com/liamg/oriel/registry"
)

// Then is what CallsOf reads: a list of {command, args} as Lua and JSON send it. CallsOf also takes the Go form,
// []Call, as it is.
func TestThenRoundTrips(t *testing.T) {
	calls := []Call{{Command: "request.open", Args: registry.Args{"thread": "t1"}}, {Command: "ship.pr.open"}}
	for _, v := range []any{Then(calls...), calls} {
		got, err := CallsOf(v)
		if err != nil || !reflect.DeepEqual(got, calls) {
			t.Errorf("CallsOf(%#v) = %+v, %v", v, got, err)
		}
	}
	if _, err := CallsOf([]any{map[string]any{"args": 1}}); err == nil {
		t.Error("a call with no command reads")
	}
}
