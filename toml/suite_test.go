package toml

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const suite = "../spec/toml-test/tests"

func suiteFiles(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(suite, "files-toml-1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if name := strings.TrimSpace(sc.Text()); strings.HasSuffix(name, ".toml") {
			out = append(out, name)
		}
	}
	if len(out) < 500 {
		t.Fatalf("only %d tests listed", len(out))
	}
	return out
}

// TestSuite runs toml-test: every invalid document must fail to parse, and every valid one must decode to its JSON.
func TestSuite(t *testing.T) {
	for _, name := range suiteFiles(t) {
		t.Run(strings.TrimSuffix(name, ".toml"), func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(suite, name))
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Parse(src)
			if strings.HasPrefix(name, "invalid/") {
				if err == nil {
					t.Fatalf("parsed an invalid document:\n%s", src)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v\n%s", err, src)
			}
			wantJSON, err := os.ReadFile(filepath.Join(suite, strings.TrimSuffix(name, ".toml")+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var want any
			if err := json.Unmarshal(wantJSON, &want); err != nil {
				t.Fatal(err)
			}
			if err := compare("", want, tagged(doc.Value())); err != nil {
				t.Errorf("%v\n%s", err, src)
			}
		})
	}
}

// tagged writes a decoded value in toml-test's JSON encoding: tables as objects, arrays as arrays,
// and everything else as {"type": …, "value": …}.
func tagged(v any) any {
	switch v := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, x := range v {
			m[k] = tagged(x)
		}
		return m
	case []any:
		a := make([]any, len(v))
		for i, x := range v {
			a[i] = tagged(x)
		}
		return a
	case string:
		return tag("string", v)
	case int64:
		return tag("integer", strconv.FormatInt(v, 10))
	case float64:
		switch {
		case math.IsNaN(v):
			return tag("float", "nan")
		case math.IsInf(v, 1):
			return tag("float", "inf")
		case math.IsInf(v, -1):
			return tag("float", "-inf")
		}
		return tag("float", strconv.FormatFloat(v, 'g', -1, 64))
	case bool:
		return tag("bool", strconv.FormatBool(v))
	case time.Time:
		return tag("datetime", v.Format(time.RFC3339Nano))
	case LocalDateTime:
		return tag("datetime-local", v.String())
	case LocalDate:
		return tag("date-local", v.String())
	case LocalTime:
		return tag("time-local", v.String())
	}
	return tag(fmt.Sprintf("unknown %T", v), fmt.Sprint(v))
}

func tag(typ, val string) map[string]any { return map[string]any{"type": typ, "value": val} }

var layouts = map[string]string{
	"datetime":       time.RFC3339Nano,
	"datetime-local": "2006-01-02T15:04:05.999999999",
	"date-local":     "2006-01-02",
	"time-local":     "15:04:05.999999999",
}

// compare follows toml-test's rules: floats compare as numbers (any NaN equals any NaN), datetimes as instants,
// and everything else as strings.
func compare(key string, want, have any) error {
	switch w := want.(type) {
	case []any:
		h, ok := have.([]any)
		if !ok || len(h) != len(w) {
			return fmt.Errorf("%s: want %v, have %v", key, want, have)
		}
		for i := range w {
			if err := compare(fmt.Sprintf("%s[%d]", key, i), w[i], h[i]); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		h, ok := have.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want %v, have %v", key, want, have)
		}
		if typ, ok := w["type"].(string); ok && len(w) == 2 {
			if val, ok := w["value"].(string); ok {
				return compareValue(key, typ, val, h)
			}
		}
		if len(h) != len(w) {
			return fmt.Errorf("%s: want keys %v, have %v", key, keys(w), keys(h))
		}
		for k := range w {
			if err := compare(strings.TrimPrefix(key+"."+k, "."), w[k], h[k]); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%s: unexpected %T in the expected JSON", key, want)
}

func compareValue(key, typ, want string, have map[string]any) error {
	if have["type"] != typ {
		return fmt.Errorf("%s: want %s %q, have %v", key, typ, want, have)
	}
	got := have["value"].(string)
	switch typ {
	case "float":
		w, g := strings.ToLower(want), strings.ToLower(got)
		if strings.HasSuffix(w, "nan") || strings.HasSuffix(g, "nan") {
			if strings.TrimLeft(w, "+-") != strings.TrimLeft(g, "+-") {
				return fmt.Errorf("%s: want %s, have %s", key, want, got)
			}
			return nil
		}
		wf, err1 := strconv.ParseFloat(w, 64)
		gf, err2 := strconv.ParseFloat(g, 64)
		if err1 != nil || err2 != nil || wf != gf {
			return fmt.Errorf("%s: want %s, have %s", key, want, got)
		}
	case "datetime", "datetime-local", "date-local", "time-local":
		r := strings.NewReplacer(" ", "T", "t", "T", "z", "Z")
		wt, err1 := time.Parse(layouts[typ], r.Replace(want))
		gt, err2 := time.Parse(layouts[typ], r.Replace(got))
		if err1 != nil || err2 != nil || !wt.Equal(gt) {
			return fmt.Errorf("%s: want %s %s, have %s (%v, %v)", key, typ, want, got, err1, err2)
		}
	default:
		if want != got {
			return fmt.Errorf("%s: want %s %q, have %q", key, typ, want, got)
		}
	}
	return nil
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
