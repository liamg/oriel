// Package kit is what every one of the app's features shares: fonts, theme tokens, formatters, session and git
// helpers, the cross-feature services' contracts (Reviews, Inbox, Explains…) and the usual plugin plumbing. What only
// some share has a package of its own under it: kit/work (work off the loop), kit/prui (pull request views),
// kit/diagrams (mermaid diagrams), kit/agentkit (agent pickers); general controls belong in ui/widget.
package kit

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// Duration is a span to two units at most, the second dropped when it's 0: "<1ms", "4.1ms", "1.2s", "38s",
// "1m 42s", "4m", "1h 05m", "2d 3h".
func Duration(d time.Duration) string {
	d = max(d, 0)
	two := func(a int, ua string, b int, ub, pad string) string {
		if b == 0 {
			return fmt.Sprintf("%d%s", a, ua)
		}
		return fmt.Sprintf("%d%s "+pad+"%s", a, ua, b, ub)
	}
	switch {
	case d == 0:
		return "0s"
	case d < time.Millisecond:
		return "<1ms"
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return two(int(d.Minutes()), "m", int(d.Seconds())%60, "s", "%02d")
	case d < 24*time.Hour:
		return two(int(d.Hours()), "h", int(d.Minutes())%60, "m", "%02d")
	}
	return two(int(d.Hours())/24, "d", int(d.Hours())%24, "h", "%d")
}

// Bytes is a size: "512 B", "4.2 KB", "48.0 MB", "1.3 GB".
func Bytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Label is how a session is named to the user: its name, else its id.
func Label(se core.SessionState) string { return cmp.Or(se.Name, string(se.ID)) }

// JoinParts joins the parts that say something with " · ", once each.
func JoinParts(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// FirstLine is s's first line, past any leading space.
func FirstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// ExpandTabs is s with each tab as four spaces: text runs have no tab stops.
func ExpandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

func Thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func Upper(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

// TildePath shows a path under the home directory as ~/….
func TildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// Money is dollars as the pages show them: $412.80, $15.2k.
func Money(v float64) string {
	if v >= 10_000 {
		return fmt.Sprintf("$%.1fk", v/1000)
	}
	return fmt.Sprintf("$%.2f", v)
}

func Pct(f float64) int { return int(f*100 + .5) }

// Count is a number as the tables show it: 9,840, 15.2k, 612M.
func Count(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 10_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 100_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	s := fmt.Sprint(n)
	if n >= 1000 {
		s = s[:len(s)-3] + "," + s[len(s)-3:]
	}
	return s
}

// Counted is n of what: "1 session", "64 sessions".
func Counted(n int, what string) string { return fmt.Sprintf("%d %s", n, Plural(n, what, what+"s")) }

// SessionDir is where the window opens for a session: its primary project, or its own directory.
func SessionDir(se core.SessionState) string {
	if p, ok := se.Primary(); ok && p.Dir != "" {
		return p.Dir
	}
	return se.Dir
}

// Tokens is a count as the session stats show it: 950, 38k, 1.42M.
func Tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// ProjectInitials are a project's two letters for the narrow sidebar: "admin-ui" is AD.
func ProjectInitials(name string) string {
	var rs []rune
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			rs = append(rs, r)
		}
		if len(rs) == 2 {
			break
		}
	}
	return string(rs)
}
