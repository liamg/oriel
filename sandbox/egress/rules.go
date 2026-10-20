package egress

import (
	"net/url"
	"strconv"
	"strings"
)

// HostGateway is the name a container reaches the host machine by. No
// wildcard matches it.
const HostGateway = "host.docker.internal"

// Rule matches a destination: Host is an exact name or a "*.example.com"
// wildcard (which matches subdomains but not example.com itself), or "*" for
// any host; Port 0 matches any port.
type Rule struct {
	Host  string `json:"host"`
	Port  int    `json:"port,omitempty"`
	Allow bool   `json:"allow"`
	// Private lets an allow reach private and tailnet addresses.
	Private bool `json:"private,omitempty"`
	// Source says where the rule came from, for the session's log.
	Source string `json:"-"`
}

// Levels are the rules in force, by where they came from.
type Levels struct {
	Session []Rule
	// Settings are the user's, most local first: project, then user.
	Settings [][]Rule
	// Builtin are the environment's endpoints and the preset list.
	Builtin []Rule
}

// Decide picks the rule for host:port across levels. A deny in settings wins
// over everything but a built-in allow naming the host more specifically;
// otherwise the session's rules beat settings, and settings beat built-ins.
func Decide(l Levels, host string, port int) (Rule, bool) {
	host = normHost(host)
	var allow Rule
	allowed := false
	for _, rules := range l.Settings {
		r, ok := Match(rules, host, port)
		if !ok {
			continue
		}
		if !r.Allow {
			if b, ok := Match(l.Builtin, host, port); ok && b.Allow && hostScore(b, host) > hostScore(r, host) {
				return b, true
			}
			return r, true
		}
		if !allowed {
			allow, allowed = r, true
		}
	}
	if r, ok := Match(l.Session, host, port); ok {
		return r, true
	}
	if allowed {
		return allow, true
	}
	return Match(l.Builtin, host, port)
}

// EnvRules allow the http(s) endpoints named in env, at their port or their
// scheme's. They reach private addresses: the user configured them.
func EnvRules(env map[string]string) []Rule {
	var out []Rule
	for _, v := range env {
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" {
			continue
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			switch u.Scheme {
			case "http":
				port = 80
			case "https":
				port = 443
			default:
				continue
			}
		} else if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		out = append(out, Rule{Host: normHost(u.Hostname()), Port: port, Allow: true, Private: true})
	}
	return out
}

func normHost(h string) string { return strings.TrimSuffix(strings.ToLower(h), ".") }

// Match returns the first rule matching host:port, preferring the most
// specific (exact host over wildcard; longer wildcard suffix over shorter;
// specific port over any), and ok=false when none match.
func Match(rules []Rule, host string, port int) (Rule, bool) {
	host = normHost(host)
	var (
		best      Rule
		bestScore = -1
	)
	for _, r := range rules {
		s := score(r, host, port)
		if s > bestScore {
			best, bestScore = r, s
		}
	}
	return best, bestScore >= 0
}

// Exact reports whether r names host itself rather than through a wildcard.
func Exact(r Rule, host string) bool {
	return normHost(r.Host) == normHost(host)
}

// score ranks a matching rule by specificity, or is -1 when it does not match.
// Host specificity dominates; the port only breaks ties.
func score(r Rule, host string, port int) int {
	if r.Port != 0 && r.Port != port {
		return -1
	}
	h := hostScore(r, host)
	if h < 0 {
		return -1
	}
	s := h * 2
	if r.Port != 0 {
		s++
	}
	return s
}

// hostScore ranks how specifically r's host matches host, or is -1.
func hostScore(r Rule, host string) int {
	pattern := normHost(r.Host)
	switch {
	case pattern == host:
		return 1 << 16
	case host == HostGateway:
		return -1
	case pattern == "*":
		return 0
	case strings.HasPrefix(pattern, "*."):
		suffix := pattern[1:]
		if !strings.HasSuffix(host, suffix) || len(host) == len(suffix) {
			return -1
		}
		return 1 + len(suffix)
	}
	return -1
}

// DefaultAllow is the preset allowlist.
var DefaultAllow = func() []Rule {
	hosts := []string{
		"api.anthropic.com",
		"claude.ai",
		"*.claude.ai",
		"statsig.anthropic.com",
		// Claude Code refreshes its login here; blocked, every request fails
		// once the token expires.
		"platform.claude.com",
		"console.anthropic.com",
		"mcp-proxy.anthropic.com",
		"proxy.golang.org",
		"sum.golang.org",
		"storage.googleapis.com",
		"registry.npmjs.org",
		"pypi.org",
		"files.pythonhosted.org",
		"crates.io",
		"static.crates.io",
		"index.crates.io",
		"github.com",
		"codeload.github.com",
		"objects.githubusercontent.com",
		// Where GitHub release downloads redirect to: plugin and tool
		// installers fetch their binaries from release pages.
		"release-assets.githubusercontent.com",
		"raw.githubusercontent.com",
	}
	rules := make([]Rule, len(hosts))
	for i, h := range hosts {
		rules[i] = Rule{Host: h, Port: 443, Allow: true}
	}
	return rules
}()
