package diagram

import (
	"regexp"
	"strings"
)

// Direction is the way a flowchart's ranks run.
type Direction uint8

const (
	TB Direction = iota // top to bottom (TD too)
	BT
	LR
	RL
)

// Shape is a flowchart node's outline.
type Shape uint8

const (
	Rect         Shape = iota // [text]
	Round                     // (text)
	Stadium                   // ([text])
	Subroutine                // [[text]]
	Cylinder                  // [(text)]
	Circle                    // ((text))
	DoubleCircle              // (((text)))
	Flag                      // >text]
	Diamond                   // {text}
	Hexagon                   // {{text}}
	Lean                      // [/text/]
	LeanLeft                  // [\text\]
	Trapezoid                 // [/text\]
	TrapezoidAlt              // [\text/]
)

// LineStyle is how an edge is drawn.
type LineStyle uint8

const (
	Solid LineStyle = iota
	Dotted
	Thick
	Invisible
)

// Marker is what an edge has at one end.
type Marker uint8

const (
	NoMarker Marker = iota
	ArrowHead
	CrossMark
	CircleMark
)

type Node struct {
	ID    string
	Label string
	Shape Shape
	Group int // the innermost subgraph it's in, or -1
}

// Edge joins two nodes, or a node and a subgraph: To or From may name a Group.
type Edge struct {
	From, To   string
	Label      string
	Line       LineStyle
	Start, End Marker
	Length     int // ranks it asks to span, from its extra dashes: 1 for -->
}

// Group is a subgraph.
type Group struct {
	ID, Title string
	Parent    int // -1 at the top
}

type Flowchart struct {
	Dir    Direction
	Nodes  []Node
	Edges  []Edge
	Groups []Group
}

// Node returns the node with id, or nil.
func (f *Flowchart) Node(id string) *Node {
	for i := range f.Nodes {
		if f.Nodes[i].ID == id {
			return &f.Nodes[i]
		}
	}
	return nil
}

func (f *Flowchart) group(id string) int {
	for i, g := range f.Groups {
		if g.ID == id {
			return i
		}
	}
	return -1
}

type flowParser struct {
	f        *Flowchart
	index    map[string]int
	labelled map[string]bool
	stack    []int
}

func parseFlowchart(head []string, lines []line) (*Flowchart, error) {
	f := &Flowchart{}
	if len(head) > 1 {
		switch strings.ToUpper(head[1]) {
		case "TB", "TD":
		case "BT":
			f.Dir = BT
		case "LR":
			f.Dir = LR
		case "RL":
			f.Dir = RL
		default:
			return nil, &SyntaxError{1, "unknown direction " + head[1]}
		}
	}
	p := &flowParser{f: f, index: map[string]int{}, labelled: map[string]bool{}}
	for _, l := range lines {
		for _, st := range splitStatements(l.text) {
			st = strings.TrimSpace(st)
			if st == "" {
				continue
			}
			if err := p.statement(st); err != "" {
				return nil, &SyntaxError{l.n, err}
			}
		}
	}
	if len(p.stack) > 0 {
		return nil, &SyntaxError{lines[len(lines)-1].n, "subgraph without end"}
	}
	p.groupEnds()
	if len(f.Nodes) == 0 {
		return nil, errEmpty
	}
	return f, nil
}

var subgraphHead = regexp.MustCompile(`^(\S+?)\s*\[(.*)\]$`)

func (p *flowParser) statement(st string) string {
	word, rest, _ := strings.Cut(st, " ")
	switch word {
	case "subgraph":
		rest = strings.TrimSpace(rest)
		g := Group{ID: rest, Title: labelText(rest), Parent: p.current()}
		if m := subgraphHead.FindStringSubmatch(rest); m != nil {
			g.ID, g.Title = m[1], labelText(m[2])
		} else if strings.HasPrefix(rest, `"`) {
			g.ID = g.Title
		}
		p.f.Groups = append(p.f.Groups, g)
		p.stack = append(p.stack, len(p.f.Groups)-1)
		return ""
	case "end":
		if len(p.stack) == 0 {
			return "end without subgraph"
		}
		p.stack = p.stack[:len(p.stack)-1]
		return ""
	case "direction", "classDef", "class", "style", "linkStyle", "click", "accTitle", "accTitle:", "accDescr", "accDescr:", "title":
		return ""
	}
	return p.chain(st)
}

func (p *flowParser) current() int {
	if len(p.stack) == 0 {
		return -1
	}
	return p.stack[len(p.stack)-1]
}

// chain reads `a & b --> c -- text --> d`: groups of nodes joined by links.
func (p *flowParser) chain(s string) string {
	s = strings.TrimSpace(s)
	from, s, err := p.nodeGroup(s)
	if err != "" {
		return err
	}
	for {
		s = strings.TrimSpace(s)
		if s == "" {
			return ""
		}
		e, rest, ok := parseLink(s)
		if !ok {
			return "expected a link at " + quoteStart(s)
		}
		to, rest, err := p.nodeGroup(strings.TrimSpace(rest))
		if err != "" {
			return err
		}
		for _, a := range from {
			for _, b := range to {
				e := e
				e.From, e.To = a, b
				p.f.Edges = append(p.f.Edges, e)
			}
		}
		from, s = to, rest
	}
}

func quoteStart(s string) string {
	if len(s) > 12 {
		s = s[:12] + "…"
	}
	return `"` + s + `"`
}

func (p *flowParser) nodeGroup(s string) ([]string, string, string) {
	var ids []string
	for {
		id, rest, err := p.node(s)
		if err != "" {
			return nil, "", err
		}
		ids = append(ids, id)
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "&") {
			return ids, rest, ""
		}
		s = strings.TrimSpace(rest[1:])
	}
}

// shapes are the node shapes' brackets, longest opener first.
var shapes = []struct {
	open, close string
	shape       Shape
}{
	{"(((", ")))", DoubleCircle},
	{"((", "))", Circle},
	{"([", "])", Stadium},
	{"[[", "]]", Subroutine},
	{"[(", ")]", Cylinder},
	{"{{", "}}", Hexagon},
	{"[/", "/]", Lean},
	{"[/", `\]`, Trapezoid},
	{`[\`, `\]`, LeanLeft},
	{`[\`, "/]", TrapezoidAlt},
	{"[", "]", Rect},
	{"(", ")", Round},
	{"{", "}", Diamond},
	{">", "]", Flag},
}

func isLinkStart(s string) bool {
	for _, p := range []string{"--", "==", "-.", "~~~", "<-", "<="} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// node reads one node, `id` or `id[label]` in any shape, and returns what follows it.
func (p *flowParser) node(s string) (string, string, string) {
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || strings.IndexByte("[({>&|\"", c) >= 0 || isLinkStart(s[i:]) || strings.HasPrefix(s[i:], ":::") {
			break
		}
		i++
	}
	id := s[:i]
	if id == "" {
		return "", "", "expected a node at " + quoteStart(s)
	}
	s = s[i:]
	label, shape, has := "", Rect, false
	for _, sh := range shapes {
		if !strings.HasPrefix(s, sh.open) {
			continue
		}
		body := s[len(sh.open):]
		end := -1
		if q := strings.TrimLeft(body, " "); strings.HasPrefix(q, `"`) {
			if j := strings.Index(q[1:], `"`); j >= 0 {
				after := len(body) - len(q) + 1 + j + 1
				if k := strings.Index(body[after:], sh.close); k >= 0 && strings.TrimSpace(body[after:after+k]) == "" {
					end = after + k
				}
			}
		} else {
			end = strings.Index(body, sh.close)
			if sh.open == "[/" || sh.open == `[\` {
				// the closer that comes first decides between a parallelogram and a trapezoid
				other := `\]`
				if sh.close == `\]` {
					other = "/]"
				}
				if o := strings.Index(body, other); o >= 0 && (end < 0 || o < end) {
					end = -1
				}
			}
		}
		if end < 0 {
			continue
		}
		label, shape, has = labelText(body[:end]), sh.shape, true
		s = s[len(sh.open)+end+len(sh.close):]
		break
	}
	if !has && s != "" && strings.IndexByte("[({>", s[0]) >= 0 && !strings.HasPrefix(s, ">") {
		return "", "", "unclosed shape after " + id
	}
	if strings.HasPrefix(s, ":::") {
		j := 3
		for j < len(s) && s[j] != ' ' && s[j] != '&' && !isLinkStart(s[j:]) {
			j++
		}
		s = s[j:]
	}
	p.add(id, label, shape, has)
	return id, s, ""
}

func (p *flowParser) add(id, label string, shape Shape, labelled bool) {
	i, ok := p.index[id]
	if !ok {
		i = len(p.f.Nodes)
		p.index[id] = i
		p.f.Nodes = append(p.f.Nodes, Node{ID: id, Label: id, Group: -1})
	}
	n := &p.f.Nodes[i]
	if labelled {
		n.Label, n.Shape = label, shape
		p.labelled[id] = true
	}
	if g := p.current(); g >= 0 && n.Group < 0 {
		n.Group = g
	}
}

// groupEnds turns nodes named like a subgraph, with no label of their own, into that subgraph: an edge to one ends
// on its box.
func (p *flowParser) groupEnds() {
	f := p.f
	drop := map[string]bool{}
	for _, n := range f.Nodes {
		if f.group(n.ID) >= 0 && !p.labelled[n.ID] {
			drop[n.ID] = true
		}
	}
	if len(drop) == 0 {
		return
	}
	nodes := f.Nodes[:0]
	for _, n := range f.Nodes {
		if !drop[n.ID] {
			nodes = append(nodes, n)
		}
	}
	f.Nodes = nodes
}

var (
	linkPlain = regexp.MustCompile(`^([<xo])?(?:(-{2,})|(={2,})|-(\.+)-|(~{3,}))([>xo])?`)
	linkMid   = regexp.MustCompile(`^(<)?(--|==|-\.)\s+(.*?)\s*(-{2,}[>xo]|-{3,}|={2,}>|={3,}|\.+-[>xo]?)`)
	pipeLabel = regexp.MustCompile(`^\s*\|([^|]*)\|`)
)

func marker(c string) Marker {
	switch c {
	case ">", "<":
		return ArrowHead
	case "x":
		return CrossMark
	case "o":
		return CircleMark
	}
	return NoMarker
}

// parseLink reads one link (`-->`, `---`, `-.->`, `==>`, `~~~`, `<-->`, `--o`, with a |label| or `-- label -->`)
// and returns what follows it.
func parseLink(s string) (Edge, string, bool) {
	var e Edge
	m := linkPlain.FindStringSubmatchIndex(s)
	mid := m == nil || m[1]-m[0] == 2 && m[4] >= 0 && m[12] < 0 || m[1]-m[0] == 2 && m[6] >= 0 && m[12] < 0
	if mid {
		mm := linkMid.FindStringSubmatch(s)
		if mm == nil {
			return e, "", false
		}
		e.Start = marker(mm[1])
		e.Label = labelText(mm[3])
		closer := mm[4]
		switch mm[2] {
		case "--":
			e.Line = Solid
		case "==":
			e.Line = Thick
		default:
			e.Line = Dotted
		}
		body := strings.TrimRight(closer, ">xo")
		if body != closer {
			e.End = marker(closer[len(closer)-1:])
		}
		switch e.Line {
		case Dotted:
			e.Length = max(1, strings.Count(body, "."))
		default:
			e.Length = linkLength(len(body), e.End != NoMarker)
		}
		return e, s[len(mm[0]):], true
	}
	sub := func(i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return s[m[2*i]:m[2*i+1]]
	}
	e.Start, e.End = marker(sub(1)), marker(sub(6))
	switch {
	case sub(2) != "":
		e.Line, e.Length = Solid, linkLength(len(sub(2)), e.End != NoMarker)
	case sub(3) != "":
		e.Line, e.Length = Thick, linkLength(len(sub(3)), e.End != NoMarker)
	case sub(4) != "":
		e.Line, e.Length = Dotted, len(sub(4))
	default:
		e.Line, e.Length = Invisible, len(sub(5))-2
	}
	rest := s[m[1]:]
	if pm := pipeLabel.FindStringSubmatch(rest); pm != nil {
		e.Label = labelText(pm[1])
		rest = rest[len(pm[0]):]
	}
	return e, rest, true
}

// linkLength is the ranks a link of n dashes spans: `-->` and `---` are 1, each dash more one more.
func linkLength(n int, arrow bool) int {
	if arrow {
		return max(1, n-1)
	}
	return max(1, n-2)
}
