package shellenv

import (
	"os"
	"os/user"
	"strings"
)

// accountShell is the login shell the user's account names, from /etc/passwd; "" if it can't be read.
func accountShell() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	return passwdShell(string(b), u.Uid)
}

// passwdShell is the shell of uid's line in a passwd file: name:password:uid:gid:gecos:home:shell.
func passwdShell(passwd, uid string) string {
	for _, line := range strings.Split(passwd, "\n") {
		f := strings.Split(line, ":")
		if len(f) == 7 && f[2] == uid {
			return strings.TrimSpace(f[6])
		}
	}
	return ""
}
