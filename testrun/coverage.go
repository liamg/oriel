package testrun

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Coverage is how often each line ran, per file: absolute path → 0-based line → hits. A line that isn't in its
// file's map isn't code the tool measured.
type Coverage struct {
	Files map[string]map[int]int `json:"files"`
}

// Hit records hits on a line; a line measured twice keeps the larger count.
func (c *Coverage) Hit(file string, line, hits int) {
	if c.Files == nil {
		c.Files = map[string]map[int]int{}
	}
	f := c.Files[file]
	if f == nil {
		f = map[int]int{}
		c.Files[file] = f
	}
	if old, ok := f[line]; !ok || hits > old {
		f[line] = hits
	}
}

// Merge adds o's counts to c, line by line.
func (c *Coverage) Merge(o *Coverage) {
	if o == nil {
		return
	}
	for file, lines := range o.Files {
		for l, n := range lines {
			c.Hit(file, l, n)
		}
	}
}

// LineRange is a run of lines, 0-based and inclusive, all covered or all not.
type LineRange struct {
	Start, End int
	Covered    bool
}

// Ranges is a file's measured lines as covered and uncovered runs; lines that aren't code don't break a run.
func (c *Coverage) Ranges(file string) []LineRange {
	lines := c.Files[file]
	keys := make([]int, 0, len(lines))
	for l := range lines {
		keys = append(keys, l)
	}
	slices.Sort(keys)
	var out []LineRange
	for _, l := range keys {
		cov := lines[l] > 0
		if k := len(out) - 1; k >= 0 && out[k].Covered == cov {
			out[k].End = l
			continue
		}
		out = append(out, LineRange{Start: l, End: l, Covered: cov})
	}
	return out
}

// Percent is the share of a file's measured lines that ran, or of every file's when file is "".
func (c *Coverage) Percent(file string) float64 {
	hit, all := 0, 0
	for f, lines := range c.Files {
		if file != "" && f != file {
			continue
		}
		for _, n := range lines {
			all++
			if n > 0 {
				hit++
			}
		}
	}
	if all == 0 {
		return 0
	}
	return 100 * float64(hit) / float64(all)
}

// parseGoCoverProfile reads a -coverprofile (go help testflag): "mode: set", then
// "file.go:line.col,line.col statements count" per block. file maps a block's import-path file name to a path.
func parseGoCoverProfile(r io.Reader, file func(string) string) (*Coverage, error) {
	c := &Coverage{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "mode:") || l == "" {
			continue
		}
		name, rest, ok := strings.Cut(l, ":")
		f := strings.Fields(rest)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("testrun: bad coverprofile line %q", l)
		}
		var sl, sc2, el, ec int
		if _, err := fmt.Sscanf(f[0], "%d.%d,%d.%d", &sl, &sc2, &el, &ec); err != nil {
			return nil, fmt.Errorf("testrun: bad coverprofile block %q", l)
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("testrun: bad coverprofile count %q", l)
		}
		path := file(name)
		for line := sl; line <= el; line++ {
			c.Hit(path, line-1, n)
		}
	}
	return c, sc.Err()
}

// parseCoveragePy reads coverage.py's JSON report (pytest-cov --cov-report=json): executed and missing lines per
// file, relative to root. It doesn't count hits, so a line that ran has one.
func parseCoveragePy(r io.Reader, root string) (*Coverage, error) {
	var doc struct {
		Files map[string]struct {
			Executed []int `json:"executed_lines"`
			Missing  []int `json:"missing_lines"`
		} `json:"files"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	c := &Coverage{}
	for name, f := range doc.Files {
		path := absUnder(root, name)
		for _, l := range f.Executed {
			c.Hit(path, l-1, 1)
		}
		for _, l := range f.Missing {
			c.Hit(path, l-1, 0)
		}
	}
	return c, nil
}

// parseIstanbul reads istanbul's coverage-final.json (vitest, jest): statements and their counts per file. A line
// takes the count of the statements that start on it, else of the smallest statement it's inside.
func parseIstanbul(r io.Reader, root string) (*Coverage, error) {
	type pos struct {
		Line int `json:"line"`
	}
	var doc map[string]struct {
		Path         string `json:"path"`
		StatementMap map[string]struct {
			Start pos `json:"start"`
			End   pos `json:"end"`
		} `json:"statementMap"`
		S map[string]int `json:"s"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	c := &Coverage{}
	for name, f := range doc {
		path := f.Path
		if path == "" {
			path = name
		}
		path = absUnder(root, path)
		starts := map[int]bool{}
		for id, st := range f.StatementMap {
			starts[st.Start.Line] = true
			c.Hit(path, st.Start.Line-1, f.S[id])
		}
		inner := map[int][2]int{} // line → span and count of the smallest statement around it
		for id, st := range f.StatementMap {
			span := st.End.Line - st.Start.Line
			for l := st.Start.Line + 1; l <= st.End.Line; l++ {
				if starts[l] {
					continue
				}
				if old, ok := inner[l]; !ok || span < old[0] {
					inner[l] = [2]int{span, f.S[id]}
				}
			}
		}
		for l, v := range inner {
			c.Hit(path, l-1, v[1])
		}
	}
	return c, nil
}

// parseLLVMCov reads `cargo llvm-cov --json` (llvm-cov export): each file's segments, [line, col, count, has count,
// region entry, gap]. A line's count is the largest of the segments that start on it or run over it.
func parseLLVMCov(r io.Reader, root string) (*Coverage, error) {
	var doc struct {
		Data []struct {
			Files []struct {
				Filename string  `json:"filename"`
				Segments [][]any `json:"segments"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	c := &Coverage{}
	for _, d := range doc.Data {
		for _, f := range d.Files {
			path := absUnder(root, f.Filename)
			type seg struct {
				line, count     int
				hasCount, entry bool
				gap             bool
			}
			var segs []seg
			for _, s := range f.Segments {
				if len(s) < 6 {
					continue
				}
				num := func(v any) int { x, _ := v.(float64); return int(x) }
				flag := func(v any) bool { b, _ := v.(bool); return b }
				segs = append(segs, seg{num(s[0]), num(s[2]), flag(s[3]), flag(s[4]), flag(s[5])})
			}
			// Walk lines from the first segment to the last, carrying the region that's open.
			if len(segs) == 0 {
				continue
			}
			var open *seg
			i := 0
			for line := segs[0].line; line <= segs[len(segs)-1].line; line++ {
				counted, best := false, 0
				if open != nil && open.hasCount && !open.gap {
					counted, best = true, open.count
				}
				for ; i < len(segs) && segs[i].line == line; i++ {
					s := segs[i]
					if s.hasCount && s.entry && !s.gap {
						if !counted || s.count > best {
							best = s.count
						}
						counted = true
					}
					open = &segs[i]
				}
				if counted {
					c.Hit(path, line-1, best)
				}
			}
		}
	}
	return c, nil
}

func absUnder(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(root, filepath.FromSlash(p))
}
