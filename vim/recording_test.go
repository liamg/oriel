package vim_test

import "testing"

// repeat.txt: "q{0-9a-zA-Z"}	Record typed characters into register {0-9a-zA-Z"} (uppercase to append)." and
// "q	Stops recording." test_registers.vim shows the register as typed: 'recording @x'.
func TestRecordingRegister(t *testing.T) {
	e := multiEngine(t, "abc", 0, 0)
	if r, ok := e.Recording(); ok {
		t.Fatalf("recording @%c before q", r)
	}
	keys(e, "qa")
	if r, ok := e.Recording(); !ok || r != 'a' {
		t.Errorf("after qa: %q %v, want 'a' true", r, ok)
	}
	keys(e, "lq")
	if r, ok := e.Recording(); ok {
		t.Errorf("after q: still recording @%c", r)
	}
	keys(e, "qA")
	if r, ok := e.Recording(); !ok || r != 'A' {
		t.Errorf("after qA: %q %v, want 'A' true", r, ok)
	}
}
