package snippet

import (
	"reflect"
	"testing"
)

func vars(m map[string]string) Vars {
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

type ts = Tabstop

func r(a, b int) Range { return Range{Start: a, End: b} }

func TestParse(t *testing.T) {
	known := vars(map[string]string{"TM_FILENAME": "foo.txt", "TM_SELECTED_TEXT": "", "TM_LINE_NUMBER": "7"})
	tests := []struct {
		name, in string
		want     Snippet
	}{
		{
			// ref: lsp/language/completion.md#Tab stops
			// quote: "Use `$1`, `$2` to specify cursor locations. The number is the order in which tab stops will be visited, whereas `$0` denotes the final cursor position."
			"tab stops", "f($2, $1)$0",
			Snippet{"f(, )", []ts{{Index: 1, Ranges: []Range{r(4, 4)}}, {Index: 2, Ranges: []Range{r(2, 2)}}, {Index: 0, Ranges: []Range{r(5, 5)}}}},
		},
		{
			// ref: lsp/language/completion.md#Grammar
			// quote: "tabstop     ::= '$' int | '${' int '}'"
			"braced tab stop", "a${1}b",
			Snippet{"ab", []ts{{Index: 1, Ranges: []Range{r(1, 1)}}}},
		},
		{
			// ref: lsp/language/completion.md#Tab stops
			// quote: "Multiple tab stops are linked and updated in sync."
			"linked tab stops", "${1:x} = $1",
			Snippet{"x = x", []ts{{Index: 1, Ranges: []Range{r(0, 1), r(4, 5)}}}},
		},
		{
			// ref: lsp/language/completion.md#Placeholders
			// quote: "Placeholders can be nested, like `${1:another ${2:placeholder}}`."
			"nested placeholders", "${1:another ${2:placeholder}}",
			Snippet{"another placeholder", []ts{{Index: 1, Ranges: []Range{r(0, 19)}}, {Index: 2, Ranges: []Range{r(8, 19)}}}},
		},
		{
			// ref: lsp/language/completion.md#Choice
			// quote: "The syntax is a comma separated enumeration of values, enclosed with the pipe-character, for example `${1|one,two,three|}`."
			"choice", "${1|one,two,three|}",
			Snippet{"one", []ts{{Index: 1, Ranges: []Range{r(0, 3)}, Choices: []string{"one", "two", "three"}}}},
		},
		{
			// ref: lsp/language/completion.md#Grammar
			// quote: "Within choice elements, the backslash also escapes comma and pipe characters."
			"choice escapes", `${1|a\,b,c\|d|}`,
			Snippet{"a,b", []ts{{Index: 1, Ranges: []Range{r(0, 3)}, Choices: []string{"a,b", "c|d"}}}},
		},
		{
			// ref: lsp/language/completion.md#Grammar
			// quote: "With `\\` (backslash), you can escape `$`, `}` and `\\`."
			"escapes", `\$1 \} \\ \x`,
			Snippet{`$1 } \ \x`, nil},
		},
		{
			// ref: lsp/language/completion.md#Variables
			// quote: "With `$name` or `${name:default}` you can insert the value of a variable."
			"variables", "$TM_LINE_NUMBER ${TM_FILENAME:x}",
			Snippet{"7 foo.txt", nil},
		},
		{
			// ref: lsp/language/completion.md#Variables
			// quote: "When a variable isn’t set, its *default* or the empty string is inserted."
			"unset variables", "[${TM_SELECTED_TEXT:def}][$TM_SELECTED_TEXT]",
			Snippet{"[def][]", nil},
		},
		{
			// ref: lsp/language/completion.md#Variables
			// quote: "When a variable is unknown (that is, its name isn’t defined) the name of the variable is inserted and it is transformed into a placeholder."
			"unknown variable", "$1 $nope",
			Snippet{" nope", []ts{{Index: 1, Ranges: []Range{r(0, 0)}}, {Index: 2, Ranges: []Range{r(1, 5)}}}},
		},
		{
			// ref: lsp/language/completion.md#Variable Transforms
			// quote: "The following example inserts the name of the current file without its ending, so from `foo.txt` it makes `foo`."
			"transform", `${TM_FILENAME/(.*)\..+$/$1/}`,
			Snippet{"foo", nil},
		},
		{
			// ref: lsp/language/completion.md#Grammar
			// quote: "| '${' int ':' '/upcase' | '/downcase' | '/capitalize' '}'"
			"transform format", `${TM_FILENAME/(foo)(bar)?/${1:/upcase}${2:+B}${2:-none}/}`,
			Snippet{"FOOnone.txt", nil},
		},
		{
			"multibyte", "é${1:ü}",
			Snippet{"éü", []ts{{Index: 1, Ranges: []Range{r(2, 4)}}}},
		},
		{"unterminated is text", "${1:a", Snippet{"${1:a", nil}},
		{"lone dollar", "a $ b", Snippet{"a $ b", nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in, known)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q) =\n %+v\nwant\n %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseNilVars(t *testing.T) {
	if got := Parse("${TM_FILENAME:x}", nil); got.Text != "TM_FILENAME" {
		t.Errorf("unknown variable without a resolver = %q", got.Text)
	}
}
