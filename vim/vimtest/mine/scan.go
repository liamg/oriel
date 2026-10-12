package mine

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/vim/vimtest"
)

// Reject says why keys can't become a case.
type Reject struct{ Category, Detail string }

func (r *Reject) Error() string { return r.Category + ": " + r.Detail }

func reject(cat, format string, args ...any) *Reject {
	return &Reject{cat, fmt.Sprintf(format, args...)}
}

// id names a key for the tables below: "a", "C-r", "Esc", "S-Up".
func id(k vimtest.Key) string {
	if k.Name == "" && k.Mods == 0 {
		return string(k.Rune)
	}
	var b strings.Builder
	if k.Mods&vimtest.Shift != 0 {
		b.WriteString("S-")
	}
	if k.Mods&vimtest.Ctrl != 0 {
		b.WriteString("C-")
	}
	if k.Mods&vimtest.Alt != 0 {
		b.WriteString("M-")
	}
	if k.Mods&vimtest.Cmd != 0 {
		b.WriteString("D-")
	}
	if k.Name != "" {
		b.WriteString(k.Name)
	} else {
		b.WriteString(strings.ToLower(string(k.Rune)))
	}
	return b.String()
}

func set(s ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range s {
		m[x] = true
	}
	return m
}

var (
	// motions usable alone and after an operator; those taking a character are in argMotions.
	motions = set("h", "j", "k", "l", "w", "W", "b", "B", "e", "E", "0", "^", "$", "|", "(", ")", "{", "}",
		"G", "%", "-", "+", "_", "CR", "BS", " ", "Space", "Left", "Right", "Up", "Down", "Home", "End",
		"C-h", "C-j", "C-m", "C-n", "C-p", ";", ",", "NL")
	argMotions = set("f", "t", "F", "T", "'", "`")
	gMotions   = set("g", "e", "E", "_", "'", "`", "o")
	bracket    = set("(", "{", ")", "}", "[", "]", "p", "P", "'", "`", "m", "M")

	// commands that run without leaving normal mode.
	simple = set("x", "X", "D", "Y", "J", "p", "P", "u", "U", ".", "~", "Del", "C-r", "C-a", "C-x",
		"C-o", "C-i", "Tab", "Esc", "C-g", "C-l")
	gSimple  = set("J", "p", "P", ";", ",", "+", "-", "a", "8")
	inserts  = set("i", "a", "I", "A", "o", "O", "s", "S", "C", "R")
	gInserts = set("i", "I")
	textObjs = set("w", "W", "s", "p", "[", "]", "(", ")", "b", "<", ">", "t", "{", "}", "B", `"`, "'", "`")

	insertOK = set("CR", "NL", "Tab", "BS", "Del", "C-h", "C-w", "C-u", "C-t", "C-d", "C-e", "C-y", "C-a",
		"C-j", "C-m", "Left", "Right", "Up", "Down", "Home", "End")
)

// rejected maps a normal-mode command to why it's out of scope.
func rejected(cmd string) *Reject {
	switch cmd {
	case "v", "V", "C-v", "gv", "gh", "gH", "C-q", "gn", "gN":
		return reject("visual/select mode", "%s", cmd)
	case "/", "?", "*", "#", "n", "N", "g*", "g#", "gd", "gD", "[i", "]i", "[I", "]I", "[d", "]d", "[D", "]D":
		return reject("search", "%s", cmd)
	case ":", "Q", "gQ", "&", "g&", "@:":
		return reject("ex command", "%s", cmd)
	case "q", "@":
		return reject("macro", "%s", cmd)
	case "H", "M", "L", "C-d", "C-u", "C-e", "C-y", "C-f", "C-b", "gj", "gk", "g0", "g^", "g$", "gm", "gM",
		"PageUp", "PageDown", "S-Up", "S-Down":
		return reject("screen-relative", "%s", cmd)
	case "C-w":
		return reject("windows", "%s", cmd)
	case "=", "!", "gq", "gw", "g@":
		return reject("formatting/filter/opfunc", "%s", cmd)
	}
	if strings.HasPrefix(cmd, "z") || strings.HasPrefix(cmd, "Z") {
		return reject("folds/scrolling", "%s", cmd)
	}
	if strings.Contains(cmd, "Mouse") || strings.Contains(cmd, "Scroll") || strings.Contains(cmd, "Drag") || strings.Contains(cmd, "Release") {
		return reject("mouse", "%s", cmd)
	}
	return reject("unsupported command", "%s", cmd)
}

type scanner struct {
	keys []vimtest.Key
	i    int
}

var errIncomplete = reject("incomplete command", "keys end mid-command")

func (s *scanner) next() (string, *Reject) {
	if s.i >= len(s.keys) {
		return "", errIncomplete
	}
	k := s.keys[s.i]
	s.i++
	return id(k), nil
}

func (s *scanner) peek() string {
	if s.i >= len(s.keys) {
		return ""
	}
	return id(s.keys[s.i])
}

func (s *scanner) count() {
	if p := s.peek(); len(p) == 1 && p[0] >= '1' && p[0] <= '9' {
		for p := s.peek(); len(p) == 1 && p[0] >= '0' && p[0] <= '9'; p = s.peek() {
			s.i++
		}
	}
}

// Scan checks that keys are plain normal-mode editing (no visual mode, search, ex commands, macros,
// windows or screen-relative motions). ok is false when the keys end in Insert or Replace mode,
// which `:normal` and feedkeys(…, 'x') end as if <Esc> were typed.
func Scan(keys []vimtest.Key) (endsInInsert bool, err *Reject) {
	s := &scanner{keys: keys}
	for s.i < len(s.keys) {
		insert, err := s.command()
		if err != nil {
			return false, err
		}
		if insert {
			done, err := s.insert()
			if err != nil {
				return false, err
			}
			if !done {
				return true, nil
			}
		}
	}
	return false, nil
}

// command reads one normal-mode command and reports whether it starts Insert or Replace mode.
func (s *scanner) command() (bool, *Reject) {
	s.count()
	if s.peek() == `"` {
		s.i++
		r, err := s.next()
		if err != nil {
			return false, err
		}
		if len(r) != 1 || !strings.ContainsAny(r, `abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"-_`) {
			return false, reject("special register", `"%s`, r)
		}
		s.count()
	}
	c, err := s.next()
	if err != nil {
		return false, err
	}
	switch {
	case motions[c] || simple[c]:
		return false, nil
	case argMotions[c] || c == "m":
		_, err := s.next()
		return false, err
	case c == "r":
		a, err := s.next()
		if err == nil && (a == "C-v" || a == "C-q" || a == "C-k") {
			return false, reject("unsupported command", "r%s", a)
		}
		return false, err
	case inserts[c]:
		return true, nil
	case c == "d" || c == "y" || c == "c" || c == "<" || c == ">":
		return c == "c", s.motion(c)
	case c == "[" || c == "]":
		b, err := s.next()
		if err != nil {
			return false, err
		}
		if !bracket[b] {
			return false, rejected(c + b)
		}
		return false, nil
	case c == "g":
		g, err := s.next()
		if err != nil {
			return false, err
		}
		switch {
		case gMotions[g] && g != "'" && g != "`" || gSimple[g]:
			return false, nil
		case g == "'" || g == "`":
			_, err := s.next()
			return false, err
		case gInserts[g]:
			return true, nil
		case g == "~" || g == "u" || g == "U" || g == "?":
			return false, s.motion("g" + g)
		}
		return false, rejected("g" + g)
	}
	return false, rejected(c)
}

// motion reads what follows operator op: a motion, a text object, or op again for a linewise op.
func (s *scanner) motion(op string) *Reject {
	s.count()
	m, err := s.next()
	if err != nil {
		return err
	}
	switch {
	case m == op || len(op) == 2 && m == op[1:]:
		return nil
	case m == "v" || m == "V" || m == "C-v":
		return reject("visual/select mode", "%s%s (forced motion)", op, m)
	case motions[m]:
		return nil
	case argMotions[m]:
		_, err := s.next()
		return err
	case m == "i" || m == "a":
		o, err := s.next()
		if err != nil {
			return err
		}
		if !textObjs[o] {
			return reject("unsupported command", "%s%s%s", op, m, o)
		}
		return nil
	case m == "[" || m == "]":
		b, err := s.next()
		if err != nil {
			return err
		}
		if !bracket[b] || b == "p" || b == "P" {
			return rejected(m + b)
		}
		return nil
	case m == "g":
		g, err := s.next()
		if err != nil {
			return err
		}
		if len(op) == 2 && "g"+g == op {
			return nil
		}
		if gMotions[g] {
			if g == "'" || g == "`" {
				_, err := s.next()
				return err
			}
			return nil
		}
		return rejected("g" + g)
	}
	return rejected(m)
}

// insert reads Insert-mode keys up to <Esc>; done is false when the keys run out first.
func (s *scanner) insert() (done bool, err *Reject) {
	for s.i < len(s.keys) {
		k := s.keys[s.i]
		c := id(k)
		s.i++
		switch {
		case c == "Esc" || c == "C-[" || c == "C-c":
			return true, nil
		case k.Name == "" && k.Mods == 0, insertOK[c]:
		case c == "C-r":
			r, err := s.next()
			if err != nil {
				return false, err
			}
			if len(r) != 1 || !strings.ContainsAny(r, `abcdefghijklmnopqrstuvwxyz0123456789"-`) {
				return false, reject("special register", "<C-R>%s", r)
			}
		case c == "C-n" || c == "C-p" || c == "C-x":
			return false, reject("insert completion", "%s", c)
		default:
			return false, reject("insert-mode key", "%s", c)
		}
	}
	return false, nil
}
