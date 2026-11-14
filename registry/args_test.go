package registry

import "testing"

// Float reads a number as Lua or JSON gives it, or as Go does.
func TestArgsFloat(t *testing.T) {
	a := Args{"lua": 0.4, "go": 3, "text": "7"}
	if a.Float("lua") != 0.4 || a.Float("go") != 3 || a.Float("text") != 0 || a.Float("none") != 0 {
		t.Errorf("Float: %v %v %v %v", a.Float("lua"), a.Float("go"), a.Float("text"), a.Float("none"))
	}
}

// Option is an option's value as the type asked for, and says when it isn't there as one.
func TestOption(t *testing.T) {
	r := New()
	r.Option(OptionSpec{Name: "x.on", Type: Bool, Default: true})
	if on, ok := Option[bool](r.Options(), "x.on", At{}); !on || !ok {
		t.Errorf("a bool = %v %v", on, ok)
	}
	if s, ok := Option[string](r.Options(), "x.on", At{}); s != "" || ok {
		t.Errorf("as a string = %q %v", s, ok)
	}
	if _, ok := Option[bool](r.Options(), "x.none", At{}); ok {
		t.Error("an unknown option is there")
	}
}
