package sandbox

import (
	"reflect"
	"testing"
)

// docs/configuration.md, sandbox.*: a network rule is "allow|deny host[:port] [private]", a gateway rule
// "allow|ask|deny tool".
func TestRulesReadFromTheirStringForm(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want EgressRule
	}{
		{"allow api.example.com", EgressRule{Host: "api.example.com", Allow: true}},
		{"allow  *.golang.org:443", EgressRule{Host: "*.golang.org", Port: 443, Allow: true}},
		{"deny ads.example.com", EgressRule{Host: "ads.example.com"}},
		{"allow db.internal:5432 private", EgressRule{Host: "db.internal", Port: 5432, Allow: true, Private: true}},
	} {
		got, err := ParseEgressRule(tt.in)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseEgressRule(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
		if back, _ := ParseEgressRule(got.String()); !reflect.DeepEqual(back, got) {
			t.Errorf("%+v doesn't round-trip through %q", got, got.String())
		}
	}
	for _, bad := range []string{"", "allow", "maybe example.com", "allow example.com:http", "allow a b c", "deny db:1 private", "allow example.com public"} {
		if _, err := ParseEgressRule(bad); err == nil {
			t.Errorf("ParseEgressRule(%q) took it", bad)
		}
	}
	for _, tt := range []struct {
		in   string
		want PolicyRule
	}{
		{"allow github__get_*", PolicyRule{Tool: "github__get_*", Action: "allow"}},
		{"ask oriel__push", PolicyRule{Tool: "oriel__push", Action: "ask"}},
		{"deny *", PolicyRule{Tool: "*", Action: "deny"}},
	} {
		got, err := ParsePolicyRule(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParsePolicyRule(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
		if got.String() != tt.in {
			t.Errorf("%+v is %q, not %q", got, got.String(), tt.in)
		}
	}
	for _, bad := range []string{"", "allow", "always x", "allow a b"} {
		if _, err := ParsePolicyRule(bad); err == nil {
			t.Errorf("ParsePolicyRule(%q) took it", bad)
		}
	}
}

// sandbox.servers: a server is an https URL (the HTTP transport) or a command line (stdio), read into
// Claude's mcpServers form.
func TestServersReadFromTheirStringForm(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.githubcopilot.com/mcp/":           `{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`,
		"npx -y @foo/mcp --flag":                       `{"command":"npx","args":["-y","@foo/mcp","--flag"]}`,
		`LINEAR_KEY=abc uvx "linear mcp" --name 'a b'`: `{"command":"uvx","args":["linear mcp","--name","a b"],"env":{"LINEAR_KEY":"abc"}}`,
		"/usr/local/bin/srv":                           `{"command":"/usr/local/bin/srv"}`,
	} {
		got, err := ParseServer(in)
		if err != nil || string(got) != want {
			t.Errorf("ParseServer(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", `npx "unclosed`, "FOO=bar", "ftp://example.com"} {
		if _, err := ParseServer(bad); err == nil {
			t.Errorf("ParseServer(%q) took it", bad)
		}
	}
}

// A project's sandbox settings, from their string forms; rules that don't parse are left out.
func TestProjectSettings(t *testing.T) {
	p, ok := NewProjectConfig([]string{"allow a.example.com:443", "bad"}, []string{"deny oriel__push"}, "img:dev")
	want := ProjectConfig{Egress: []EgressRule{{Host: "a.example.com", Port: 443, Allow: true}}, Rules: []PolicyRule{{Tool: "oriel__push", Action: "deny"}},
		Image: "img:dev"}
	if !ok || !reflect.DeepEqual(p, want) {
		t.Errorf("NewProjectConfig = %+v, %v", p, ok)
	}
	if _, ok := NewProjectConfig(nil, nil, ""); ok {
		t.Error("an empty project's settings are something")
	}
}
