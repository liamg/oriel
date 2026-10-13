package mine

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/vim/vimtest"
)

// notation renders keys for a `keys:` field, whose value is trimmed: spaces at the ends become <Space>.
func notation(keys []vimtest.Key) string {
	var b strings.Builder
	for i, k := range keys {
		if k.Name == "" && k.Mods == 0 && k.Rune == ' ' && (i == 0 || i == len(keys)-1) {
			b.WriteString("<Space>")
			continue
		}
		b.WriteString(k.String())
	}
	return b.String()
}

// Render writes cases as a .vt file.
func Render(header string, cases []Mined) string {
	var b strings.Builder
	b.WriteString(header)
	for _, m := range cases {
		b.WriteString("\n")
		b.WriteString(renderCase(m))
	}
	return b.String()
}

func renderCase(m Mined) string {
	c := m.Case
	var b strings.Builder
	fmt.Fprintf(&b, "=== %s\n", c.Name)
	fmt.Fprintf(&b, "ref:    %s\n", c.Ref)
	if c.Follows != "" {
		fmt.Fprintf(&b, "follows: %s\n", c.Follows)
	}
	block(&b, "given", c.Given.String())
	fmt.Fprintf(&b, "keys:   %s\n", notation(c.Keys))
	for _, n := range m.Notes {
		fmt.Fprintf(&b, "# %s\n", n)
	}
	block(&b, "expect", c.Expect.String())
	if len(c.Regs) > 0 {
		var parts []string
		for _, r := range sortedRunes(c.Regs) {
			v := "(unset)"
			if c.Regs[r] != nil {
				v = strconv.Quote(*c.Regs[r])
			}
			parts = append(parts, fmt.Sprintf(`"%c = %s`, r, v))
		}
		fmt.Fprintf(&b, "regs:   %s\n", strings.Join(parts, "   "))
	}
	if len(c.Marks) > 0 {
		var parts []string
		for _, r := range sortedRunes(c.Marks) {
			v := "(unset)"
			if p := c.Marks[r]; p != nil {
				v = p.String()
			}
			name := string(r)
			if r == '\'' {
				name = "''"
			}
			parts = append(parts, name+"="+v)
		}
		fmt.Fprintf(&b, "marks:  %s\n", strings.Join(parts, " "))
	}
	return b.String()
}

func block(b *strings.Builder, field, markup string) {
	fmt.Fprintf(b, "%s:\n", field)
	for _, l := range strings.Split(markup, "\n") {
		fmt.Fprintf(b, "  %s\n", l)
	}
}

func sortedRunes[V any](m map[rune]V) []rune {
	var rs []rune
	for r := range m {
		rs = append(rs, r)
	}
	slices.Sort(rs)
	return rs
}

// roundTrip checks that the case reads back from its rendering unchanged.
func roundTrip(c vimtest.Case) error {
	text := renderCase(Mined{Case: c})
	cs, err := vimtest.Parse("mined", strings.NewReader(text))
	if err != nil {
		return err
	}
	if len(cs) != 1 {
		return fmt.Errorf("%d cases read back", len(cs))
	}
	got := cs[0]
	switch {
	case got.Given.String() != c.Given.String() || !slices.Equal(got.Given.Lines, c.Given.Lines):
		return fmt.Errorf("given reads back as %q", got.Given.String())
	case got.Expect.String() != c.Expect.String() || !slices.Equal(got.Expect.Lines, c.Expect.Lines):
		return fmt.Errorf("expect reads back as %q", got.Expect.String())
	case vimtest.Notation(got.Keys) != vimtest.Notation(c.Keys):
		return fmt.Errorf("keys read back as %q", vimtest.Notation(got.Keys))
	case !reflect.DeepEqual(got.Regs, c.Regs) || !reflect.DeepEqual(got.Marks, c.Marks):
		return fmt.Errorf("registers or marks don't read back")
	}
	return nil
}
