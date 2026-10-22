package aireview

import (
	"testing"

	"github.com/liamg/oriel/core"
)

func TestParseFindings(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []core.Finding
	}{
		{`[{"severity":"blocker","title":"Data loss","file":"a.go:12-14"}]`, []core.Finding{{Severity: "critical", Title: "Data loss", File: "a.go", Line: 12}}},
		{"Verdict.\n```\n{\"findings\":[{\"severity\":\"nit\",\"title\":\"Name\",\"path\":\"./b.go\",\"line\":4}]}\n```",
			[]core.Finding{{Severity: "nit", Title: "Name", File: "b.go", Line: 4}}},
		{"```json\n{\"findings\": []}\n```", nil},
		{"No JSON here.", nil},
	} {
		got := ParseFindings(tc.text)
		if len(got) != len(tc.want) {
			t.Errorf("%q: %+v", tc.text, got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: %+v, want %+v", tc.text, got[i], tc.want[i])
			}
		}
	}
}
