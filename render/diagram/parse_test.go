package diagram

import (
	"errors"
	"slices"
	"testing"
)

func flow(t *testing.T, src string) *Flowchart {
	t.Helper()
	d, err := Parse(src)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	if d.Flow == nil {
		t.Fatalf("%q isn't a flowchart", src)
	}
	return d.Flow
}

func edgeList(f *Flowchart) []string {
	var out []string
	for _, e := range f.Edges {
		out = append(out, e.From+">"+e.To)
	}
	return out
}

// ref:   mermaid/flowchart.md#Direction
// quote: "TB - Top to bottom"
func TestFlowchartDirection(t *testing.T) {
	for src, want := range map[string]Direction{
		"flowchart TD\n  Start --> Stop": TB, "flowchart TB\n a": TB, "graph LR\n a": LR, "flowchart BT\n a": BT, "flowchart RL\n a": RL, "graph\n a": TB,
	} {
		if f := flow(t, src); f.Dir != want {
			t.Errorf("%q: direction %d, want %d", src, f.Dir, want)
		}
	}
}

// ref:   mermaid/flowchart.md#Node shapes
// quote: "id1[(Database)]"
func TestFlowchartShapes(t *testing.T) {
	for src, want := range map[string]Shape{
		"id1[This is the text in the box]": Rect, "id1(This is the text in the box)": Round, "id1([This is the text in the box])": Stadium,
		"id1[[This is the text in the box]]": Subroutine, "id1[(This is the text in the box)]": Cylinder, "id1((This is the text in the box))": Circle,
		"id1(((This is the text in the box)))": DoubleCircle, "id1>This is the text in the box]": Flag, "id1{This is the text in the box}": Diamond,
		"id1{{This is the text in the box}}": Hexagon, "id1[/This is the text in the box/]": Lean, `id1[\This is the text in the box\]`: LeanLeft,
		`id1[/This is the text in the box\]`: Trapezoid, `id1[\This is the text in the box/]`: TrapezoidAlt,
	} {
		f := flow(t, "flowchart LR\n    "+src)
		if len(f.Nodes) != 1 || f.Nodes[0].ID != "id1" || f.Nodes[0].Shape != want || f.Nodes[0].Label != "This is the text in the box" {
			t.Errorf("%q: %+v, want shape %d", src, f.Nodes, want)
		}
	}
}

// ref:   mermaid/flowchart.md#A node (default)
// quote: "The id is what is displayed in the box."
func TestFlowchartNodeText(t *testing.T) {
	f := flow(t, "flowchart LR\n    id")
	if f.Nodes[0].Label != "id" {
		t.Errorf("label %q", f.Nodes[0].Label)
	}
	// ref:   mermaid/flowchart.md#A node with text
	// quote: "it is the last text found for the node that will be used"
	f = flow(t, "flowchart LR\n  a[One] --> b\n  a[Two]\n  a --> c")
	if f.Nodes[0].Label != "Two" {
		t.Errorf("the last text: %q", f.Nodes[0].Label)
	}
	// ref:   mermaid/flowchart.md#Special characters that break syntax
	// quote: "It is possible to put text within quotes in order to render more troublesome characters."
	f = flow(t, "flowchart LR\n    id1[\"This is the (text) in the box\"]")
	if f.Nodes[0].Label != "This is the (text) in the box" {
		t.Errorf("quoted: %q", f.Nodes[0].Label)
	}
	// ref:   mermaid/flowchart.md#Entity codes to escape characters
	// quote: "Numbers given are base 10, so `#` can be encoded as `#35;`."
	f = flow(t, "flowchart LR\n        A[\"A double quote:#quot;\"] --> B[\"A dec char:#9829;\"]")
	if f.Nodes[0].Label != `A double quote:"` || f.Nodes[1].Label != "A dec char:♥" {
		t.Errorf("entities: %q %q", f.Nodes[0].Label, f.Nodes[1].Label)
	}
	f = flow(t, "flowchart LR\n  a[one<br>two<br/>three]")
	if f.Nodes[0].Label != "one\ntwo\nthree" {
		t.Errorf("<br>: %q", f.Nodes[0].Label)
	}
	// ref:   mermaid/flowchart.md#Markdown formatting
	// quote: "Use double quotes and backticks"
	f = flow(t, "flowchart LR\n    markdown[\"`This **is** _Markdown_`\"]\n    newLines[\"`Line1\n    Line 2\n    Line 3`\"]\n    markdown --> newLines")
	if f.Nodes[0].Label != "This is _Markdown_" || f.Nodes[1].Label != "Line1\nLine 2\nLine 3" || len(f.Edges) != 1 {
		t.Errorf("markdown strings: %+v", f)
	}
}

// ref:   mermaid/flowchart.md#Links between nodes
// quote: "Nodes can be connected with links/edges."
func TestFlowchartLinks(t *testing.T) {
	for _, tt := range []struct {
		src        string
		line       LineStyle
		start, end Marker
		label      string
		length     int
	}{
		{"A-->B", Solid, NoMarker, ArrowHead, "", 1},
		{"A --- B", Solid, NoMarker, NoMarker, "", 1},
		{"A-- This is the text! ---B", Solid, NoMarker, NoMarker, "This is the text!", 1},
		{"A---|This is the text|B", Solid, NoMarker, NoMarker, "This is the text", 1},
		{"A-->|text|B", Solid, NoMarker, ArrowHead, "text", 1},
		{"A-- text -->B", Solid, NoMarker, ArrowHead, "text", 1},
		{"A-.->B;", Dotted, NoMarker, ArrowHead, "", 1},
		{"A-. text .-> B", Dotted, NoMarker, ArrowHead, "text", 1},
		{"A ==> B", Thick, NoMarker, ArrowHead, "", 1},
		{"A == text ==> B", Thick, NoMarker, ArrowHead, "text", 1},
		{"A ~~~ B", Invisible, NoMarker, NoMarker, "", 1},
		{"A --o B", Solid, NoMarker, CircleMark, "", 1},
		{"A --x B", Solid, NoMarker, CrossMark, "", 1},
		{"B <--> C", Solid, ArrowHead, ArrowHead, "", 1},
		{"A o--o B", Solid, CircleMark, CircleMark, "", 1},
		// ref:   mermaid/flowchart.md#Minimum length of a link
		// quote: "two extra dashes are added in the link from node _B_"
		{"B ---->|No| E[End]", Solid, NoMarker, ArrowHead, "No", 3},
		{"B -- No ----> E[End]", Solid, NoMarker, ArrowHead, "No", 3},
		{"A ----- B", Solid, NoMarker, NoMarker, "", 3},
		{"A -...-> B", Dotted, NoMarker, ArrowHead, "", 3},
		{"A ===> B", Thick, NoMarker, ArrowHead, "", 2},
	} {
		f := flow(t, "flowchart LR\n   "+tt.src)
		if len(f.Edges) != 1 {
			t.Errorf("%q: edges %v", tt.src, edgeList(f))
			continue
		}
		e := f.Edges[0]
		if e.Line != tt.line || e.Start != tt.start || e.End != tt.end || e.Label != tt.label || e.Length != tt.length {
			t.Errorf("%q: %+v", tt.src, e)
		}
		if len(f.Nodes) != 2 {
			t.Errorf("%q: nodes %+v", tt.src, f.Nodes)
		}
	}
}

// ref:   mermaid/flowchart.md#Chaining of links
// quote: "It is possible declare many links in the same line as per below:"
func TestFlowchartChains(t *testing.T) {
	f := flow(t, "flowchart LR\n   A -- text --> B -- text2 --> C")
	if got := edgeList(f); !slices.Equal(got, []string{"A>B", "B>C"}) || f.Edges[1].Label != "text2" {
		t.Errorf("chain: %v %+v", got, f.Edges)
	}
	f = flow(t, "flowchart LR\n   a --> b & c--> d")
	if got := edgeList(f); !slices.Equal(got, []string{"a>b", "a>c", "b>d", "c>d"}) {
		t.Errorf("a --> b & c--> d: %v", got)
	}
	// ref:   mermaid/flowchart.md#Chaining of links
	// quote: "If you describe the same diagram using the basic syntax, it will take four lines."
	f = flow(t, "flowchart TB\n    A & B--> C & D")
	if got := edgeList(f); !slices.Equal(got, []string{"A>C", "A>D", "B>C", "B>D"}) {
		t.Errorf("A & B--> C & D: %v", got)
	}
	f = flow(t, "flowchart LR\n  usage.Meter --> pricing.Round --> billing.Invoice\n  billing-ui")
	if f.Nodes[0].ID != "usage.Meter" || f.Nodes[3].ID != "billing-ui" || len(f.Edges) != 2 {
		t.Errorf("dotted and dashed ids: %+v", f.Nodes)
	}
}

// ref:   mermaid/flowchart.md#Subgraphs
// quote: "You can also set an explicit id for the subgraph."
func TestFlowchartSubgraphs(t *testing.T) {
	f := flow(t, "flowchart TB\n    c1-->a2\n    subgraph one\n    a1-->a2\n    end\n    subgraph two\n    b1-->b2\n    end\n    subgraph three\n    c1-->c2\n    end")
	if len(f.Groups) != 3 || f.Groups[0].Title != "one" {
		t.Fatalf("groups %+v", f.Groups)
	}
	in := map[string]int{}
	for _, n := range f.Nodes {
		in[n.ID] = n.Group
	}
	// a2 is named at the top first, then inside one; c1 is named at the top and inside three.
	if in["a1"] != 0 || in["a2"] != 0 || in["b1"] != 1 || in["c1"] != 2 || in["c2"] != 2 {
		t.Errorf("membership %v", in)
	}
	f = flow(t, "flowchart TB\n    c1-->a2\n    subgraph ide1 [one]\n    a1-->a2\n    end")
	if f.Groups[0].ID != "ide1" || f.Groups[0].Title != "one" {
		t.Errorf("explicit id: %+v", f.Groups)
	}
	// ref:   mermaid/flowchart.md#flowcharts
	// quote: "it is also possible to set edges to and from subgraphs"
	f = flow(t, "flowchart TB\n    c1-->a2\n    subgraph one\n    a1-->a2\n    end\n    subgraph two\n    b1-->b2\n    end\n    one --> two\n    two --> c2")
	if f.Node("one") != nil || f.Node("two") != nil {
		t.Errorf("a subgraph's id made a node: %+v", f.Nodes)
	}
	if got := edgeList(f); !slices.Contains(got, "one>two") || !slices.Contains(got, "two>c2") {
		t.Errorf("edges to subgraphs: %v", got)
	}
	f = flow(t, "flowchart LR\n  subgraph TOP\n    direction TB\n    subgraph B1\n        i1 -->f1\n    end\n  end\n  A --> TOP --> B")
	if len(f.Groups) != 2 || f.Groups[1].Parent != 0 || f.Node("i1").Group != 1 {
		t.Errorf("nested: %+v %+v", f.Groups, f.Nodes)
	}
}

// ref:   mermaid/flowchart.md#Comments
// quote: "must be prefaced with `%%` (double percent signs)"
func TestFlowchartIgnores(t *testing.T) {
	f := flow(t, "flowchart LR\n%% this is a comment A -- text --> B{node}\n   A -- text --> B -- text2 --> C\n  classDef someclass fill:#f96\n"+
		"  style A fill:#f9f,stroke:#333\n  click A callback \"Tooltip\"\n  linkStyle 1 color:blue;\n  A:::someclass --> D\n  B[fa:fa-twitter] %% standard icon")
	if got := edgeList(f); !slices.Equal(got, []string{"A>B", "B>C", "A>D"}) {
		t.Errorf("edges %v", got)
	}
	if f.Node("A").Label != "A" || f.Node("B").Label != "fa:fa-twitter" {
		t.Errorf("nodes %+v", f.Nodes)
	}
}

func TestParseErrors(t *testing.T) {
	var un *UnsupportedError
	for _, src := range []string{"gantt\n  title A", "pie title Pets\n  \"Dogs\" : 386", "classDiagram\n  A <|-- B", "stateDiagram-v2\n  [*] --> A", "erDiagram\n  A ||--o{ B : has"} {
		if _, err := Parse(src); !errors.As(err, &un) {
			t.Errorf("%q: %v", src, err)
		}
	}
	var syn *SyntaxError
	for _, src := range []string{"flowchart LR\n  A --> ", "flowchart LR\n  subgraph x\n  A", "flowchart LR\n  A[open", "flowchart XX\n A", "sequenceDiagram\n  Alice says hi", "flowchart LR\n end"} {
		if _, err := Parse(src); !errors.As(err, &syn) {
			t.Errorf("%q: %v", src, err)
		}
	}
	if _, err := Parse("  \n%% only a comment\n"); err == nil {
		t.Error("an empty source parsed")
	}
}

// ref:   mermaid/sequenceDiagram.md#Participants
// quote: "The participants or actors are rendered in order of appearance in the diagram source text."
func TestSequenceParticipants(t *testing.T) {
	d, err := Parse("sequenceDiagram\n    participant Alice\n    participant Bob\n    Bob->>Alice: Hi Alice\n    Alice->>Bob: Hi Bob\n    Bob->>Carl: hi")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range d.Seq.Participants {
		ids = append(ids, p.ID)
	}
	if !slices.Equal(ids, []string{"Alice", "Bob", "Carl"}) {
		t.Errorf("order %v", ids)
	}
	// ref:   mermaid/sequenceDiagram.md#Aliases
	// quote: "The actor can have a convenient identifier and a descriptive label."
	d, _ = Parse("sequenceDiagram\n    participant A as Alice\n    actor J as John\n    A->>J: Hello John, how are you?")
	if p := d.Seq.Participants; p[0].Label != "Alice" || p[1].Label != "John" || !p[1].Actor || p[0].Actor {
		t.Errorf("aliases %+v", p)
	}
}

// ref:   mermaid/sequenceDiagram.md#Messages
// quote: "There are ten types of arrows currently supported:"
func TestSequenceMessages(t *testing.T) {
	for _, tt := range []struct {
		arrow  string
		dotted bool
		head   Marker
		async  bool
	}{
		{"->", false, NoMarker, false}, {"-->", true, NoMarker, false}, {"->>", false, ArrowHead, false}, {"-->>", true, ArrowHead, false},
		{"-x", false, CrossMark, false}, {"--x", true, CrossMark, false}, {"-)", false, ArrowHead, true}, {"--)", true, ArrowHead, true},
		{"<<->>", false, ArrowHead, false}, {"<<-->>", true, ArrowHead, false},
	} {
		d, err := Parse("sequenceDiagram\n    Alice" + tt.arrow + "John: Hello John, how are you?")
		if err != nil {
			t.Errorf("%s: %v", tt.arrow, err)
			continue
		}
		it := d.Seq.Items[0]
		if it.Kind != Message || it.From != "Alice" || it.To != "John" || it.Text != "Hello John, how are you?" || it.Dotted != tt.dotted || it.Head != tt.head ||
			it.Async != tt.async || it.Both != (tt.arrow[0] == '<') {
			t.Errorf("%s: %+v", tt.arrow, it)
		}
	}
	// ref:   mermaid/sequenceDiagram.md#Activations
	// quote: "There is also a shortcut notation by appending `+`/`-` suffix to the message arrow:"
	d, _ := Parse("sequenceDiagram\n    Alice->>+John: Hello John, how are you?\n    John-->>-Alice: Great!\n    activate John\n    deactivate John")
	if it := d.Seq.Items; !it[0].On || !it[1].Off || it[1].To != "Alice" || it[2].Kind != Activate || it[3].Kind != Deactivate || it[3].From != "John" {
		t.Errorf("activations %+v", it)
	}
}

// ref:   mermaid/sequenceDiagram.md#Notes
// quote: "It is also possible to create notes spanning two participants:"
func TestSequenceNotesAndBlocks(t *testing.T) {
	d, err := Parse("sequenceDiagram\n    autonumber\n    Alice->John: Hello John, how are you?\n    Note over Alice,John: A typical interaction<br/>But now in two lines\n" +
		"    Note right of John: Text in note\n    alt is sick\n        Bob->>Alice: Not so good :(\n    else is well\n        Bob->>Alice: Feeling fresh like a daisy\n    end\n" +
		"    loop Every minute\n        John-->Alice: Great!\n    end")
	if err != nil {
		t.Fatal(err)
	}
	s := d.Seq
	it := s.Items
	if !s.Autonumber || it[1].Kind != Note || it[1].Side != Over || it[1].From != "Alice" || it[1].To != "John" || it[1].Text != "A typical interaction\nBut now in two lines" {
		t.Errorf("note over %+v", it[1])
	}
	if it[2].Side != RightOf || it[2].From != "John" {
		t.Errorf("note right %+v", it[2])
	}
	if it[3].Kind != BlockStart || it[3].Block != "alt" || it[3].Text != "is sick" || it[5].Kind != BlockElse || it[5].Text != "is well" || it[7].Kind != BlockEnd {
		t.Errorf("alt %+v", it[3:8])
	}
	if it[4].Text != "Not so good :(" {
		t.Errorf("a message with a colon in it: %q", it[4].Text)
	}
	if it[8].Block != "loop" || it[8].Text != "Every minute" {
		t.Errorf("loop %+v", it[8])
	}
}
