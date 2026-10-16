package seatbelt

import (
	"fmt"
	"strings"
)

// Profile is what an agent under Seatbelt may touch.
type Profile struct {
	// Hide are subtrees it can't read; the rest of the system it can.
	Hide []string
	// Paths open paths, in order: a later one overrides an earlier one it's under.
	Paths []Access
	// Sockets are the unix sockets it may connect to.
	Sockets []string
	// Ports are the loopback TCP ports it may connect to.
	Ports []int
	// Listen lets it serve on loopback.
	Listen bool
	// Loopback lets it connect to any loopback port.
	Loopback bool
	// Network lets it reach anywhere, unfiltered.
	Network bool
}

// Access opens Path and everything under it: to read, or to read and write.
type Access struct {
	Path  string
	Write bool
}

// services are the mach services a toolchain's programs look up: users and groups, notifications, logging,
// preferences, file events, and the certificate checks behind TLS. The keychain (com.apple.SecurityServer) isn't
// among them.
var services = []string{
	"com.apple.system.opendirectoryd.libinfo", "com.apple.system.notification_center", "com.apple.system.logger",
	"com.apple.logd", "com.apple.diagnosticd", "com.apple.cfprefsd.daemon", "com.apple.cfprefsd.agent",
	"com.apple.FSEvents", "com.apple.trustd", "com.apple.trustd.agent", "com.apple.SystemConfiguration.configd",
	"com.apple.coreservices.launchservicesd", "com.apple.lsd.mapdb",
}

// devices are the devices a terminal program writes to and controls.
const devices = `(literal "/dev/null") (literal "/dev/zero") (literal "/dev/tty") (literal "/dev/dtracehelper") (regex #"^/dev/ttys[0-9]+$")`

// SBPL is p as a sandbox-exec profile. Seatbelt applies the last rule that matches, so each rule overrides those
// before it. Paths must be real (/private/tmp, not /tmp), as Seatbelt matches them after symlinks.
func SBPL(p Profile) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString("(allow process-fork process-exec)\n(allow signal (target same-sandbox))\n(allow process-info* (target same-sandbox))\n")
	b.WriteString("(allow sysctl-read)\n(allow pseudo-tty)\n(allow ipc-posix*)\n(allow user-preference-read)\n")
	b.WriteString("(allow mach-lookup")
	for _, s := range services {
		fmt.Fprintf(&b, " (global-name %s)", quote(s))
	}
	b.WriteString(")\n(allow file-read*)\n")
	if len(p.Hide) > 0 {
		b.WriteString("(deny file-read*")
		for _, h := range p.Hide {
			fmt.Fprintf(&b, " (subpath %s)", quote(h))
		}
		b.WriteString(")\n(allow file-read-metadata)\n")
	}
	for _, a := range p.Paths {
		q := quote(a.Path)
		if a.Write {
			fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %s))\n", q)
		} else {
			fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n(deny file-write* (subpath %s))\n", q, q)
		}
	}
	fmt.Fprintf(&b, "(allow file-read* file-write-data file-ioctl %s)\n", devices)
	b.WriteString("(allow system-socket)\n")
	if p.Network {
		b.WriteString("(allow network*)\n(allow mach-lookup (global-name \"com.apple.dnssd.service\"))\n")
		return b.String()
	}
	if len(p.Sockets) > 0 || len(p.Ports) > 0 {
		b.WriteString("(allow network-outbound")
		for _, s := range p.Sockets {
			fmt.Fprintf(&b, " (remote unix-socket (path-literal %s))", quote(s))
		}
		for _, port := range p.Ports {
			fmt.Fprintf(&b, " (remote tcp %s)", quote(fmt.Sprintf("localhost:%d", port)))
		}
		b.WriteString(")\n")
	}
	if p.Listen {
		b.WriteString("(allow network-bind network-inbound (local ip \"localhost:*\"))\n")
	}
	if p.Loopback {
		b.WriteString("(allow network-outbound (remote ip \"localhost:*\"))\n")
	}
	return b.String()
}

// quote is a Seatbelt string: in double quotes, with \ and " escaped.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
