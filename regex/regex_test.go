package regex

import (
	"slices"
	"testing"
)

// Offsets are bytes into the text, across lines; groups that took no part are -1.
func TestFindIndex(t *testing.T) {
	re := MustCompile(`(?m)^(\w+)(?:=(\d+))?$`, Flags{})
	loc, err := re.FindIndex("# x\nkey=42\nflag", 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{4, 10, 4, 7, 8, 10}; !slices.Equal(loc, want) {
		t.Errorf("FindIndex = %v, want %v", loc, want)
	}
	loc, _ = re.FindIndex("# x\nkey=42\nflag", 10)
	if want := []int{11, 15, 11, 15, -1, -1}; !slices.Equal(loc, want) {
		t.Errorf("from 10: %v, want %v", loc, want)
	}
	if re.NumGroups() != 2 {
		t.Errorf("NumGroups %d", re.NumGroups())
	}
}

// Global matching and replacement as pcre2test's g and replace= do them: the cases are spec/pcre2/testoutput2's
// (/(?<=abc)(|def)/g,replace=<$0> and /a(?<ONE>b)c(?<TWO>d)e/g,replace=X$ONE+${TWO}Z), which vimregex's suite runs too.
func TestReplaceAll(t *testing.T) {
	for _, tt := range []struct{ pat, repl, in, want string }{
		{`(?<=abc)(|def)`, `<$0>`, "123abcxyzabcdef789abcpqr", "123abc<>xyzabc<><def>789abc<>pqr"},
		{`a(?<ONE>b)c(?<TWO>d)e`, `X$ONE+${TWO}Z`, "abcde-abcde-", "Xb+dZ-Xb+dZ-"},
		{`abc`, `X$$Z`, "123abc123abc123", "123X$Z123X$Z123"},
	} {
		got, _, err := MustCompile(tt.pat, Flags{}).ReplaceAll(tt.in, tt.repl)
		if err != nil || got != tt.want {
			t.Errorf("/%s/ → %q on %q: %q, %v; want %q", tt.pat, tt.repl, tt.in, got, err, tt.want)
		}
	}
	if _, _, err := MustCompile(`abc`, Flags{}).ReplaceAll("abc", "a$bad"); err == nil {
		t.Error("an unknown group in a replacement isn't an error")
	}
}

func TestUnsupported(t *testing.T) {
	_, err := Compile(`(a(?1)?b)`, Flags{})
	if !IsUnsupported(err) {
		t.Errorf("recursion: %v", err)
	}
	if _, err := Compile(`a{5,4}`, Flags{}); err == nil || IsUnsupported(err) {
		t.Errorf("a bad quantifier: %v", err)
	}
}
