package diagram

import (
	"regexp"
	"strings"
)

type Participant struct {
	ID, Label string
	Actor     bool
}

// SeqKind is what a sequence statement is.
type SeqKind uint8

const (
	Message SeqKind = iota
	Note
	BlockStart // loop, alt, opt, par, critical, break, rect
	BlockElse  // else, and, option
	BlockEnd
	Activate
	Deactivate
)

// NoteSide is where a note sits.
type NoteSide uint8

const (
	RightOf NoteSide = iota
	LeftOf
	Over
)

// SeqItem is one statement of a sequence diagram, in order.
type SeqItem struct {
	Kind     SeqKind
	From, To string // a message's ends; a note's participants (To is "" for one); an activation's participant
	Text     string // a message's or note's text; a block's condition
	Dotted   bool
	Head     Marker // ArrowHead (->>), CrossMark (-x), CircleMark (-) async), NoMarker (->)
	Both     bool   // <<->>
	Async    bool   // -) : an open arrowhead
	Side     NoteSide
	Block    string // loop, alt, opt, par, critical, break, rect; else, and, option
	On, Off  bool   // a message's +/- activation of its target / deactivation of its source
}

type Sequence struct {
	Participants []Participant
	Items        []SeqItem
	Autonumber   bool
}

var (
	seqParticipant = regexp.MustCompile(`^(?:create\s+)?(participant|actor)\s+(.+?)(?:\s+as\s+(.+))?$`)
	seqMessage     = regexp.MustCompile(`^(.+?)\s*(<<-->>|<<->>|-->>|->>|--x|-x|--\)|-\)|-->|->)\s*([+-]?)\s*(.+?)\s*:(.*)$`)
	seqNoteRe      = regexp.MustCompile(`(?i)^note\s+(right of|left of|over)\s+([^:]+?)\s*:(.*)$`)
	seqBlockRe     = regexp.MustCompile(`^(loop|alt|opt|par|critical|break|rect|else|and|option)\b\s*(.*)$`)
)

func parseSequence(lines []line) (*Sequence, error) {
	s := &Sequence{}
	known := map[string]bool{}
	use := func(id string) {
		if !known[id] {
			known[id] = true
			s.Participants = append(s.Participants, Participant{ID: id, Label: id})
		}
	}
	depth := 0
	for _, l := range lines {
		for _, st := range splitStatements(l.text) {
			st = strings.TrimSpace(st)
			if st == "" {
				continue
			}
			word, rest, _ := strings.Cut(st, " ")
			rest = strings.TrimSpace(rest)
			switch {
			case word == "autonumber":
				s.Autonumber = true
			case word == "title" || word == "accTitle:" || word == "accDescr:" || word == "destroy":
			case word == "box":
				// a box of participants is drawn as its participants
				depth++
				s.Items = append(s.Items, SeqItem{Kind: BlockStart, Block: "box"})
			case word == "activate" || word == "deactivate":
				use(rest)
				k := Activate
				if word == "deactivate" {
					k = Deactivate
				}
				s.Items = append(s.Items, SeqItem{Kind: k, From: rest})
			case word == "end":
				if depth == 0 {
					return nil, &SyntaxError{l.n, "end without a block"}
				}
				depth--
				s.Items = append(s.Items, SeqItem{Kind: BlockEnd})
			default:
				if m := seqParticipant.FindStringSubmatch(st); m != nil {
					id, label := strings.TrimSpace(m[2]), strings.TrimSpace(m[2])
					if m[3] != "" {
						label = m[3]
					}
					if !known[id] {
						known[id] = true
						s.Participants = append(s.Participants, Participant{ID: id})
					}
					for i := range s.Participants {
						if s.Participants[i].ID == id {
							s.Participants[i].Label, s.Participants[i].Actor = labelText(label), m[1] == "actor"
						}
					}
					continue
				}
				if m := seqNoteRe.FindStringSubmatch(st); m != nil {
					it := SeqItem{Kind: Note, Text: labelText(m[3])}
					switch strings.ToLower(m[1]) {
					case "left of":
						it.Side = LeftOf
					case "over":
						it.Side = Over
					}
					a, b, _ := strings.Cut(m[2], ",")
					it.From, it.To = strings.TrimSpace(a), strings.TrimSpace(b)
					use(it.From)
					if it.To != "" {
						use(it.To)
					}
					s.Items = append(s.Items, it)
					continue
				}
				if m := seqBlockRe.FindStringSubmatch(st); m != nil {
					it := SeqItem{Kind: BlockStart, Block: m[1], Text: labelText(m[2])}
					switch m[1] {
					case "else", "and", "option":
						if depth == 0 {
							return nil, &SyntaxError{l.n, m[1] + " outside a block"}
						}
						it.Kind = BlockElse
					case "rect":
						it.Text = ""
						depth++
					default:
						depth++
					}
					s.Items = append(s.Items, it)
					continue
				}
				m := seqMessage.FindStringSubmatch(st)
				if m == nil {
					return nil, &SyntaxError{l.n, "expected a message at " + quoteStart(st)}
				}
				it := SeqItem{Kind: Message, From: strings.TrimSpace(m[1]), To: strings.TrimSpace(m[4]), Text: labelText(m[5])}
				arrow := m[2]
				it.Dotted = strings.Count(arrow, "-") >= 2
				switch {
				case strings.HasPrefix(arrow, "<<"):
					it.Head, it.Both = ArrowHead, true
				case strings.HasSuffix(arrow, ">>"):
					it.Head = ArrowHead
				case strings.HasSuffix(arrow, "x"):
					it.Head = CrossMark
				case strings.HasSuffix(arrow, ")"):
					it.Head, it.Async = ArrowHead, true
				}
				it.On, it.Off = m[3] == "+", m[3] == "-"
				use(it.From)
				use(it.To)
				s.Items = append(s.Items, it)
			}
		}
	}
	if depth != 0 {
		return nil, &SyntaxError{lines[len(lines)-1].n, "block without end"}
	}
	if len(s.Participants) == 0 {
		return nil, errEmpty
	}
	return s, nil
}
