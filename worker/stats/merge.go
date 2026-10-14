package stats

import (
	"cmp"
	"slices"
	"strings"
)

// Merge adds up several workers' answers to the same query: their sessions are their own, so everything sums, and
// rows with the same key (a repo on two machines) become one.
func Merge(rs ...Report) Report {
	if len(rs) == 0 {
		return Report{}
	}
	out := Report{Points: rs[0].Points}
	groups := map[string]*Group{}
	var order []string
	models := map[string]*Model{}
	efforts := map[string]*Effort{}
	tools := map[string]*ToolRow{}
	for _, r := range rs {
		if out.Scanned.IsZero() || r.Scanned.Before(out.Scanned) {
			out.Scanned = r.Scanned
		}
		sum(&out.Totals, r.Totals)
		sum(&out.Prev, r.Prev)
		out.Tokens = out.Tokens.Add(r.Tokens, 1)
		out.Costs = out.Costs.Add(r.Costs, 1)
		for i, v := range r.Hours {
			out.Hours[i] += v
		}
		for _, g := range r.Groups {
			m := groups[g.Key]
			if m == nil {
				m = &Group{Key: g.Key, Label: g.Label, Series: make([]float64, len(out.Points)), Used: make([]int64, len(out.Points))}
				groups[g.Key] = m
				order = append(order, g.Key)
			}
			m.Sessions += g.Sessions
			m.Requests += g.Requests
			m.Tokens += g.Tokens
			m.Cost += g.Cost
			if g.Last.After(m.Last) {
				m.Last = g.Last
			}
			for _, rp := range g.Repos {
				if !slices.Contains(m.Repos, rp) {
					m.Repos = append(m.Repos, rp)
				}
			}
			for i := range min(len(g.Series), len(m.Series)) {
				m.Series[i] += g.Series[i]
			}
			for i := range min(len(g.Used), len(m.Used)) {
				m.Used[i] += g.Used[i]
			}
		}
		for _, x := range r.Models {
			m := models[x.Name]
			if m == nil {
				m = &Model{Name: x.Name}
				models[x.Name] = m
			}
			m.Requests += x.Requests
			m.Sidechain += x.Sidechain
			m.Tokens += x.Tokens
			m.Cost += x.Cost
		}
		for _, x := range r.Efforts {
			e := efforts[x.Name]
			if e == nil {
				e = &Effort{Name: x.Name}
				efforts[x.Name] = e
			}
			e.Requests += x.Requests
			e.Cost += x.Cost
		}
		for _, x := range r.Tools {
			t := tools[x.Name]
			if t == nil {
				t = &ToolRow{Name: x.Name}
				tools[x.Name] = t
			}
			if x.Calls > t.Calls {
				t.P50 = x.P50 // the median of the machine with the most calls
			}
			t.Calls += x.Calls
			t.Errors += x.Errors
			for i, b := range x.Buckets {
				t.Buckets[i] += b
			}
		}
	}
	for _, k := range order {
		out.Groups = append(out.Groups, *groups[k])
	}
	slices.SortStableFunc(out.Groups, func(a, b Group) int { return cmp.Compare(b.Cost, a.Cost) })
	for _, m := range models {
		out.Models = append(out.Models, *m)
	}
	slices.SortFunc(out.Models, func(a, b Model) int { return cmp.Or(cmp.Compare(b.Cost, a.Cost), strings.Compare(a.Name, b.Name)) })
	for _, e := range efforts {
		out.Efforts = append(out.Efforts, *e)
	}
	SortEfforts(out.Efforts)
	for _, t := range tools {
		out.Tools = append(out.Tools, *t)
	}
	slices.SortFunc(out.Tools, func(a, b ToolRow) int { return cmp.Or(cmp.Compare(b.Calls, a.Calls), strings.Compare(a.Name, b.Name)) })
	return out
}

func sum(t *Totals, o Totals) {
	t.Sessions += o.Sessions
	t.MultiRepo += o.MultiRepo
	t.Pairs += o.Pairs
	t.Requests += o.Requests
	t.Turns += o.Turns
	t.ToolCalls += o.ToolCalls
	t.ToolErrors += o.ToolErrors
	t.ActiveS += o.ActiveS
	t.Tokens = t.Tokens.Add(o.Tokens, 1)
	t.Cost += o.Cost
}
