package github

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Allowance is what's left of one of GitHub's rate limits (Resource "core" for REST, "graphql" for its points…), as
// its last answer said.
type Allowance struct {
	Resource         string
	Limit, Remaining int
	Reset            time.Time
}

// Limits keeps the latest Allowance of each of a server's rate limits, from the headers on GitHub's answers. Its zero
// value is ready.
type Limits struct {
	mu sync.Mutex
	by map[string]Allowance
}

// note keeps the allowance h reports, if it reports one.
func (l *Limits) note(h http.Header) {
	if l == nil {
		return
	}
	limit, err1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	left, err2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	reset, err3 := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || limit <= 0 {
		return
	}
	res := h.Get("X-RateLimit-Resource")
	if res == "" {
		res = "core"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.by == nil {
		l.by = map[string]Allowance{}
	}
	l.by[res] = Allowance{Resource: res, Limit: limit, Remaining: left, Reset: time.Unix(reset, 0)}
}

// All are the allowances seen, by resource.
func (l *Limits) All() []Allowance {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := slices.Collect(maps.Values(l.by))
	slices.SortFunc(out, func(a, b Allowance) int { return strings.Compare(a.Resource, b.Resource) })
	return out
}

// limitedBy is a with its HTTP transport's answers' rate limits kept in l.
func (a API) limitedBy(l *Limits) API {
	if h, ok := a.T.(HTTP); ok {
		h.Limits = l
		a.T = h
	}
	return a
}
