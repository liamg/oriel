package shellenv

import "testing"

func TestPasswdShell(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\nliam:x:1000:1000:Liam,,,:/home/liam:/usr/bin/fish\n"
	if got := passwdShell(passwd, "1000"); got != "/usr/bin/fish" {
		t.Errorf("uid 1000's shell = %q", got)
	}
	if got := passwdShell(passwd, "42"); got != "" {
		t.Errorf("an unknown uid's shell = %q", got)
	}
}
