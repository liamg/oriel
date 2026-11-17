package words

import "testing"

func TestCounts(t *testing.T) {
	if Plural(1, "branch", "branches") != "branch" || Plural(0, "branch", "branches") != "branches" || Plural(2, "branch", "branches") != "branches" {
		t.Error("Plural")
	}
	if Counted(1, "file") != "1 file" || Counted(0, "file") != "0 files" || Counted(12, "file") != "12 files" {
		t.Error("Counted")
	}
}
