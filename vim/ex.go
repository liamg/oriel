package vim

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/registry/keymap"
)

// ex runs one ex command line.
func (e *Engine) ex(v *view, line string) {
	e.group = e.newGroup()
	if err := e.exec1(v, line); err != nil {
		e.fail(err)
	}
	e.group = 0
}

func (e *Engine) exec1(v *view, line string) error {
	line = strings.TrimLeft(line, " \t:")
	if line == "" {
		return nil
	}
	for _, mod := range []string{"silent!", "silent", "sil!", "sil"} {
		if rest, ok := strings.CutPrefix(line, mod); ok && (rest == "" || rest[0] == ' ') {
			e.silent = true
			defer func() { e.silent = false }()
			line = strings.TrimLeft(rest, " ")
			break
		}
	}
	e.exLine = line
	r, rest, err := e.parseRange(v, line)
	if err != nil {
		return err
	}
	rest = strings.TrimLeft(rest, " \t")
	name, args := splitCommand(rest)
	if name == "" {
		if r.given == 0 {
			return nil
		}
		r.to = min(max(r.to, 0), v.lines()-1)
		e.cur = Pos{r.to, 0}
		e.cur.Col = v.colAt(r.to, e.want, false)
		if e.cur.Col == 0 {
			e.cur.Col = 0
		}
		e.setWant(v)
		return nil
	}
	if countCommands[name] {
		return e.runCommand(v, r, name, args)
	}
	if r.from == -1 && name != "pu" && name != "put" {
		r.from = 0
		r.to = max(r.to, 0)
	}
	if r.from > r.to {
		if e.silent {
			r.from, r.to = r.to, r.from
		} else {
			r.from, r.to = r.to, r.from
			e.confirm = &confirmation{yes: func() {
				e.group = e.newGroup()
				if err := e.runCommand(e.view(), r, name, args); err != nil {
					e.fail(err)
				}
				e.group = 0
			}}
			e.notify(askSwapRange.with())
			return nil
		}
	}
	if r.to >= v.lines() || r.from < -1 {
		return errInvalidRange.with()
	}
	// A range starts and ends on whole closed folds.
	if len(e.gstack) == 0 && r.from >= 0 && (r.given >= 2 || !strings.HasPrefix(name, "fold")) {
		r.from, _, _ = v.folded(r.from)
		_, r.to, _ = v.folded(r.to)
	}
	return e.runCommand(v, r, name, args)
}

// runCommand runs a parsed ex command over its range.
func (e *Engine) runCommand(v *view, r exRange, name, args string) error {
	bang := false
	if strings.HasPrefix(args, "!") && name != "s" && name != "&" {
		bang = true
		args = args[1:]
	}
	if ok, err := e.fileCmd(name, args, bang); ok {
		return err
	}
	if hostCommands[name] && e.host.Command(name, strings.TrimSpace(args), bang) {
		return nil
	}
	switch name {
	case "d", "delete":
		return e.exDelete(v, r, args)
	case "y", "yank":
		return e.exYank(v, r, args)
	case "m", "move":
		return e.exMove(v, r, args)
	case "t", "co", "copy":
		return e.exCopy(v, r, args)
	case "j", "join":
		return e.exJoin(v, r, args, bang)
	case ">", "<":
		return e.exShift(v, r, name, args)
	case "norm", "normal":
		return e.exNormal(v, r, args, bang)
	case "pu", "put":
		return e.exPut(v, r, args, bang)
	case "s", "substitute":
		return e.exSubstitute(v, r, args, false)
	case "&":
		return e.exSubstitute(v, r, args, true)
	case "&&":
		return e.exSubstitute(v, r, "&"+args, true)
	case "~":
		return e.exTilde(v, r, args)
	case "g", "global", "v", "vglobal":
		return e.exGlobal(v, r, args, bang || name[0] == 'v', r.given == 0)
	case "set", "se":
		return e.exSet(args)
	case "@", "*":
		return e.exAt(v, r, args)
	case "map", "noremap", "unmap", "mapclear", "nmap", "nnoremap", "nunmap", "nmapclear", "vmap", "vnoremap",
		"vunmap", "vmapclear", "xmap", "xnoremap", "xunmap", "xmapclear", "omap", "onoremap", "ounmap", "omapclear",
		"imap", "inoremap", "iunmap", "imapclear", "cmap", "cnoremap", "cunmap", "cmapclear":
		return e.exMap(name, args, bang)
	case "abbreviate", "noreabbrev", "unabbreviate", "abclear", "iabbrev", "inoreabbrev", "iunabbrev",
		"iabclear", "cabbrev", "cnoreabbrev", "cunabbrev", "cabclear":
		return e.exAbbrev(name, args)
	case "fold", "foldopen", "foldclose":
		return e.exFold(name, r, bang)
	case "cexpr", "cgetexpr", "caddexpr", "cbuffer", "cgetbuffer", "caddbuffer", "cnext", "cprevious", "cNext",
		"cc", "cfirst", "crewind", "clast", "cabove", "cbelow", "cbefore", "cafter", "cnfile", "cpfile", "cNfile",
		"colder", "cnewer", "chistory", "copen", "cwindow", "cclose", "cdo", "vimgrep",
		"lexpr", "lgetexpr", "laddexpr", "lbuffer", "lgetbuffer", "laddbuffer", "lnext", "lprevious", "lNext",
		"ll", "lfirst", "lrewind", "llast", "labove", "lbelow", "lbefore", "lafter", "lnfile", "lpfile", "lNfile",
		"lolder", "lnewer", "lhistory", "lopen", "lwindow", "lclose", "ldo", "lvimgrep":
		return e.exQuickfix(name, args, r, bang, e.exLine)
	case "execute":
		cmd, err := evalStrings(args)
		if err != nil {
			return err
		}
		return e.exec1(v, cmd)
	case "sp", "split", "vs", "vsplit":
		e.split(name[0] == 'v', nil)
		return nil
	case "new", "vne", "vnew":
		e.newBufferWindow(name[0] == 'v')
		return nil
	case "close", "quit":
		w := e.window
		if r.given > 0 {
			order := e.order()
			w = order[min(max(r.to, 0), len(order)-1)]
		}
		if name == "quit" && r.given == 0 {
			return e.quit(bang)
		}
		if name == "quit" && len(e.order()) == 1 {
			return nil
		}
		e.closeWindow(w)
		return nil
	case "only":
		e.only()
		return nil
	case "wincmd":
		count := 0
		if r.given > 0 {
			count = r.to + 1
		}
		keys := keymap.Parse(strings.TrimSpace(args))
		if len(keys) == 0 {
			return errArgRequired.with()
		}
		e.windowCmd(keys[0], count)
		return nil
	case "enew", "bnext", "bprevious", "bNext", "bfirst", "brewind", "blast", "buffer", "bdelete", "bwipeout",
		"bunload", "ls", "buffers", "files", "file", "badd", "balt", "bmodified", "edit", "sbuffer", "sbnext",
		"sbprevious", "sbNext", "sbfirst", "sbrewind", "sblast", "sbmodified":
		return e.exBuffer(name, args, r, bang)
	case "p", "print", "#", "nu", "number", "l", "list":
		e.cur = Pos{r.to, v.firstNonBlank(r.to)}
		return nil
	}
	if e.host.Command(name, strings.TrimSpace(args), bang) {
		return nil
	}
	return errUnknownCommand.with(strings.TrimSpace(name + args))
}

// hostCommands are the ex commands the IDE shows in its own views when it has them: the buffer switcher, the
// results panel and the notification history.
var hostCommands = map[string]bool{"ls": true, "buffers": true, "files": true, "copen": true, "cwindow": true,
	"lopen": true, "lwindow": true, "messages": true}

// splitCommand splits a command line after its range into the command name and its arguments.
func splitCommand(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	switch c := s[0]; {
	case c == '&' && strings.HasPrefix(s, "&&"):
		return "&&", s[2:]
	case c == '@' || c == '*':
		return "@", s[1:]
	case c == '&' || c == '~' || c == '#':
		return string(c), s[1:]
	case c == '>' || c == '<':
		i := 0
		for i < len(s) && s[i] == c {
			i++
		}
		return string(c), strings.Repeat(string(c), i-1) + s[i:]
	}
	i := 0
	for i < len(s) && unicode.IsLetter(rune(s[i])) {
		i++
	}
	name := s[:i]
	if full, ok := exName(name); ok {
		return full, s[i:]
	}
	// :s, :g and :v take any delimiter right after the name, letters included.
	for _, short := range []string{"s", "g", "v"} {
		if strings.HasPrefix(name, short) {
			full, _ := exName(short)
			return full, s[1:]
		}
	}
	return name, s[i:]
}

// countCommands take a count rather than lines in their range: a buffer or window number, or how many.
var countCommands = map[string]bool{
	"bnext": true, "bprevious": true, "bNext": true, "buffer": true, "bdelete": true, "bwipeout": true,
	"bunload": true, "sbuffer": true, "sbnext": true, "sbprevious": true, "sbNext": true, "close": true,
	"quit": true, "only": true, "wincmd": true, "split": true, "vsplit": true, "new": true, "vnew": true,
	"cfirst": true, "crewind": true, "clast": true, "chistory": true, "lhistory": true, "cabove": true,
	"cbelow": true, "cbefore": true, "cafter": true, "labove": true, "lbelow": true, "lbefore": true,
	"lafter": true, "cnfile": true, "cpfile": true, "cNfile": true, "lnfile": true, "lpfile": true,
	"lNfile": true,
	"cc":     true, "cnext": true, "cprevious": true, "cNext": true,
	"ll": true, "lnext": true, "lprevious": true, "lNext": true, "lfirst": true, "llast": true, "lrewind": true,
	"colder": true, "cnewer": true, "lolder": true, "lnewer": true,
}

// exNames are the ex commands with the shortest abbreviation of each, as vim's :help lists them.
var exNames = []struct {
	full string
	min  int
}{
	{"append", 1}, {"buffer", 1}, {"bNext", 2}, {"bdelete", 2}, {"bfirst", 2}, {"blast", 2}, {"bnext", 2},
	{"bprevious", 2}, {"brewind", 2}, {"buffers", 7}, {"bunload", 3}, {"bwipeout", 2}, {"cabbrev", 2},
	{"close", 3}, {"cnoreabbrev", 6}, {"cunabbrev", 4}, {"copy", 2},
	{"delete", 1}, {"enew", 3}, {"execute", 3}, {"edit", 1}, {"badd", 3}, {"balt", 4}, {"bmodified", 2}, {"sbuffer", 2}, {"sbnext", 3}, {"sbprevious", 3}, {"sbNext", 3}, {"sbfirst", 3}, {"sbrewind", 3}, {"sblast", 3}, {"sbmodified", 3}, {"file", 1}, {"files", 5}, {"fold", 2}, {"foldclose", 5}, {"folddoopen", 5},
	{"folddoclosed", 7}, {"foldopen", 3}, {"global", 1}, {"iabbrev", 2}, {"inoreabbrev", 6}, {"iunabbrev", 4},
	{"abbreviate", 2}, {"abclear", 3}, {"iabclear", 4}, {"cabclear", 4}, {"noreabbrev", 5}, {"unabbreviate", 3},
	{"imap", 2}, {"inoremap", 3}, {"cmap", 2}, {"cnoremap", 3}, {"map", 3}, {"noremap", 2}, {"nmap", 2},
	{"nnoremap", 2}, {"vmap", 2}, {"vnoremap", 2}, {"xmap", 2}, {"xnoremap", 2}, {"omap", 2}, {"onoremap", 3},
	{"unmap", 3}, {"nunmap", 3}, {"vunmap", 2}, {"xunmap", 2}, {"ounmap", 2}, {"iunmap", 2}, {"cunmap", 2},
	{"mapclear", 4}, {"nmapclear", 5}, {"vmapclear", 5}, {"xmapclear", 5}, {"omapclear", 5}, {"imapclear", 5},
	{"cmapclear", 5}, {"join", 1}, {"list", 1}, {"ls", 2}, {"move", 1}, {"new", 3}, {"normal", 4}, {"number", 2}, {"only", 2},
	{"print", 1}, {"put", 2}, {"quit", 1}, {"set", 2}, {"split", 2}, {"substitute", 1}, {"vglobal", 1},
	{"vnew", 3}, {"vsplit", 2}, {"wincmd", 4}, {"yank", 1},
	{"cexpr", 3}, {"cgetexpr", 5}, {"caddexpr", 3}, {"cbuffer", 2}, {"cgetbuffer", 5}, {"caddbuffer", 5},
	{"cnext", 2}, {"cNext", 2}, {"cprevious", 2}, {"cc", 2}, {"cfirst", 4}, {"crewind", 2}, {"clast", 3},
	{"cabove", 4}, {"cbelow", 4}, {"cbefore", 3}, {"cafter", 3}, {"cnfile", 3}, {"cpfile", 3}, {"cNfile", 3},
	{"colder", 3}, {"cnewer", 4}, {"chistory", 3}, {"copen", 4}, {"cwindow", 2}, {"cclose", 3}, {"cdo", 3},
	{"vimgrep", 3}, {"lexpr", 3}, {"lgetexpr", 5}, {"laddexpr", 3}, {"lbuffer", 2}, {"lgetbuffer", 5},
	{"laddbuffer", 5}, {"lnext", 3}, {"lNext", 2}, {"lprevious", 2}, {"ll", 2}, {"lfirst", 4}, {"lrewind", 2},
	{"llast", 3}, {"labove", 3}, {"lbelow", 3}, {"lbefore", 4}, {"lafter", 3}, {"lnfile", 3}, {"lpfile", 3},
	{"lNfile", 3}, {"lolder", 3}, {"lnewer", 4}, {"lhistory", 3}, {"lopen", 3}, {"lwindow", 2}, {"lclose", 3},
	{"ldo", 3}, {"lvimgrep", 2},
	{"write", 1}, {"wq", 2}, {"wall", 2}, {"wqall", 3}, {"xit", 1}, {"xall", 2}, {"exit", 3}, {"saveas", 3},
	{"update", 2}, {"qall", 2}, {"quitall", 5}, {"messages", 3},
}

// exName finds the command an abbreviated name stands for.
func exName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for _, c := range exNames {
		if len(name) >= c.min && strings.HasPrefix(c.full, name) {
			return c.full, true
		}
	}
	return "", false
}

// regAndCount reads an optional register name and count after a command, as :d and :y take them.
func regAndCount(args string) (rune, int, error) {
	args = strings.TrimSpace(args)
	var reg rune
	if args != "" && !unicode.IsDigit(rune(args[0])) {
		reg = rune(args[0])
		args = strings.TrimSpace(args[1:])
	}
	if args == "" {
		return reg, 0, nil
	}
	n, err := strconv.Atoi(args)
	if err != nil || n <= 0 {
		return 0, 0, errTrailing.with(args)
	}
	return reg, n, nil
}

// withCount makes a range of count lines from the range's last line, as ":d 3" does.
func withCount(v *view, r exRange, count int) exRange {
	if count > 0 {
		r.from = r.to
		r.to = min(r.from+count-1, v.lines()-1)
	}
	return r
}

// parseKeys splits typed text into keys: :normal takes characters literally.

// confirmation is a yes/no question waiting for the next key.
type confirmation struct {
	yes func()
}

// exAt is :@{reg}: run a register's lines as Ex commands, at the range's last line.
func (e *Engine) exAt(v *view, r exRange, args string) error {
	reg := '@'
	if args = strings.TrimSpace(args); args != "" {
		reg, _ = utf8.DecodeRuneInString(args)
	}
	if reg == '@' {
		if e.lastExReg == 0 {
			return errNoPrevRegister.with()
		}
		reg = e.lastExReg
	}
	if r.given > 0 {
		e.cur = Pos{r.to, 0}
	}
	if reg == ':' {
		e.lastExReg = ':'
		return e.exec1(v, e.lastCmdline)
	}
	val, ok := e.ed.Register(unicode.ToLower(reg))
	if !ok {
		return errNoPrevRegister.with()
	}
	e.lastExReg = reg
	for _, line := range strings.Split(strings.TrimSuffix(val.Text, "\n"), "\n") {
		if err := e.exec1(e.view(), line); err != nil {
			return err
		}
	}
	return nil
}

// evalStrings evaluates :execute's arguments: string literals, joined with spaces.
func evalStrings(s string) (string, error) {
	var parts []string
	s = strings.TrimSpace(s)
	for s != "" {
		var b strings.Builder
		switch s[0] {
		case '"':
			i := 1
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] != '\\' || i+1 >= len(s) {
					b.WriteByte(s[i])
					continue
				}
				i++
				switch c := s[i]; c {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case 'e':
					b.WriteByte(0x1b)
				case '<':
					end := strings.IndexByte(s[i:], '>')
					if end < 0 {
						b.WriteByte(c)
						continue
					}
					b.WriteString(keysToRaw(keymap.Parse(s[i : i+end+1])))
					i += end
				default:
					b.WriteByte(c)
				}
			}
			if i >= len(s) {
				return "", errMissingDouble.with(s)
			}
			s = s[i+1:]
		case '\'':
			i := 1
			for ; i < len(s); i++ {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i++
					} else {
						break
					}
				}
				b.WriteByte(s[i])
			}
			if i >= len(s) {
				return "", errMissingSingle.with(s)
			}
			s = s[i+1:]
		default:
			return "", errUndefinedVar.with(s)
		}
		parts = append(parts, b.String())
		s = strings.TrimLeft(s, " \t")
		if strings.HasPrefix(s, "..") || strings.HasPrefix(s, ".") {
			s = strings.TrimLeft(strings.TrimLeft(s, "."), " \t")
			parts[len(parts)-1] += "\x00"
		}
	}
	return strings.ReplaceAll(strings.Join(parts, " "), "\x00 ", ""), nil
}
