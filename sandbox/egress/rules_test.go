package egress

import "testing"

func TestMatch(t *testing.T) {
	rules := []Rule{
		{Host: "*", Allow: false},
		{Host: "*.example.com", Allow: true},
		{Host: "*.api.example.com", Port: 443, Allow: false},
		{Host: "secret.example.com", Allow: false},
		{Host: "secret.example.com", Port: 8443, Allow: true},
		{Host: "exact.org", Port: 443, Allow: true},
		{Host: "dup.org", Allow: true},
		{Host: "dup.org", Allow: false},
	}
	tests := []struct {
		host  string
		port  int
		ok    bool
		want  Rule
		label string
	}{
		{"www.example.com", 443, true, rules[1], "wildcard"},
		{"a.b.example.com", 80, true, rules[1], "wildcard nested"},
		{"example.com", 443, true, rules[0], "wildcard excludes apex"},
		{"v1.api.example.com", 443, true, rules[2], "longer suffix wins"},
		{"v1.api.example.com", 80, true, rules[1], "longer suffix port mismatch"},
		{"secret.example.com", 443, true, rules[3], "exact over wildcard"},
		{"secret.example.com", 8443, true, rules[4], "specific port over any"},
		{"SECRET.Example.com.", 8443, true, rules[4], "normalised"},
		{"exact.org", 80, true, rules[0], "port mismatch falls through"},
		{"dup.org", 1, true, rules[6], "first of equals"},
	}
	for _, tt := range tests {
		got, ok := Match(rules, tt.host, tt.port)
		if ok != tt.ok || got != tt.want {
			t.Errorf("%s: Match(%s:%d) = %+v, %v; want %+v", tt.label, tt.host, tt.port, got, ok, tt.want)
		}
	}

	if _, ok := Match(rules[1:], "other.net", 443); ok {
		t.Error("expected no match")
	}
	if _, ok := Match(nil, "example.com", 443); ok {
		t.Error("expected no match on empty rules")
	}
}

func TestDefaultAllow(t *testing.T) {
	for _, tt := range []struct {
		host  string
		port  int
		allow bool
	}{
		// An agent's own endpoints are its provider's to allow.
		{"api.anthropic.com", 443, false},
		{"github.com", 443, true},
		// GitHub release downloads redirect here; a blocked one fails every
		// release asset a plugin or a tool installer fetches.
		{"release-assets.githubusercontent.com", 443, true},
		{"github.com", 80, false},
		{"sentry.io", 443, false},
	} {
		r, ok := Match(DefaultAllow, tt.host, tt.port)
		if (ok && r.Allow) != tt.allow {
			t.Errorf("%s:%d allowed = %v", tt.host, tt.port, ok && r.Allow)
		}
	}
}

func TestDecidePrecedence(t *testing.T) {
	builtin := []Rule{{Host: "github.com", Port: 443, Allow: true, Source: "builtin"}, {Host: "api.example.com", Port: 443, Allow: true, Source: "builtin"}}
	tests := []struct {
		label   string
		session []Rule
		project []Rule
		user    []Rule
		host    string
		port    int
		ok      bool
		allow   bool
		source  string
	}{
		{label: "built-in alone", host: "github.com", port: 443, ok: true, allow: true, source: "builtin"},
		{label: "nothing matches", host: "other.net", port: 443},
		{label: "user deny any port beats built-in port", user: []Rule{{Host: "github.com", Source: "user"}}, host: "github.com", port: 443, ok: true, source: "user"},
		{label: "user deny same rule beats built-in", user: []Rule{{Host: "github.com", Port: 443, Source: "user"}}, host: "github.com", port: 443, ok: true, source: "user"},
		{label: "user deny wildcard loses to a more specific built-in", user: []Rule{{Host: "*.example.com", Source: "user"}}, host: "api.example.com", port: 443, ok: true, allow: true, source: "builtin"},
		{label: "user deny * loses to a built-in name", user: []Rule{{Host: "*", Source: "user"}}, host: "github.com", port: 443, ok: true, allow: true, source: "builtin"},
		{label: "user deny * denies the rest", user: []Rule{{Host: "*", Source: "user"}}, host: "other.net", port: 443, ok: true, source: "user"},
		{label: "project deny beats user allow", project: []Rule{{Host: "x.org", Source: "project"}}, user: []Rule{{Host: "x.org", Port: 443, Allow: true, Source: "user"}}, host: "x.org", port: 443, ok: true, source: "project"},
		{label: "settings deny beats session allow", session: []Rule{{Host: "*", Allow: true, Source: "session"}}, user: []Rule{{Host: "x.org", Source: "user"}}, host: "x.org", port: 443, ok: true, source: "user"},
		{label: "session deny beats built-in", session: []Rule{{Host: "github.com", Port: 443, Source: "session"}}, host: "github.com", port: 443, ok: true, source: "session"},
		{label: "session allow beats settings allow", session: []Rule{{Host: "x.org", Allow: true, Private: true, Source: "session"}}, user: []Rule{{Host: "x.org", Allow: true, Source: "user"}}, host: "x.org", port: 1, ok: true, allow: true, source: "session"},
		{label: "settings allow beats built-in", user: []Rule{{Host: "*.example.com", Allow: true, Source: "user"}}, host: "api.example.com", port: 443, ok: true, allow: true, source: "user"},
	}
	for _, tt := range tests {
		r, ok := Decide(Levels{Session: tt.session, Settings: [][]Rule{tt.project, tt.user}, Builtin: builtin}, tt.host, tt.port)
		if ok != tt.ok || r.Allow != tt.allow || r.Source != tt.source {
			t.Errorf("%s: got %+v, %v", tt.label, r, ok)
		}
	}
}

func TestWildcardsSkipTheHostGateway(t *testing.T) {
	rules := []Rule{{Host: "*", Allow: true}, {Host: "*.internal", Allow: true}, {Host: "*.docker.internal", Allow: true}}
	if r, ok := Match(rules, HostGateway, 443); ok {
		t.Fatalf("a wildcard matched the host gateway: %+v", r)
	}
	if _, ok := Match(append(rules, Rule{Host: HostGateway, Port: 8080, Allow: true}), HostGateway, 8080); !ok {
		t.Fatal("an exact rule should match the host gateway")
	}
}

func TestEnvRules(t *testing.T) {
	got := EnvRules(map[string]string{
		"A": "https://api.example.com",
		"B": "http://Proxy.lan",
		"C": "http://host.docker.internal:4000/v1",
		"D": "not a url",
		"E": "grpc://otel:4317",
	})
	want := map[string]int{"api.example.com": 443, "proxy.lan": 80, HostGateway: 4000}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for _, r := range got {
		if want[r.Host] != r.Port || !r.Allow || !r.Private {
			t.Errorf("unexpected %+v", r)
		}
	}
}
