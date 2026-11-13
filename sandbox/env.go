package sandbox

import (
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox/egress"
)

// DefaultEnv is the host environment every sandbox inherits when settings name none, past what its agent's
// provider passes (agent.SandboxNeeds.PassEnv): telemetry, never credentials.
var DefaultEnv = []string{"OTEL_*"}

// HostGateway is how the container reaches the host.
const HostGateway = egress.HostGateway

// endpointVars hold URLs whose localhost means the host, not the container, past the provider's (URLEnv).
var endpointVars = []string{
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
}

// FilterEnv keeps the entries of environ whose names match a pattern ("FOO" or a "FOO_*" glob), with localhost
// endpoints pointed at the host. With no patterns, the defaults and what n passes.
func FilterEnv(environ, patterns []string, n agent.SandboxNeeds) map[string]string {
	out := HostEnv(environ, patterns, n)
	for _, k := range append(slices.Clone(endpointVars), n.URLEnv...) {
		if v, ok := out[k]; ok {
			out[k] = RewriteLocalhost(v)
		}
	}
	return out
}

// HostEnv keeps the entries of environ whose names match a pattern, as they are: for an agent that shares the
// host's network. With no patterns, the defaults and what n passes, less what n skips.
func HostEnv(environ, patterns []string, n agent.SandboxNeeds) map[string]string {
	defaults := patterns == nil
	if defaults {
		patterns = append(slices.Clone(DefaultEnv), n.PassEnv...)
	}
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !matchAny(patterns, k) || defaults && matchAny(n.SkipEnv, k) {
			continue
		}
		out[k] = v
	}
	return out
}

// EndpointEnv is what the sandbox's endpoints are read from: environ filtered by patterns, then the endpoints n
// names outside its environment, by their own keys.
func EndpointEnv(environ, patterns []string, n agent.SandboxNeeds) map[string]string {
	out := FilterEnv(environ, patterns, n)
	for i, u := range n.URLs {
		out[fmt.Sprintf("url:%d", i)] = RewriteLocalhost(u)
	}
	return out
}

func matchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(strings.TrimSpace(p), name); ok {
			return true
		}
	}
	return false
}

// RewriteLocalhost points a loopback URL at the host gateway.
func RewriteLocalhost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return raw
	}
	if p := u.Port(); p != "" {
		u.Host = HostGateway + ":" + p
	} else {
		u.Host = HostGateway
	}
	return u.String()
}

// EnvFile renders env as a docker --env-file, sorted for stable output.
// Values cannot hold newlines there, so any that do are dropped.
func EnvFile(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		v := env[k]
		if strings.ContainsAny(v, "\r\n") {
			continue
		}
		b.WriteString(k + "=" + v + "\n")
	}
	return b.String()
}
