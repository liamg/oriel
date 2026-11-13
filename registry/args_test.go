package registry

import "testing"

// Float reads a number as Lua or JSON gives it, or as Go does.
func TestArgsFloat(t *testing.T) {
	a := Args{"lua": 0.4, "go": 3, "text": "7"}
	if a.Float("lua") != 0.4 || a.Float("go") != 3 || a.Float("text") != 0 || a.Float("none") != 0 {
		t.Errorf("Float: %v %v %v %v", a.Float("lua"), a.Float("go"), a.Float("text"), a.Float("none"))
	}
}
