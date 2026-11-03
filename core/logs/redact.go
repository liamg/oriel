// Package logs is where Oriel's and each session's log records go: slog handlers for an in-memory ring and a
// rotating file, per-session logs that outlive a restart, and redaction before anything is written.
package logs

import (
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Mask replaces whatever is redacted.
const Mask = "[redacted]"

// Redactor strips secrets from text: every value it has been given (the secrets store adds each one it hands
// out), and tokens in URLs, both a URL's password and its token-like query parameters.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string // longest first, so a secret containing another goes whole
}

func NewRedactor() *Redactor { return &Redactor{} }

// Add redacts s from now on. Values shorter than 4 bytes are ignored: masking them would mangle ordinary text.
func (r *Redactor) Add(s string) {
	if len(s) < 4 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.Contains(r.secrets, s) {
		return
	}
	r.secrets = append(r.secrets, s)
	slices.SortFunc(r.secrets, func(a, b string) int { return len(b) - len(a) })
}

var (
	urlPassword = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]*:)[^/\s@]+@`)
	urlToken    = regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|token|api_key|apikey|key|secret|password|passwd|sig|signature|auth|code)=)[^&#\s]+`)
)

// Redact returns s with every secret masked. A nil Redactor still masks tokens in URLs.
func (r *Redactor) Redact(s string) string {
	if r != nil {
		r.mu.RLock()
		for _, sec := range r.secrets {
			s = strings.ReplaceAll(s, sec, Mask)
		}
		r.mu.RUnlock()
	}
	if strings.Contains(s, "://") {
		s = urlPassword.ReplaceAllString(s, "${1}"+Mask+"@")
	}
	if strings.ContainsAny(s, "?&") {
		s = urlToken.ReplaceAllString(s, "${1}"+Mask)
	}
	return s
}
