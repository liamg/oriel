package vim

import (
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/vim/vimtest/nvimtext"
)

func sample(k noticeKind) []any {
	args := []any{"1", "1", "1", "1", "1"}
	if k.textf == nil {
		args = args[:strings.Count(k.text, "%s")]
	}
	return args
}

// Every notice has nvim's wording for the conformance cases, and none shows a vim error number.
func TestNotices(t *testing.T) {
	vimNumber := regexp.MustCompile(`\bE\d+:`)
	seen := map[string]bool{}
	for _, k := range kinds {
		n := k.with(sample(k)...)
		if seen[n.ID] {
			t.Errorf("%s: ID used twice", n.ID)
		}
		seen[n.ID] = true
		if strings.Contains(n.Text, "%!") || vimNumber.MatchString(n.Text) {
			t.Errorf("%s: text %q", n.ID, n.Text)
		}
		if got := nvimtext.Notice(n); strings.HasPrefix(got, "no nvim wording") || strings.Contains(got, "%!") {
			t.Errorf("%s: nvim wording %q", n.ID, got)
		}
	}
}
