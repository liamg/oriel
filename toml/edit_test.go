package toml

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validDocs(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, name := range suiteFiles(t) {
		if strings.HasPrefix(name, "valid/") {
			src, err := os.ReadFile(filepath.Join(suite, name))
			if err != nil {
				t.Fatal(err)
			}
			out[name] = src
		}
	}
	return out
}

func TestBytesRoundTrip(t *testing.T) {
	for name, src := range validDocs(t) {
		doc, err := Parse(src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.Equal(doc.Bytes(), src) {
			t.Errorf("%s: Bytes() differs from the source", name)
		}
	}
}

func equalValue(a, b any) bool {
	if fa, ok := a.(float64); ok {
		if fb, ok := b.(float64); ok && math.IsNaN(fa) && math.IsNaN(fb) {
			return true
		}
	}
	if ta, ok := a.(time.Time); ok {
		if tb, ok := b.(time.Time); ok {
			return ta.Equal(tb)
		}
	}
	return reflect.DeepEqual(a, b)
}

// Setting an existing key changes only its value's bytes, and deleting one removes only its line.
func TestEditsTouchOnlyTheirKey(t *testing.T) {
	edits := 0
	for name, src := range validDocs(t) {
		doc, err := Parse(src)
		if err != nil {
			continue
		}
		for _, kv := range doc.keyvals {
			if kv.member || kv.aot {
				continue
			}
			edits++
			set, _ := Parse(src)
			if err := set.Set(kv.path, "edited"); err != nil {
				t.Errorf("%s: set %q: %v", name, kv.path, err)
				continue
			}
			want := string(src[:kv.valStart]) + `"edited"` + string(src[kv.valEnd:])
			if got := string(set.Bytes()); got != want {
				t.Errorf("%s: set %q:\n%s\nwant:\n%s", name, kv.path, got, want)
			}
			if v, ok := set.Get(kv.path...); !ok || v != "edited" {
				t.Errorf("%s: after set, %q = %v", name, kv.path, v)
			}

			del, _ := Parse(src)
			if err := del.Delete(kv.path...); err != nil {
				t.Errorf("%s: delete %q: %v", name, kv.path, err)
				continue
			}
			want = string(src[:kv.lineStart]) + string(src[kv.lineEnd:])
			if got := string(del.Bytes()); got != want {
				t.Errorf("%s: delete %q:\n%s\nwant:\n%s", name, kv.path, got, want)
			}
			if _, ok := del.Get(kv.path...); ok {
				t.Errorf("%s: %q is still there after delete", name, kv.path)
			}
		}
	}
	if edits < 300 {
		t.Errorf("only %d edits ran", edits)
	}
}

func TestEdit(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
		edit func(d *Document) error
		want string
	}{
		{
			name: "set keeps comments and layout",
			src:  "# settings\n[editor]\nline_numbers = \"absolute\"   # was relative\ntab_width = 4\n",
			edit: func(d *Document) error { return d.Set([]string{"editor", "line_numbers"}, "relative") },
			want: "# settings\n[editor]\nline_numbers = \"relative\"   # was relative\ntab_width = 4\n",
		},
		{
			name: "add to an existing table, after its last key",
			src:  "[editor]\n  tab_width = 4 # tabs\n\n[agent]\ndefault_provider = \"claude\"\n",
			edit: func(d *Document) error { return d.Set([]string{"editor", "wrap"}, true) },
			want: "[editor]\n  tab_width = 4 # tabs\n  wrap = true\n\n[agent]\ndefault_provider = \"claude\"\n",
		},
		{
			name: "add to an empty table",
			src:  "[editor]\n\n[agent]\n",
			edit: func(d *Document) error { return d.Set([]string{"agent", "commit_gate"}, "soft") },
			want: "[editor]\n\n[agent]\ncommit_gate = \"soft\"\n",
		},
		{
			name: "add a new table at the end",
			src:  "[editor]\ntab_width = 4",
			edit: func(d *Document) error { return d.Set([]string{"agent", "default_provider"}, "codex") },
			want: "[editor]\ntab_width = 4\n\n[agent]\ndefault_provider = \"codex\"\n",
		},
		{
			name: "add to an empty file",
			src:  "",
			edit: func(d *Document) error { return d.Set([]string{"editor", "tab_width"}, int64(2)) },
			want: "[editor]\ntab_width = 2\n",
		},
		{
			name: "add a root key before the first table",
			src:  "# top\n[editor]\ntab_width = 4\n",
			edit: func(d *Document) error { return d.Set([]string{"theme"}, "graphite") },
			want: "# top\ntheme = \"graphite\"\n\n[editor]\ntab_width = 4\n",
		},
		{
			name: "add next to dotted keys that define the table",
			src:  "editor.tab_width = 4\nname = \"x\"\n",
			edit: func(d *Document) error { return d.Set([]string{"editor", "wrap"}, false) },
			want: "editor.tab_width = 4\nname = \"x\"\neditor.wrap = false\n",
		},
		{
			name: "keys that need quotes",
			src:  "[component]\n",
			edit: func(d *Document) error { return d.Set([]string{"component", "tab.active.edge"}, "accent") },
			want: "[component]\n\"tab.active.edge\" = \"accent\"\n",
		},
		{
			name: "CRLF documents get CRLF",
			src:  "[a]\r\nb = 1\r\n",
			edit: func(d *Document) error { return d.Set([]string{"a", "c"}, int64(2)) },
			want: "[a]\r\nb = 1\r\nc = 2\r\n",
		},
		{
			name: "set inside an inline table",
			src:  "font = { family = \"Inter\", size = 13 }\n",
			edit: func(d *Document) error { return d.Set([]string{"font", "size"}, 14.5) },
			want: "font = { family = \"Inter\", size = 14.5 }\n",
		},
		{
			name: "delete from an inline table",
			src:  "font = { family = \"Inter\", size = 13, bold = true }\n",
			edit: func(d *Document) error { return d.Delete("font", "size") },
			want: "font = { family = \"Inter\", bold = true }\n",
		},
		{
			name: "delete the last member of an inline table",
			src:  "font = { family = \"Inter\", size = 13 }\n",
			edit: func(d *Document) error { return d.Delete("font", "size") },
			want: "font = { family = \"Inter\" }\n",
		},
		{
			name: "delete a table and its keys",
			src:  "[a]\nx = 1\n\n# about b\n[b]\ny = 2\n\n[c]\nz = 3\n",
			edit: func(d *Document) error { return d.Delete("b") },
			want: "[a]\nx = 1\n\n# about b\n[c]\nz = 3\n",
		},
		{
			name: "deleting what isn't there changes nothing",
			src:  "a = 1\n",
			edit: func(d *Document) error { return d.Delete("b") },
			want: "a = 1\n",
		},
		{
			name: "values of every kind",
			src:  "",
			edit: func(d *Document) error {
				for _, kv := range []struct {
					k string
					v any
				}{
					{"s", "tab\there \"q\" \u007f"}, {"i", -3}, {"f", 1.0}, {"inf", math.Inf(-1)}, {"b", true},
					{"t", time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)}, {"d", LocalDate{2026, 10, 12}},
					{"arr", []any{int64(1), "two", []string{"x"}}}, {"tbl", map[string]any{"b": 2, "a": "x"}},
				} {
					if err := d.Set([]string{kv.k}, kv.v); err != nil {
						return err
					}
				}
				return nil
			},
			want: "s = \"tab\\there \\\"q\\\" \\u007F\"\ni = -3\nf = 1.0\ninf = -inf\nb = true\nt = 2026-10-12T09:30:00Z\nd = 2026-10-12\narr = [1, \"two\", [\"x\"]]\ntbl = { a = \"x\", b = 2 }\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.edit(d); err != nil {
				t.Fatal(err)
			}
			if got := string(d.Bytes()); got != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestEditErrorsLeaveTheDocumentAlone(t *testing.T) {
	src := "a = { b = 1 }\nn = 1\n[t]\nx = 1\n"
	for name, edit := range map[string]func(d *Document) error{
		"add to an inline table":    func(d *Document) error { return d.Set([]string{"a", "c"}, 2) },
		"turn a value into a table": func(d *Document) error { return d.Set([]string{"n", "x"}, 2) },
		"set a table to a value":    func(d *Document) error { return d.Set([]string{"t"}, 2) },
		"unsupported value":         func(d *Document) error { return d.Set([]string{"z"}, struct{}{}) },
		"empty path":                func(d *Document) error { return d.Set(nil, 1) },
	} {
		d, _ := Parse([]byte(src))
		if err := edit(d); err == nil {
			t.Errorf("%s: succeeded:\n%s", name, d.Bytes())
		}
		if got := string(d.Bytes()); got != src {
			t.Errorf("%s: changed the document:\n%s", name, got)
		}
	}
}

func TestParseErrorPosition(t *testing.T) {
	_, err := Parse([]byte("a = 1\nb = \"x\nc = 3\n"))
	pe, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	if pe.Line != 2 || pe.Col != 7 {
		t.Errorf("error at %d:%d, want 2:7 (%v)", pe.Line, pe.Col, pe)
	}
}
