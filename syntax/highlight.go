package syntax

import (
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/text"
)

// Span is a run of text and the highlight capture that won it, like "keyword" or "string.escape".
type Span struct {
	Range   text.Range
	Capture string
}

// hl is one highlight capture before overlaps are resolved.
type hl struct {
	start, end int
	name       string
	priority   int
	depth      int // injected text wins over the text around it
	order      int // later patterns win over earlier ones
}

const maxInjectionDepth = 3

// Highlights returns the highlighted spans in [start, end), in order and not overlapping. Where captures
// overlap, the one with the highest #set! priority wins, then text injected deeper, then the later pattern,
// as nvim's highlighter has it. Captures named with a leading "_", and spell, nospell and conceal, aren't
// highlights.
func (p *Parser) Highlights(start, end int) ([]Span, error) {
	var hls []hl
	if err := p.collect(start, end, &hls); err != nil {
		return nil, err
	}
	return resolve(hls), nil
}

// highlightChunk bounds one highlight query. Compiled wasm can't be preempted, so a long query would hold up
// the Go runtime's stop-the-world, and every goroutine with it; chunks keep each call under a millisecond
// (tree-sitter's matcher costs about 1.6 µs a match under wazero, dense code a match every 3 or 4 bytes).
const highlightChunk = 1 << 10

// highlightsInChunks is Highlights over [0, n), queried a chunk at a time, each chunk's spans clipped to it.
func (p *Parser) highlightsInChunks(n int) ([]Span, error) { return p.highlightsIn(0, n) }

// highlightsIn is Highlights over [from, to), queried a chunk at a time, each chunk's spans clipped to it.
func (p *Parser) highlightsIn(from, to int) ([]Span, error) {
	var out []Span
	for start := from; start < to; start += highlightChunk {
		end := min(start+highlightChunk, to)
		spans, err := p.Highlights(start, end)
		if err != nil {
			return out, err
		}
		for _, s := range spans {
			s.Range.Start, s.Range.End = max(s.Range.Start, start), min(s.Range.End, end)
			if s.Range.Start >= s.Range.End {
				continue
			}
			if k := len(out) - 1; k >= 0 && out[k].Capture == s.Capture && out[k].Range.End == s.Range.Start {
				out[k].Range.End = s.Range.End
				continue
			}
			out = append(out, s)
		}
	}
	return out, nil
}

func (p *Parser) collect(start, end int, hls *[]hl) error {
	p.queried += end - start
	_, ms, err := p.run("highlights", start, end)
	if err != nil {
		return err
	}
	for _, mt := range ms {
		pat := mt.q.patterns[mt.pattern]
		prio := 100
		if v, ok := pat.set["priority"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				prio = n
			}
		}
		for _, c := range mt.captures {
			name := mt.q.captures[c.index]
			if strings.HasPrefix(name, "_") || name == "spell" || name == "nospell" || name == "conceal" {
				continue
			}
			s, e := applyOffset(p.src, c, pat.offsets[c.index])
			if s >= e {
				continue
			}
			*hls = append(*hls, hl{start: s, end: e, name: name, priority: prio, depth: p.depth, order: mt.q.orderOf(mt.pattern)})
		}
	}
	if p.depth >= maxInjectionDepth {
		return nil
	}
	return p.injections(start, end, func(ip *Parser) error { return ip.collect(start, end, hls) })
}

// applyOffset moves a capture's ends by an #offset! directive's rows and columns.
func applyOffset(src []byte, c capture, o [4]int) (int, int) {
	if o == [4]int{} {
		return c.start, c.end
	}
	move := func(off, row, col int) int {
		if row == 0 {
			return max(0, min(len(src), off+col))
		}
		return off
	}
	return move(c.start, o[0], o[1]), move(c.end, o[2], o[3])
}

// injections parses the text the language's injections query hands to other languages, and calls fn with
// each language's parser once its tree covers that text.
func (p *Parser) injections(start, end int, fn func(*Parser) error) error {
	q, ms, err := p.run("injections", start, end)
	if err != nil || q == nil {
		return err
	}
	type group struct {
		lang     string
		ranges   [][2]int
		combined bool
	}
	var groups []group
	byLang := map[string]int{}
	for _, mt := range ms {
		pat := mt.q.patterns[mt.pattern]
		lang := pat.set["injection.language"]
		var content [][2]int
		for _, c := range mt.captures {
			switch mt.q.captures[c.index] {
			case "injection.language":
				lang = strings.ToLower(string(p.src[c.start:c.end]))
			case "injection.content":
				s, e := applyOffset(p.src, c, pat.offsets[c.index])
				if s < e {
					content = append(content, [2]int{s, e})
				}
			}
		}
		if lang == "" || len(content) == 0 {
			continue
		}
		if _, ok := p.lang.lookup(lang); !ok {
			continue
		}
		if _, combined := pat.set["injection.combined"]; combined {
			if i, ok := byLang[lang]; ok {
				groups[i].ranges = append(groups[i].ranges, content...)
				continue
			}
			byLang[lang] = len(groups)
			groups = append(groups, group{lang: lang, ranges: content, combined: true})
			continue
		}
		for _, r := range content {
			groups = append(groups, group{lang: lang, ranges: [][2]int{r}})
		}
	}
	for _, g := range groups {
		ip, err := p.injectedParser(g.lang)
		if err != nil {
			return err
		}
		slices.SortFunc(g.ranges, func(a, b [2]int) int { return a[0] - b[0] })
		ip.parseRanges(p.src, g.ranges)
		if err := fn(ip); err != nil {
			return err
		}
	}
	return nil
}

func (p *Parser) injectedParser(name string) (*Parser, error) {
	if ip, ok := p.injected[name]; ok {
		return ip, nil
	}
	l, _ := p.lang.lookup(name)
	ip, err := NewParser(l)
	if err != nil {
		return nil, err
	}
	ip.depth = p.depth + 1
	if p.injected == nil {
		p.injected = map[string]*Parser{}
	}
	p.injected[name] = ip
	return ip, nil
}

func (a hl) beats(b hl) bool {
	if a.priority != b.priority {
		return a.priority > b.priority
	}
	if a.depth != b.depth {
		return a.depth > b.depth
	}
	return a.order >= b.order
}

// resolve turns overlapping captures into non-overlapping spans, each taking the winning capture.
func resolve(hls []hl) []Span {
	if len(hls) == 0 {
		return nil
	}
	var cuts []int
	for _, h := range hls {
		cuts = append(cuts, h.start, h.end)
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)
	slices.SortStableFunc(hls, func(a, b hl) int { return a.start - b.start })
	var out []Span
	var active []hl
	next := 0
	for i := 0; i+1 < len(cuts); i++ {
		s, e := cuts[i], cuts[i+1]
		for next < len(hls) && hls[next].start <= s {
			active = append(active, hls[next])
			next++
		}
		active = slices.DeleteFunc(active, func(h hl) bool { return h.end <= s })
		var win *hl
		for j := range active {
			if win == nil || active[j].beats(*win) {
				win = &active[j]
			}
		}
		if win == nil {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Range.End == s && out[n-1].Capture == win.name {
			out[n-1].Range.End = e
			continue
		}
		out = append(out, Span{Range: text.Range{Start: s, End: e}, Capture: win.name})
	}
	return out
}
