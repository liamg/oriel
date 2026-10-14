package sandbox

import (
	"reflect"
	"testing"
)

// settings.md, "sandbox.*": a network rule is "allow|deny host[:port] [private]", a gateway rule "allow|ask|deny tool".
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
