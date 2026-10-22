// Package semver reads and orders semantic versions (semver.org).
package semver

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version: 1.2.3, or 1.2.3-rc.1. A leading v is allowed and dropped; build metadata is dropped.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// Parse reads a full version: major, minor and patch.
func Parse(s string) (Version, error) {
	v, n, err := ParsePartial(s)
	if err == nil && n < 3 {
		err = fmt.Errorf("%q isn't a full version (major.minor.patch)", s)
	}
	return v, err
}

// ParsePartial reads 1, 1.2 or 1.2.3 (with a pre-release), and says how many parts it had.
func ParsePartial(s string) (Version, int, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "v")
	t, _, _ = strings.Cut(t, "+")
	t, pre, hasPre := strings.Cut(t, "-")
	parts := strings.Split(t, ".")
	if len(parts) > 3 || t == "" || hasPre && pre == "" {
		return Version{}, 0, fmt.Errorf("%q isn't a version", s)
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || p[0] == '+' || (len(p) > 1 && p[0] == '0') {
			return Version{}, 0, fmt.Errorf("%q isn't a version", s)
		}
		n[i] = x
	}
	return Version{n[0], n[1], n[2], pre}, len(parts), nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders versions as semver.org does: a pre-release comes before its release.
func (v Version) Compare(w Version) int {
	if c := cmp.Or(cmp.Compare(v.Major, w.Major), cmp.Compare(v.Minor, w.Minor), cmp.Compare(v.Patch, w.Patch)); c != 0 {
		return c
	}
	switch {
	case v.Pre == w.Pre:
		return 0
	case v.Pre == "":
		return 1
	case w.Pre == "":
		return -1
	}
	return comparePre(v.Pre, w.Pre)
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(as), len(bs)) {
		x, xerr := strconv.Atoi(as[i])
		y, yerr := strconv.Atoi(bs[i])
		var c int
		switch {
		case xerr == nil && yerr == nil:
			c = cmp.Compare(x, y)
		case xerr == nil:
			c = -1
		case yerr == nil:
			c = 1
		default:
			c = strings.Compare(as[i], bs[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

// Bump is v moved on by part, "major", "minor" or "patch" (anything else), with no pre-release.
func (v Version) Bump(part string) Version {
	switch part {
	case "major":
		return Version{Major: v.Major + 1}
	case "minor":
		return Version{Major: v.Major, Minor: v.Minor + 1}
	}
	return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
}
