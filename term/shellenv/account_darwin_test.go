package shellenv

import "testing"

func TestParseDscl(t *testing.T) {
	if got := parseDscl("UserShell: /opt/homebrew/bin/fish\n"); got != "/opt/homebrew/bin/fish" {
		t.Errorf("shell = %q", got)
	}
	if got := parseDscl("No such key: UserShell\n"); got != "" {
		t.Errorf("no shell read as %q", got)
	}
}
