package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/liamg/oriel/core"
)

// UsageAPI is where Claude Code's own /usage reads the plan's windows from, with the sign-in's OAuth token. It asks no
// model anything, so it bills nothing.
const UsageAPI = "https://api.anthropic.com/api/oauth/usage"

// usageWindows are the windows the API names, in the order they're shown.
var usageWindows = []string{"five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet"}

// apiUsage reads the plan's windows from url with the host's sign-in. ok is false when there's no sign-in to use, or
// its token has run out: the /usage probe then reads them, and claude refreshes the token as it starts.
func (p *Provider) apiUsage(ctx context.Context, client *http.Client, url string) (u core.ProviderUsage, ok bool, err error) {
	l, ok := p.box.HostLogin()
	if !ok || l.AccessToken() == "" || time.Until(l.Expiry()) < time.Minute {
		return core.ProviderUsage{}, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return core.ProviderUsage{}, true, err
	}
	req.Header.Set("Authorization", "Bearer "+l.AccessToken())
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := client.Do(req)
	if err != nil {
		return core.ProviderUsage{}, true, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return core.ProviderUsage{}, true, err
	}
	if resp.StatusCode != http.StatusOK {
		return core.ProviderUsage{}, true, fmt.Errorf("claude: the usage API answered %s", resp.Status)
	}
	u, err = parseAPIUsage(b)
	return u, true, err
}

// parseAPIUsage reads the usage API's answer: a window per kind, its utilization a percentage.
func parseAPIUsage(b []byte) (core.ProviderUsage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return core.ProviderUsage{}, fmt.Errorf("claude: the usage API's answer: %w", err)
	}
	var rls []rateLimit
	for _, kind := range usageWindows {
		var w *struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		}
		if json.Unmarshal(doc[kind], &w) != nil || w == nil || w.Utilization == nil {
			continue
		}
		rls = append(rls, rateLimit{Kind: kind, PercentUsed: *w.Utilization, ResetsAt: w.ResetsAt})
	}
	if len(rls) == 0 {
		return core.ProviderUsage{}, fmt.Errorf("claude: the usage API named none of %v", slices.Clone(usageWindows))
	}
	u := usageOf(rls)
	u.Source = "Anthropic's usage API, with Claude Code's sign-in"
	return u, nil
}
