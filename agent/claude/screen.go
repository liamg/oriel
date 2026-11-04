package claude

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Claude Code's screen, as recorded from 2.1.296 (testdata/signed-out-*.pty), says "Not logged in · Run /login" at
// its foot while it has no login; an API error under a prompt says "… · Please run /login", which the log has too.
// Its /login dialog ends "Login successful. Press Enter to continue…" (from its source; not recorded). A dialog
// that waits on the keyboard (2.1.318, testdata/external-imports.pty) ends with a line of its own, dialogFooter,
// under the question it asks.
const dialogFooter = "Enter to confirm · Esc to cancel"

var (
	signedOutLine = regexp.MustCompile(`\p{L}[\p{L} ]* · Run /login\b`)
	signedInLine  = regexp.MustCompile(`Login successful\. Press Enter to continue`)
)

// needsLogin is an API error that says the fix is a new login.
func needsLogin(text string) bool { return strings.Contains(text, "Please run /login") }

// screen reads Claude Code's terminal output for its login state.
type screen struct {
	carry    []byte // the end of what was read last, so a line split between reads is still found
	cut      bool   // carry starts part way through what was read
	question string // the last whole line ending "?", a dialog's title
}

type screenSays struct {
	signedOut string // the signed-out line
	signedIn  bool
	dialog    bool   // a dialog's footer
	title     string // the dialog's question
}

// read takes the next output, and says what it shows about the login, if anything.
func (s *screen) read(p []byte) (screenSays, bool) {
	raw := append(s.carry, p...)
	text := plain(raw)
	var out screenSays
	if m := signedOutLine.FindString(text); m != "" {
		out.signedOut = m
	}
	out.signedIn = signedInLine.MatchString(text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case i == 0 && s.cut, i == len(lines)-1: // may be part of a line
		case l == dialogFooter:
			out.dialog, out.title, s.question = true, s.question, ""
		case strings.HasSuffix(l, "?"):
			s.question = l
		}
	}
	if out.signedOut != "" || out.signedIn || out.dialog {
		s.carry, s.cut = nil, false
		return out, true
	}
	const keep = 1024
	s.cut = len(raw) > keep
	if s.cut {
		raw = raw[len(raw)-keep:]
	}
	s.carry = append([]byte(nil), raw...)
	return out, false
}

// plain is terminal output as text: styles dropped, a cursor moved along its line as spaces, any other move or
// control as a line break, spaces collapsed.
func plain(b []byte) string {
	var sb strings.Builder
	space := func() {
		if sb.Len() > 0 && !strings.HasSuffix(sb.String(), " ") && !strings.HasSuffix(sb.String(), "\n") {
			sb.WriteByte(' ')
		}
	}
	s := string(b)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				return sb.String()
			}
			switch s[j] {
			case 'm':
			case 'C', 'G':
				space()
			default:
				sb.WriteByte('\n')
			}
			i = j + 1
		case c == 0x1b && i+1 < len(s) && s[i+1] == ']':
			j := strings.IndexAny(s[i+2:], "\x07\x1b")
			if j < 0 {
				return sb.String()
			}
			i += 2 + j + 1
			if s[i-1] == 0x1b {
				i++ // ESC \
			}
		case c == 0x1b:
			i += 2
		case c == '\r' || c == '\n':
			sb.WriteByte('\n')
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if unicode.IsSpace(r) {
				space()
			} else {
				sb.WriteString(s[i : i+n])
			}
			i += n
		}
	}
	return sb.String()
}
