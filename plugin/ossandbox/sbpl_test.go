package ossandbox

import (
	"strings"
	"testing"
)

// The macOS profile, by its text. Seatbelt denies what no rule allows ("(deny default)"), so the profile is
// right when it allows exactly the profile's paths, programs and ports beyond the system's.
func TestSBPL(t *testing.T) {
	p := Profile{Read: []string{"/plugins/acme/jira"}, Write: []string{"/src/work tree", `/odd"name`}, Exec: []string{"/plugins/acme/jira/bin/jira"}}
	got := SBPL(p)
	for _, want := range []string{
		"(version 1)\n(deny default)\n",
		`(allow file-read* (subpath "/plugins/acme/jira"))`,
		`(allow file-read* file-write* (subpath "/src/work tree") (subpath "/odd\"name"))`,
		`(allow process-exec (literal "/plugins/acme/jira/bin/jira"))`,
		`(subpath "/usr/lib")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the profile lacks %s:\n%s", want, got)
		}
	}
	for _, not := range []string{"network-outbound", "system-socket", "process-exec (subpath", "(allow default", "file-write* (subpath \"/usr"} {
		if strings.Contains(got, not) {
			t.Errorf("a profile with no ports or wide exec has %s:\n%s", not, got)
		}
	}
	p.Ports = []int{443}
	got = SBPL(p)
	for _, want := range []string{`(allow network-outbound (remote tcp "*:443")`, `(global-name "com.apple.dnssd.service")`, "(allow system-socket)"} {
		if !strings.Contains(got, want) {
			t.Errorf("with port 443 the profile lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"*:80"`) {
		t.Errorf("a port not listed:\n%s", got)
	}
}

// A path can't break out of its string: a quote or backslash in it is escaped.
func TestSBPLQuote(t *testing.T) {
	for in, want := range map[string]string{`/a`: `"/a"`, `/a"b`: `"/a\"b"`, `/a\b`: `"/a\\b"`, `/a") (allow default`: `"/a\") (allow default"`} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}
