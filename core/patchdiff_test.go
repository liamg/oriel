package core

import "testing"

// The file headers come before the first hunk; inside one, a removed "-- x" or an added "++ y" is a line like any.
func TestPatchDiffCountsLinesThatLookLikeHeaders(t *testing.T) {
	patch := "--- a/sql.txt\n+++ b/sql.txt\n@@ -1,3 +1,3 @@\n keep\n--- an SQL comment\n+++ a counter\n"
	d := PatchDiff("sql.txt", patch)
	if d.Added != 1 || d.Removed != 1 || len(d.Lines) != 3 {
		t.Errorf("added %d, removed %d, lines %+v", d.Added, d.Removed, d.Lines)
	}
	if d.Lines[1].Text != "-- an SQL comment" || d.Lines[2].Text != "++ a counter" {
		t.Errorf("the lines: %+v", d.Lines)
	}
}
