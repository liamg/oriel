package keymap

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"gd", []string{"g", "d"}},
		{"<C-w>h", []string{"<C-w>", "h"}},
		{"<c-w>", []string{"<C-w>"}},
		{"<d-S-x>", []string{"<D-S-x>"}},
		// ref:   intro.txt *<Enter>*
		// quote: "<Enter>		same as <CR>"
		{"<Enter><Return><cr>", []string{"<CR>", "<CR>", "<CR>"}},
		// ref:   intro.txt *space*
		// quote: "<Space>		space				 32"
		{"<Space>x", []string{" ", "x"}},
		// ref:   intro.txt *<lt>*
		// quote: "<lt>		less-than		<	 60"
		{"<lt>", []string{"<"}},
		// ref:   intro.txt *<Esc>*
		// quote: "<Esc>		escape			CTRL-[	 27"
		{"<esc>", []string{"<Esc>"}},
		{"<Leader>rr", []string{"<Leader>", "r", "r"}},
		{"<F12>", []string{"<F12>"}},
		{"<C-w", []string{"<", "C", "-", "w"}},
		{"<>", []string{"<", ">"}},
		{"é", []string{"é"}},
	} {
		if got := Parse(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("Parse(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := Format([]string{" ", "<", "<C-w>", "x"}); got != "<Space><lt><C-w>x" {
		t.Errorf("Format = %q", got)
	}
}
