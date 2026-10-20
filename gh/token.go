package gh

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// Token is the user's GitHub token, for private repos' releases and the plugin registries: GH_TOKEN or GITHUB_TOKEN,
// else their gh login; "" without one, and public repos are read without it.
func Token(ctx context.Context) string {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if tok := strings.TrimSpace(os.Getenv(name)); tok != "" {
			return tok
		}
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
