package settings

import "testing"

// A map reads back as a Lua table, and as an inline TOML one.
func TestMapValues(t *testing.T) {
	if got := luaValue(map[string]string{"cache-1": "a", "db": "b"}); got != `{ ["cache-1"] = "a", db = "b" }` {
		t.Errorf("as Lua: %s", got)
	}
	if got := formatValue(map[string]string{"cache 1": "a", "db": "b"}); got != `{ "cache 1" = "a", db = "b" }` {
		t.Errorf("as TOML: %s", got)
	}
}
