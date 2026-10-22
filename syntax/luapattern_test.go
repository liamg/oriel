package syntax

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// luaCases are expectations for string.find(subject, pattern) ~= nil, taken from nvim's Lua (the oracle):
// ORIEL_ORACLE=1 checks them against it again.
var luaCases = []struct {
	pattern, subject string
	want             bool
}{
	{"^[A-Z][A-Z_0-9]*$", "MAX_SIZE", true},
	{"^[A-Z][A-Z_0-9]*$", "MaxSize", false},
	{"^[A-Z]", "abc", false},
	{"^_", "_private", true},
	{"^%u", "Upper", true},
	{"^%l", "Upper", false},
	{"%d+", "abc123", true},
	{"^%d+$", "12a", false},
	{"^#!", "#!/bin/sh", true},
	{"^%s*$", "   ", true},
	{"^%s*$", " x ", false},
	{"^%-%-%-", "---@param", true},
	{"^[%a_][%w_]*$", "snake_case1", true},
	{"^[%a_][%w_]*$", "1bad", false},
	{"%.", "a.b", true},
	{"^.$", "x", true},
	{"^%x+$", "1aF", true},
	{"^%x+$", "1aG", false},
	{"a-b", "ab", true},
	{"a-b", "aaab", true},
	{"^a?b$", "b", true},
	{"^a?b$", "aab", false},
	{"%bxy", "axbxyy", true},
	{"%f[%w]%w+", " word", true},
	{"[^%s]", "  ", false},
	{"[^%s]", " x", true},
	{"^[%[%]]", "]", true},
	{"^%p", "!x", true},
	{"^%c", "\t", true},
	{"[a-c]+$", "xxabc", true},
	{"^0[xX]", "0x1F", true},
	{"^%*", "*", true},
	{"^[^%w]", "-", true},
	{"[]]", "]", true},
	{"^[]]", "]x", true},
	{"^%W+$", "+-+", true},
	{"$", "", true},
	{"^$", "a", false},
}

func TestLuaPatterns(t *testing.T) {
	for _, c := range luaCases {
		if got := compileLuaPattern(c.pattern).find(c.subject); got != c.want {
			t.Errorf("find(%q, %q) = %v, want %v", c.subject, c.pattern, got, c.want)
		}
	}
}

func TestLuaPatternsOracle(t *testing.T) {
	if os.Getenv("ORIEL_ORACLE") != "1" {
		t.Skip("oracle mode only (ORIEL_ORACLE=1)")
	}
	var script strings.Builder
	for _, c := range luaCases {
		script.WriteString("io.write(tostring(string.find(" + luaQuote(c.subject) + ", " + luaQuote(c.pattern) + ") ~= nil), '\\n')\n")
	}
	f := t.TempDir() + "/cases.lua"
	os.WriteFile(f, []byte(script.String()), 0o644)
	out, err := exec.Command("nvim", "--headless", "-l", f).CombinedOutput()
	if err != nil {
		t.Fatalf("nvim: %v\n%s", err, out)
	}
	for i, line := range strings.Fields(string(out)) {
		if want := luaCases[i].want; (line == "true") != want {
			t.Errorf("case %d (%q in %q): the table says %v, nvim says %s", i, luaCases[i].pattern, luaCases[i].subject, want, line)
		}
	}
}

func luaQuote(s string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\t", "\\t", "\n", "\\n").Replace(s) + "\""
}

// Patterns Lua refuses as malformed (string.find raises "malformed pattern (ends with '%')", "missing ']'",
// "unbalanced pattern", "missing '[' after '%f' in pattern") don't compile, so their predicate never matches.
func TestLuaPatternsMalformed(t *testing.T) {
	for _, p := range []string{"a%", "%", "[%a]%", "[a", "[a%", "[]", "%b(", "%fx"} {
		if lp := compileLuaPattern(p); lp != nil {
			t.Errorf("compileLuaPattern(%q) compiled", p)
		}
	}
}
