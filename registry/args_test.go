package registry

import "testing"

// Float reads a number as Lua or JSON gives it, or as Go does.
func TestArgsFloat(t *testing.T) {
	a := Args{"lua": 0.4, "go": 3, "text": "7"}
	if a.Float("lua") != 0.4 || a.Float("go") != 3 || a.Float("text") != 0 || a.Float("none") != 0 {
		t.Errorf("Float: %v %v %v %v", a.Float("lua"), a.Float("go"), a.Float("text"), a.Float("none"))
	}
}

// Int and Trimmed read what an agent passes for a number or a name, quoted or not.
func TestArgsAsAnAgentPassesThem(t *testing.T) {
	a := Args{"lua": 4.0, "go": 3, "quoted": " 12 ", "word": "  api ", "num": 7.0, "bad": "x"}
	if a.Int("lua") != 4 || a.Int("go") != 3 || a.Int("quoted") != 12 || a.Int("bad") != 0 || a.Int("none") != 0 {
		t.Errorf("Int: %v %v %v %v %v", a.Int("lua"), a.Int("go"), a.Int("quoted"), a.Int("bad"), a.Int("none"))
	}
	if a.Trimmed("word") != "api" || a.Trimmed("num") != "7" || a.Trimmed("go") != "3" || a.Trimmed("none") != "" {
		t.Errorf("Trimmed: %q %q %q %q", a.Trimmed("word"), a.Trimmed("num"), a.Trimmed("go"), a.Trimmed("none"))
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
