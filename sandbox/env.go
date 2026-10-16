package sandbox

import (
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/liamg/oriel/sandbox/egress"
)

// DefaultEnv is the host environment a sandbox inherits when settings name
// none: the model endpoint and telemetry, never credentials for anything else.
var DefaultEnv = []string{
	"ANTHROPIC_*",
	"OTEL_*",
	"DISABLE_*",
	"CLAUDE_CODE_USE_*",
	"CLAUDE_CODE_ENABLE_TELEMETRY",
}

// HostGateway is how the container reaches the host.
const HostGateway = egress.HostGateway

// endpointVars hold URLs whose localhost means the host, not the container.
var endpointVars = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_BEDROCK_BASE_URL",
	"ANTHROPIC_VERTEX_BASE_URL",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
}

// FilterEnv keeps the entries of environ whose names match a pattern ("FOO"
// or a "FOO_*" glob), with localhost endpoints pointed at the host.
func FilterEnv(environ, patterns []string) map[string]string {
	out := HostEnv(environ, patterns)
	for _, k := range endpointVars {
		if v, ok := out[k]; ok {
			out[k] = RewriteLocalhost(v)
		}
	}
	return out
}

// HostEnv keeps the entries of environ whose names match a pattern, as they
// are: for an agent that shares the host's network.
func HostEnv(environ, patterns []string) map[string]string {
	if patterns == nil {
		patterns = DefaultEnv
	}
	out := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !matchAny(patterns, k) {
			continue
		}
		out[k] = v
	}
	return out
}

// EndpointEnv is what the sandbox's endpoints are read from: environ filtered
// by patterns, then the endpoints in the env block of Claude's settings, which
// the agent sees over its process environment.
func EndpointEnv(environ, patterns []string, settings map[string]any) map[string]string {
	out := FilterEnv(environ, patterns)
	env, _ := settings["env"].(map[string]any)
	for _, k := range endpointVars {
		if v, ok := env[k].(string); ok {
			out[k] = RewriteLocalhost(v)
		}
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
	sort.Strings(keys)
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
