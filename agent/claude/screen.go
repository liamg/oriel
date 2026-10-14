package claude

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Claude Code's screen, as recorded from 2.1.296 (testdata/signed-out-*.pty), says "Not logged in · Run /login" at
// its foot while it has no login; an API error under a prompt says "… · Please run /login", which the log has too.
// Its /login dialog ends "Login successful. Press Enter to continue…" (from its source; not recorded).
var (
	signedOutLine = regexp.MustCompile(`\p{L}[\p{L} ]* · Run /login\b`)
	signedInLine  = regexp.MustCompile(`Login successful\. Press Enter to continue`)
)

// needsLogin is an API error that says the fix is a new login.
func needsLogin(text string) bool { return strings.Contains(text, "Please run /login") }

// screen reads Claude Code's terminal output for its login state.
type screen struct {
	carry []byte // the end of what was read last, so a line split between reads is still found
}

type screenSays struct {
	signedOut string // the signed-out line
	signedIn  bool
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
	if out.signedOut != "" || out.signedIn {
		s.carry = nil
		return out, true
	}
	const keep = 256
	if len(raw) > keep {
		raw = raw[len(raw)-keep:]
	}
	s.carry = append([]byte(nil), raw...)
	return out, false
}

// plain is terminal output as text: styles dropped, a cursor moved right as spaces, any other move or control as a
// line break, spaces collapsed.
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
			case 'C':
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
