package mine

import (
	"fmt"
	"regexp"
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
		"C-h", "C-j", "C-m", "C-n", "C-p", ";", ",", "NL", "n", "N", "*", "#")
	argMotions = set("f", "t", "F", "T", "'", "`")
	gMotions   = set("g", "e", "E", "_", "'", "`", "o", "*", "#")
	bracket    = set("(", "{", ")", "}", "[", "]", "p", "P", "'", "`", "m", "M")

	// commands that run without leaving normal mode.
	simple = set("x", "X", "D", "Y", "J", "p", "P", "u", "U", ".", "~", "Del", "C-r", "C-a", "C-x",
		"C-o", "C-i", "Tab", "Esc", "C-g", "C-l", "&")
	gSimple  = set("J", "p", "P", ";", ",", "+", "-", "a", "8", "&")
	inserts  = set("i", "a", "I", "A", "o", "O", "s", "S", "C", "R")
	gInserts = set("i", "I")
	textObjs = set("w", "W", "s", "p", "[", "]", "(", ")", "b", "<", ">", "t", "{", "}", "B", `"`, "'", "`")

	insertOK = set("CR", "NL", "Tab", "BS", "Del", "C-h", "C-w", "C-u", "C-t", "C-d", "C-e", "C-y", "C-a",
		"C-j", "C-m", "Left", "Right", "Up", "Down", "Home", "End")

	// Visual-mode commands that end Visual mode; visualInserts also start Insert mode.
	visualOps     = set("d", "y", "<", ">", "x", "X", "D", "Y", "J", "p", "P", "~", "u", "U", "Del")
	visualInserts = set("c", "s", "C", "S", "R", "I", "A")
	visualModes   = set("v", "V", "C-v")
)

// rejected maps a command to why it's out of scope.
func rejected(cmd string) *Reject {
	switch cmd {
	case "gh", "gH", "gC-h", "C-g":
		return reject("select mode", "%s", cmd)
	case "gn", "gN":
		return reject("gn/gN", "%s", cmd)
	case "C-q":
		return reject("unsupported command", "%s", cmd)
	case "gd", "gD", "[i", "]i", "[I", "]I", "[d", "]d", "[D", "]D", "[C-i", "]C-i", "[C-d", "]C-d":
		return reject("search", "%s", cmd)
	case "Q", "gQ", "@:":
		return reject("ex command", "%s", cmd)
	case "q:", "q/", "q?":
		return reject("windows", "%s (command-line window)", cmd)
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

// State is what keys leave behind for the keys after them.
type State struct {
	Visual    string // "v", "V" or "C-v" in Visual mode; "?" when the kind is unknown (gv)
	Recording bool
}

type scanner struct {
	keys []vimtest.Key
	i    int
	st   State
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

func (s *scanner) count() bool {
	p := s.peek()
	if len(p) != 1 || p[0] < '1' || p[0] > '9' {
		return false
	}
	for p := s.peek(); len(p) == 1 && p[0] >= '0' && p[0] <= '9'; p = s.peek() {
		s.i++
	}
	return true
}

// Scan checks that keys, typed in state from, are editing our engine supports: Normal, Visual and
// Insert mode commands, searches, the ex commands in exCommands, and macros. endsInInsert is set when
// the keys end in Insert or Replace mode, which `:normal` and feedkeys(…, 'x') end as if <Esc> were typed.
func Scan(keys []vimtest.Key, from State) (to State, endsInInsert bool, err *Reject) {
	s := &scanner{keys: keys, st: from}
	for s.i < len(s.keys) {
		var insert bool
		var err *Reject
		if s.st.Visual != "" {
			insert, err = s.visual()
		} else {
			insert, err = s.command()
		}
		if err != nil {
			return s.st, false, err
		}
		if insert {
			done, err := s.insert()
			if err != nil {
				return s.st, false, err
			}
			if !done {
				return s.st, true, nil
			}
		}
	}
	return s.st, false, nil
}

func (s *scanner) register() *Reject {
	if s.peek() != `"` {
		return nil
	}
	s.i++
	r, err := s.next()
	if err != nil {
		return err
	}
	if len(r) != 1 || !strings.ContainsAny(r, `abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"-_`) {
		return reject("special register", `"%s`, r)
	}
	return nil
}

// command reads one normal-mode command and reports whether it starts Insert or Replace mode.
func (s *scanner) command() (bool, *Reject) {
	if s.st.Recording && s.peek() == "q" {
		s.i++
		s.st.Recording = false
		return false, nil
	}
	counted := s.count()
	if err := s.register(); err != nil {
		return false, err
	}
	counted = s.count() || counted
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
	case visualModes[c]:
		s.st.Visual = c
		if counted {
			s.st.Visual = "?"
		}
		return false, nil
	case c == "/" || c == "?":
		_, _, err := s.cmdline(c, "")
		return false, err
	case c == ":":
		return false, s.ex("")
	case c == "q":
		r, err := s.next()
		if err != nil {
			return false, err
		}
		if len(r) != 1 || !strings.ContainsAny(r, `abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"`) {
			return false, rejected("q" + r)
		}
		s.st.Recording = true
		return false, nil
	case c == "@":
		r, err := s.next()
		if err != nil {
			return false, err
		}
		if r == ":" {
			return false, rejected("@:")
		}
		if len(r) != 1 || !strings.ContainsAny(r, `abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"@`) {
			return false, reject("special register", "@%s", r)
		}
		return false, nil
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
		case g == "v":
			s.st.Visual = "?"
			return false, nil
		case g == "~" || g == "u" || g == "U" || g == "?":
			return false, s.motion("g" + g)
		}
		return false, rejected("g" + g)
	}
	return false, rejected(c)
}

// visual reads one Visual-mode command and reports whether it starts Insert mode.
func (s *scanner) visual() (bool, *Reject) {
	s.count()
	if err := s.register(); err != nil {
		return false, err
	}
	s.count()
	c, err := s.next()
	if err != nil {
		return false, err
	}
	switch {
	case motions[c]:
		return false, nil
	case argMotions[c]:
		_, err := s.next()
		return false, err
	case c == "o" || c == "O":
		return false, nil
	case c == "Esc" || c == "C-c":
		s.st.Visual = ""
		return false, nil
	case visualModes[c]:
		switch s.st.Visual {
		case c:
			s.st.Visual = ""
		case "?":
			return false, reject("visual/select mode", "%s after gv: Visual kind unknown", c)
		default:
			s.st.Visual = c
		}
		return false, nil
	case c == "i" || c == "a":
		o, err := s.next()
		if err != nil {
			return false, err
		}
		if !textObjs[o] {
			return false, reject("unsupported command", "v_%s%s", c, o)
		}
		return false, nil
	case c == "/" || c == "?":
		_, _, err := s.cmdline(c, "")
		return false, err
	case c == ":":
		s.st.Visual = ""
		return false, s.ex("'<,'>")
	case visualOps[c]:
		s.st.Visual = ""
		return false, nil
	case visualInserts[c]:
		s.st.Visual = ""
		return true, nil
	case c == "r":
		a, err := s.next()
		if err == nil && (a == "C-v" || a == "C-q" || a == "C-k") {
			return false, reject("unsupported command", "v_r%s", a)
		}
		s.st.Visual = ""
		return false, err
	case c == "[" || c == "]":
		b, err := s.next()
		if err != nil {
			return false, err
		}
		if !bracket[b] || b == "p" || b == "P" {
			return false, rejected(c + b)
		}
		return false, nil
	case c == "g":
		g, err := s.next()
		if err != nil {
			return false, err
		}
		switch {
		case g == "'" || g == "`":
			_, err := s.next()
			return false, err
		case gMotions[g]:
			return false, nil
		case g == "v":
			s.st.Visual = "?"
			return false, nil
		case g == "J" || g == "~" || g == "u" || g == "U" || g == "?":
			s.st.Visual = ""
			return false, nil
		}
		return false, rejected("g" + g)
	}
	if r := rejected(c); r.Category != "unsupported command" {
		return false, r
	}
	return false, reject("unsupported command", "v_%s", c)
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
		return reject("forced motion", "%s%s", op, m)
	case m == "/" || m == "?":
		_, _, err := s.cmdline(m, "")
		return err
	case m == ":":
		return reject("ex command", "%s: (an ex command as a motion)", op)
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

// cmdline reads a command line typed after kind (":", "/" or "?") up to <CR>. ran is false when <BS>
// left the line; opaque is set when <C-R> inserted a register, so text isn't what ran.
func (s *scanner) cmdline(kind, text string) (line string, ran bool, err *Reject) {
	opaque := false
	for {
		k, err := s.next()
		if err != nil {
			return "", false, err
		}
		key := s.keys[s.i-1]
		switch {
		case k == "CR" || k == "NL" || k == "C-m" || k == "C-j":
			if opaque && kind == ":" {
				return "", false, reject("ex command", "<C-R> in the command line")
			}
			return text, true, nil
		case k == "Space":
			text += " "
		case key.Name == "" && key.Mods == 0:
			text += string(key.Rune)
		case k == "BS" || k == "C-h":
			if text == "" {
				return "", false, nil
			}
			r := []rune(text)
			text = string(r[:len(r)-1])
		case k == "C-u":
			text = ""
		case k == "C-r":
			r, err := s.next()
			if err != nil {
				return "", false, err
			}
			if len(r) != 1 || !strings.ContainsAny(r, `abcdefghijklmnopqrstuvwxyz0123456789"-`) {
				return "", false, reject("special register", "c_<C-R>%s", r)
			}
			opaque = true
		default:
			return "", false, reject("command-line key", "%s%s<%s>", kind, text, k)
		}
	}
}

// ex reads a `:` command line and checks it's one our engine runs.
func (s *scanner) ex(prefill string) *Reject {
	text, ran, err := s.cmdline(":", prefill)
	if err != nil || !ran {
		return err
	}
	confirm, err := checkEx(text, false)
	if err != nil {
		return err
	}
	if confirm {
		// The answers to :s///c's prompt.
		for {
			k := s.peek()
			if k != "y" && k != "n" && k != "a" && k != "q" && k != "l" && k != "Esc" && k != "C-e" && k != "C-y" {
				break
			}
			s.i++
			if k == "a" || k == "q" || k == "l" || k == "Esc" {
				break
			}
		}
	}
	return nil
}

// exCommands are the ex commands our engine runs, by full name and shortest abbreviation.
var exCommands = []struct{ full, short string }{
	{"delete", "d"}, {"yank", "y"}, {"move", "m"}, {"t", "t"}, {"copy", "co"}, {"join", "j"}, {"put", "pu"},
	{"substitute", "s"}, {"global", "g"}, {"vglobal", "v"}, {"normal", "norm"}, {"set", "se"},
}

// exOptions are the options our engine's :set knows; numeric ones take =N.
var exOptions = map[string]bool{"sw": true, "shiftwidth": true, "report": true,
	"et": false, "expandtab": false, "ic": false, "ignorecase": false, "scs": false, "smartcase": false,
	"ws": false, "wrapscan": false}

var (
	silentRe  = regexp.MustCompile(`^sil(?:e|en|ent)?!?(?:\s+|$)`)
	regArgRe  = regexp.MustCompile(`^\s*([a-zA-Z0-9"_-])?\s*\d*\s*$`)
	countRe   = regexp.MustCompile(`^\s*\d*\s*$`)
	shiftRe   = regexp.MustCompile(`^[<>]*\s*\d*\s*$`)
	subFlagRe = regexp.MustCompile(`^&?[cegiInp#lr]*\s*\d*\s*$`)
)

// checkEx checks an ex command line. confirm is set when it asks before each substitution.
func checkEx(line string, inGlobal bool) (confirm bool, err *Reject) {
	t := strings.TrimLeft(line, " \t:")
	if loc := silentRe.FindStringIndex(t); loc != nil {
		t = t[loc[1]:]
	}
	if strings.ContainsAny(t, "\t") {
		return false, reject("ex command", "tab in %q", line)
	}
	rest, ok := skipRange(t)
	if !ok {
		return false, reject("ex command", "range in %q", line)
	}
	rest = strings.TrimLeft(rest, " ")
	if rest == "" {
		return false, nil
	}
	name, args := splitEx(rest)
	bang := strings.HasPrefix(args, "!")
	if bang {
		args = args[1:]
	}
	full := ""
	for _, c := range exCommands {
		if len(name) >= len(c.short) && strings.HasPrefix(c.full, name) {
			full = c.full
		}
	}
	if name == "&" || name == "&&" || name == "~" || name == "<" || name == ">" {
		full = name
	}
	if full == "" {
		return false, reject("ex command", ":%s", name)
	}
	if bang && full != "global" && full != "join" && full != "normal" && full != "put" {
		return false, reject("ex command", ":%s!", name)
	}
	bad := func() (bool, *Reject) { return false, reject("ex command", "arguments of %q", line) }
	switch full {
	case "delete", "yank":
		if !regArgRe.MatchString(args) {
			return bad()
		}
	case "put":
		if !regArgRe.MatchString(args) {
			return bad()
		}
	case "move", "t", "copy":
		r, ok := skipRange(strings.TrimSpace(args))
		if !ok || strings.TrimSpace(args) == "" || strings.TrimSpace(r) != "" {
			return bad()
		}
	case "join":
		if !countRe.MatchString(args) {
			return bad()
		}
	case "<", ">":
		if !shiftRe.MatchString(args) {
			return bad()
		}
	case "&", "&&", "~":
		if full == "&&" {
			args = "&" + args
		}
		if !subFlagRe.MatchString(args) {
			return bad()
		}
		return strings.Contains(args, "c"), nil
	case "substitute":
		return checkSub(line, args)
	case "global", "vglobal":
		if inGlobal {
			return false, reject("ex command", ":%s inside :global", name)
		}
		if args == "" || !isDelim(args[0]) {
			return bad()
		}
		_, cmd, _ := cutPattern(args[1:], args[0])
		if strings.TrimSpace(cmd) == "" {
			return false, nil
		}
		return checkEx(cmd, true)
	case "normal":
		keys := rawKeys(strings.TrimLeft(args, " "))
		if _, _, err := Scan(keys, State{}); err != nil {
			return false, err
		}
	case "set":
		for _, a := range strings.Fields(args) {
			name, val, hasVal := strings.Cut(a, "=")
			num, known := exOptions[name]
			if n, ok := exOptions[strings.TrimPrefix(name, "no")]; !known && !hasVal && ok && !n {
				known = true
			}
			if !known || num != hasVal || hasVal && (val == "" || !countRe.MatchString(val)) {
				return false, reject("ex command", ":set %s", a)
			}
		}
	}
	return false, nil
}

// checkSub checks :s's arguments: /pattern/replacement/flags count, or just flags and a count.
func checkSub(line, args string) (bool, *Reject) {
	if args == "" {
		return false, nil
	}
	if !isDelim(args[0]) {
		if !subFlagRe.MatchString(strings.TrimLeft(args, " ")) {
			return false, reject("ex command", "arguments of %q", line)
		}
		return strings.Contains(args, "c"), nil
	}
	d := args[0]
	pat, rest, found := cutPattern(args[1:], d)
	rep, flags, found2 := cutPattern(rest, d)
	if strings.Contains(unescaped(pat), "|") || strings.Contains(unescaped(rep), "|") {
		return false, reject("ex command", "| in %q", line)
	}
	if !found || !found2 {
		return false, nil
	}
	if !subFlagRe.MatchString(flags) {
		return false, reject("ex command", "flags of %q", line)
	}
	return strings.Contains(flags, "c"), nil
}

// isDelim reports whether c can delimit :s and :g patterns.
func isDelim(c byte) bool {
	return c < 0x80 && !strings.ContainsRune(`abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789\"| `, rune(c))
}

// unescaped drops backslash-escaped characters.
func unescaped(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// cutPattern splits s at the first d not escaped with a backslash.
func cutPattern(s string, d byte) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case d:
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// skipRange skips the addresses at the start of an ex command line.
func skipRange(s string) (string, bool) {
	for {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			return s, true
		}
		switch c := s[0]; {
		case c == '%' || c == '.' || c == '$' || c == ',' || c == ';' || c == '+' || c == '-' || c >= '0' && c <= '9':
			s = s[1:]
		case c == '\'':
			if len(s) < 2 || !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ<>'[]^.`", rune(s[1])) {
				return s, false
			}
			s = s[2:]
		case c == '/' || c == '?':
			_, s, _ = cutPattern(s[1:], c)
		case c == '\\':
			if len(s) < 2 || !strings.ContainsRune("/?&", rune(s[1])) {
				return s, false
			}
			s = s[2:]
		default:
			return s, true
		}
	}
}

// splitEx splits a command line after its range into the command name and its arguments.
func splitEx(s string) (string, string) {
	switch {
	case strings.HasPrefix(s, "&&"):
		return "&&", s[2:]
	case strings.ContainsRune("&~<>", rune(s[0])):
		return s[:1], s[1:]
	}
	i := 0
	for i < len(s) && (s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z') {
		i++
	}
	if i == 0 {
		return s[:1], s[1:]
	}
	return s[:i], s[i:]
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
