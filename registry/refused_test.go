package registry

import (
	"errors"
	"fmt"
	"testing"
)

type httpErr int

func (e httpErr) Error() string   { return fmt.Sprintf("host: no (HTTP %d)", int(e)) }
func (e httpErr) HTTPStatus() int { return int(e) }

// A code host refused the token when its error says HTTP 401: by its status, or, after it crossed a plugin's Lua as
// text, by the "HTTP 401" its message says. A 401 anywhere else in the message (a PR's number) isn't one.
func TestTokenRefused(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{httpErr(401), true},
		{fmt.Errorf("reading: %w", httpErr(401)), true},
		{httpErr(404), false},
		{errors.New("GitLab: the token was refused (HTTP 401)"), true},
		{errors.New("Gitea: HTTP 401 token is required"), true},
		{errors.New("PR #401 has conflicts"), false},
		{errors.New("GitHub: rate limited (HTTP 403) after 401 requests"), false},
		{nil, false},
	} {
		if got := TokenRefused(tt.err); got != tt.want {
			t.Errorf("TokenRefused(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
