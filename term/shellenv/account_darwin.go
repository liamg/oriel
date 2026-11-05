package shellenv

import (
	"os/exec"
	"os/user"
	"strings"
)

// accountShell is the login shell the user's account names, from Directory Services; "" if it can't be read.
func accountShell() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	out, err := exec.Command("dscl", ".", "-read", "/Users/"+u.Username, "UserShell").Output()
	if err != nil {
		return ""
	}
	return parseDscl(string(out))
}

// parseDscl is the shell in dscl's "UserShell: /bin/zsh".
func parseDscl(out string) string {
	_, s, ok := strings.Cut(strings.TrimSpace(out), "UserShell:")
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}
