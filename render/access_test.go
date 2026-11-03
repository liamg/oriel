package render

import (
	"strings"
	"testing"
)

func TestAccessDiffSeesCheckedAndSelection(t *testing.T) {
	on, off := true, false
	tree := func(checked *bool, sel TextRange) *AccessNode {
		return &AccessNode{Role: RoleWindow, Children: []*AccessNode{
			{ID: 1, Role: RoleCheckBox, Name: "Wrap", Checked: checked},
			{ID: 2, Role: RoleTextBox, Value: "hello", Selection: sel},
		}}
	}
	prev := tree(&off, TextRange{1, 1})
	for _, c := range []struct {
		name string
		next *AccessNode
		want []uint64
	}{
		{"same", tree(&off, TextRange{1, 1}), nil},
		{"checked", tree(&on, TextRange{1, 1}), []uint64{1}},
		{"no state", tree(nil, TextRange{1, 1}), []uint64{1}},
		{"caret moved", tree(&off, TextRange{2, 2}), []uint64{2}},
		{"selected", tree(&off, TextRange{0, 5}), []uint64{2}},
	} {
		var got []uint64
		for _, n := range AccessDiff(prev, c.next).Changed {
			got = append(got, n.ID)
		}
		if len(got) != len(c.want) || len(got) > 0 && got[0] != c.want[0] {
			t.Errorf("%s: changed %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDumpSaysChecked(t *testing.T) {
	on, off := true, false
	n := &AccessNode{Role: RoleWindow, Children: []*AccessNode{
		{ID: 1, Role: RoleCheckBox, Name: "Wrap", Checked: &on},
		{ID: 2, Role: RoleCheckBox, Name: "Spell", Checked: &off},
	}}
	if got := n.Dump(); !strings.Contains(got, `checkbox "Wrap" checked`) || !strings.Contains(got, `checkbox "Spell" unchecked`) {
		t.Errorf("dump:\n%s", got)
	}
}
