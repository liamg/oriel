package ossandbox

import (
	"fmt"
	"strings"
)

// sbplRead are what any program on macOS reads: dyld and its shared cache, the system's libraries, frameworks
// and certificates, time zones, and the safe devices.
var sbplRead = []string{"/usr/lib", "/usr/share", "/System", "/Library/Apple", "/private/var/db/dyld",
	"/private/var/db/timezone", "/private/etc/ssl", "/private/etc/hosts", "/private/etc/resolv.conf",
	"/private/etc/services", "/private/etc/protocols", "/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"}

// sbplNet are the services a program with a network needs: name lookups and the certificate checks behind TLS.
var sbplNet = []string{"com.apple.dnssd.service", "com.apple.trustd", "com.apple.trustd.agent", "com.apple.SecurityServer"}

// SBPL is p as a sandbox-exec profile, Seatbelt's language: everything denied but what p lists and what every
// program needs. Paths must be real (/private/tmp, not /tmp), as Seatbelt matches them after symlinks.
func SBPL(p Profile) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString("(allow process-fork)\n(allow signal (target same-sandbox))\n(allow process-info* (target same-sandbox))\n")
	b.WriteString("(allow sysctl-read)\n(allow file-read-metadata)\n")
	b.WriteString("(allow mach-lookup (global-name \"com.apple.system.opendirectoryd.libinfo\") (global-name \"com.apple.system.notification_center\"))\n")
	b.WriteString("(allow file-write-data (literal \"/dev/null\"))\n")
	rule(&b, "allow file-read*", sbplRead, false)
	rule(&b, "allow file-read*", p.Read, false)
	rule(&b, "allow file-read* file-write*", p.Write, false)
	ex := runs(p)
	rule(&b, "allow file-read*", ex, true)
	rule(&b, "allow process-exec", ex, true)
	if len(p.Ports) > 0 {
		b.WriteString("(allow system-socket)\n(allow network-outbound")
		for _, port := range p.Ports {
			fmt.Fprintf(&b, " (remote tcp %s)", quote(fmt.Sprintf("*:%d", port)))
		}
		b.WriteString(" (remote unix-socket (path-literal \"/private/var/run/mDNSResponder\")))\n(allow mach-lookup")
		for _, s := range sbplNet {
			fmt.Fprintf(&b, " (global-name %s)", quote(s))
		}
		b.WriteString(")\n")
		rule(&b, "allow file-read*", []string{"/private/var/db/mds", "/Library/Keychains"}, false)
	}
	return b.String()
}

// rule is one allow over paths: each a literal (one file) or a subpath (everything under it).
func rule(b *strings.Builder, allow string, paths []string, literal bool) {
	if len(paths) == 0 {
		return
	}
	fmt.Fprintf(b, "(%s", allow)
	for _, p := range paths {
		kind := "subpath"
		if literal {
			kind = "literal"
		}
		fmt.Fprintf(b, " (%s %s)", kind, quote(p))
	}
	b.WriteString(")\n")
}

// quote is a Seatbelt string: in double quotes, with \ and " escaped.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
