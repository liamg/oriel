package syntax

import (
	"slices"
	"strings"

	"github.com/liamg/oriel/text"
)

// Fold is a range of lines that can be folded, 0-based and inclusive.
type Fold struct {
	Start, End int
}

// Folds returns the foldable line ranges the folds query finds: @fold captures that span more than one
// line. #trim! leaves out blank lines at the end.
func (p *Parser) Folds() ([]Fold, error) {
	q, ms, err := p.run("folds", 0, len(p.src))
	if err != nil || q == nil {
		return nil, err
	}
	seen := map[Fold]bool{}
	var out []Fold
	for _, mt := range ms {
		_, trim := mt.q.patterns[mt.pattern].set["trim!"]
		for _, c := range mt.captures {
			if mt.q.captures[c.index] != "fold" {
				continue
			}
			f := Fold{c.startRow, c.endRow}
			if c.endC == 0 && f.End > f.Start {
				f.End--
			}
			if trim {
				lines := strings.Split(string(p.src), "\n")
				for f.End > f.Start && f.End < len(lines) && strings.TrimSpace(lines[f.End]) == "" {
					f.End--
				}
			}
			if f.End > f.Start && !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	slices.SortFunc(out, func(a, b Fold) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return b.End - a.End
	})
	return out, nil
}

// Units returns the code units the units query finds: a capture @unit.<kind> per unit, with @name for its
// name, nested by range so each unit's Parent is the index of the smallest unit around it (-1 for none).
func (p *Parser) Units() ([]text.Unit, error) {
	q, ms, err := p.run("units", 0, len(p.src))
	if err != nil || q == nil {
		return nil, err
	}
	var out []text.Unit
	seen := map[text.Range]bool{}
	for _, mt := range ms {
		var u text.Unit
		found := false
		for _, c := range mt.captures {
			name := mt.q.captures[c.index]
			switch {
			case strings.HasPrefix(name, "unit."):
				u.Kind = strings.TrimPrefix(name, "unit.")
				u.Range = text.Range{Start: c.start, End: c.end}
				found = true
			case name == "name":
				u.Name = string(p.src[c.start:c.end])
			}
		}
		if found && !seen[u.Range] {
			seen[u.Range] = true
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b text.Unit) int {
		if a.Range.Start != b.Range.Start {
			return a.Range.Start - b.Range.Start
		}
		return b.Range.End - a.Range.End
	})
	nest(out)
	return out, nil
}

// Outline is the named units, for an outline view: functions, methods and types.
func (p *Parser) Outline() ([]text.Unit, error) {
	units, err := p.Units()
	if err != nil {
		return nil, err
	}
	var out []text.Unit
	for _, u := range units {
		if u.Name != "" && (u.Kind == "function" || u.Kind == "method" || u.Kind == "type") {
			out = append(out, u)
		}
	}
	nest(out)
	return out, nil
}

// nest sets each unit's Parent to the smallest unit around it in a list sorted by start.
func nest(us []text.Unit) {
	for i := range us {
		us[i].Parent = -1
		for j := i - 1; j >= 0; j-- {
			if us[j].Range.Start <= us[i].Range.Start && us[i].Range.End <= us[j].Range.End {
				us[i].Parent = j
				break
			}
		}
	}
}
