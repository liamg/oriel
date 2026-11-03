package voice

import (
	"context"
	"os/exec"
	"regexp"
	"time"
)

// nmMetered is NetworkManager's Metered property in busctl's or gdbus's output: "u 1", "(<uint32 1>,)".
var nmMetered = regexp.MustCompile(`(\d+)\D*$`)

// Metered reports whether NetworkManager counts the network as metered, or guesses it is. Without NetworkManager it's
// false.
func Metered() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, args := range [][]string{
		{"busctl", "--system", "get-property", "org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager",
			"org.freedesktop.NetworkManager", "Metered"},
		{"gdbus", "call", "--system", "--dest", "org.freedesktop.NetworkManager", "--object-path", "/org/freedesktop/NetworkManager",
			"--method", "org.freedesktop.DBus.Properties.Get", "org.freedesktop.NetworkManager", "Metered"},
	} {
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
		if err != nil {
			continue
		}
		return meteredValue(string(out))
	}
	return false
}

// meteredValue reads NMMetered: 1 is yes, 3 is guessed yes.
func meteredValue(out string) bool {
	m := nmMetered.FindStringSubmatch(out)
	return m != nil && (m[1] == "1" || m[1] == "3")
}
