package registry

import "maps"

func (r *reg) SetCoverage(owner, file string, hits map[int]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cover == nil {
		r.cover = map[diagKey]map[int]int{}
	}
	k := diagKey{owner, file}
	if len(hits) == 0 {
		delete(r.cover, k)
		return
	}
	r.cover[k] = maps.Clone(hits)
}

func (r *reg) ClearCoverage(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.cover {
		if k.owner == owner {
			delete(r.cover, k)
		}
	}
}

func (r *reg) CoverageFor(file string) map[int]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[int]int{}
	for k, hits := range r.cover {
		if k.file != file {
			continue
		}
		for l, n := range hits {
			if have, ok := out[l]; !ok || n > have {
				out[l] = n
			}
		}
	}
	return out
}
