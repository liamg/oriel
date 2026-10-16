package gh

import (
	"context"
	"os/exec"
	"strings"
)

// Token is the user's GitHub token from their gh login, for private repos' releases and the plugin registries; ""
// without one.
func Token(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
