package plugin

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Version is a semantic version: 1.2.3, or 1.2.3-rc.1 (semver.org). A leading v is allowed and dropped.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseVersion reads a full version: major, minor and patch.
func ParseVersion(s string) (Version, error) {
	v, n, err := parsePartial(s)
	if err == nil && n < 3 {
		err = fmt.Errorf("%q isn't a full version (major.minor.patch)", s)
	}
	return v, err
}

// parsePartial reads 1, 1.2 or 1.2.3 (with a pre-release), and says how many parts it had.
func parsePartial(s string) (Version, int, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "v")
	t, pre, _ := strings.Cut(t, "-")
	t, _, _ = strings.Cut(t, "+")
	parts := strings.Split(t, ".")
	if len(parts) > 3 || t == "" {
		return Version{}, 0, fmt.Errorf("%q isn't a version", s)
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || (len(p) > 1 && p[0] == '0') {
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

// Range is a set of versions, as a [plugins] entry names it (catalog.md), in node-semver's syntax: "*", "^0.4",
// "~1.2", "1.x", ">=0.9 <2", "1.2.3 || ^2", or an exact "1.2.3". Hyphen ranges aren't supported.
type Range struct {
	src  string
	sets [][]comparator // any set, every comparator in it
}

type comparator struct {
	op string // =, <, <=, >, >=
	v  Version
}

// ParseRange reads a range: sets joined by ||, each of comparators separated by spaces.
func ParseRange(s string) (Range, error) {
	r := Range{src: s}
	for _, set := range strings.Split(s, "||") {
		var cmps []comparator
		for _, f := range strings.Fields(set) {
			cs, err := parseComparator(f)
			if err != nil {
				return Range{}, fmt.Errorf("range %q: %w", s, err)
			}
			cmps = append(cmps, cs...)
		}
		r.sets = append(r.sets, cmps)
	}
	return r, nil
}

func parseComparator(f string) ([]comparator, error) {
	op := ""
	for _, o := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(f, o) {
			op, f = o, f[len(o):]
			break
		}
	}
	for _, x := range []string{".x", ".X", ".*"} {
		for strings.HasSuffix(f, x) {
			f = strings.TrimSuffix(f, x)
		}
	}
	if f == "" || f == "*" || f == "x" || f == "X" {
		return nil, nil
	}
	v, n, err := parsePartial(f)
	if err != nil {
		return nil, err
	}
	// next is the version after the last part named: 1.2 → 1.3.0, 1 → 2.0.0.
	next := func(parts int) Version {
		switch parts {
		case 1:
			return Version{Major: v.Major + 1}
		case 2:
			return Version{Major: v.Major, Minor: v.Minor + 1}
		}
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	}
	switch op {
	case "^":
		// The first non-zero part may not change: ^1.2 is <2, ^0.4 is <0.5, ^0.0.3 is <0.0.4.
		upto := 1
		switch {
		case v.Major == 0 && n >= 2 && v.Minor == 0 && n == 3:
			upto = 3
		case v.Major == 0 && n >= 2:
			upto = 2
		}
		return []comparator{{">=", v}, {"<", next(upto)}}, nil
	case "~":
		return []comparator{{">=", v}, {"<", next(min(n, 2))}}, nil
	case "", "=":
		if n == 3 {
			return []comparator{{"=", v}}, nil
		}
		return []comparator{{">=", v}, {"<", next(n)}}, nil
	case "<=":
		if n < 3 {
			return []comparator{{"<", next(n)}}, nil
		}
	case ">":
		if n < 3 {
			return []comparator{{">=", next(n)}}, nil
		}
	}
	return []comparator{{op, v}}, nil
}

func (r Range) String() string { return r.src }

// Match says whether v is in the range. A pre-release is only in a set with a comparator naming a pre-release of
// the same major.minor.patch.
func (r Range) Match(v Version) bool {
	for _, set := range r.sets {
		if match(set, v) {
			return true
		}
	}
	return false
}

func match(set []comparator, v Version) bool {
	if v.Pre != "" && !slices.ContainsFunc(set, func(c comparator) bool {
		return c.v.Pre != "" && c.v.Major == v.Major && c.v.Minor == v.Minor && c.v.Patch == v.Patch
	}) {
		return false
	}
	for _, c := range set {
		d := v.Compare(c.v)
		ok := false
		switch c.op {
		case "=":
			ok = d == 0
		case "<":
			ok = d < 0
		case "<=":
			ok = d <= 0
		case ">":
			ok = d > 0
		case ">=":
			ok = d >= 0
		}
		if !ok {
			return false
		}
	}
	return true
}
