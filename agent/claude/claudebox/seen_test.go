package claudebox

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A notice Claude Code shows once waits for someone to dismiss it, and a sandbox has nobody: every one is marked
// seen, though the host's user never saw it, and the version's release notes are read.
func TestSandboxStateMarksEveryNoticeSeen(t *testing.T) {
	state := SandboxState([]string{"/w"}, map[string]any{"hasSeenTasksHint": false}, Seen{Flags: []string{"hasSeenTasksHint", "hasSeenSomethingNew"}, Version: "2.1.317"})
	for _, k := range []string{"hasSeenTasksHint", "hasSeenSomethingNew", "hasCompletedOnboarding"} {
		if state[k] != true {
			t.Errorf("%s = %v, want true", k, state[k])
		}
	}
	if state["lastReleaseNotesSeen"] != "2.1.317" {
		t.Errorf("lastReleaseNotesSeen = %v", state["lastReleaseNotesSeen"])
	}
}

// The flags a Claude Code's bundle sets true are found in its binary, a newer one's own included, wherever a read
// of it is cut; what it only counts (hasUsed…) isn't one.
func TestSeenInFindsTheBundlesFlags(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("prefix;e.hasSeenFirst:!0,")
	b.Write(bytes.Repeat([]byte{'x'}, 4<<20-len("prefix;e.hasSeenFirst:!0,")-10))
	b.WriteString("{hasAcknowledgedAcrossTheCut:!0}") // across the first read's end
	b.WriteString(";u.hasUsedStash:!0;v.hasSeenOff:!1;")
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, b.Bytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	got := seenIn(bin)
	for _, want := range []string{"hasSeenFirst", "hasAcknowledgedAcrossTheCut", "hasCompletedOnboarding"} {
		if !slices.Contains(got, want) {
			t.Errorf("seenIn has no %s: %v", want, got)
		}
	}
	for _, not := range []string{"hasUsedStash", "hasSeenOff"} {
		if slices.Contains(got, not) {
			t.Errorf("seenIn has %s", not)
		}
	}
}
