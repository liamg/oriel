package nvim

import (
	"bufio"
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := encode(w, v); err != nil {
		t.Fatalf("encode %#v: %v", v, err)
	}
	w.Flush()
	got, err := decode(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("decode %#v: %v", v, err)
	}
	return got
}

func TestRoundTrip(t *testing.T) {
	long := strings.Repeat("x", 70000)
	tests := []struct {
		in, want any
	}{
		{nil, nil},
		{true, true},
		{false, false},
		{0, int64(0)},
		{127, int64(127)},
		{128, int64(128)},
		{-1, int64(-1)},
		{-32, int64(-32)},
		{-33, int64(-33)},
		{-200, int64(-200)},
		{70000, int64(70000)},
		{-70000, int64(-70000)},
		{int64(math.MaxInt64), int64(math.MaxInt64)},
		{int64(math.MinInt64), int64(math.MinInt64)},
		{uint64(math.MaxUint64), uint64(math.MaxUint64)},
		{1.5, 1.5},
		{"", ""},
		{"héllo", "héllo"},
		{strings.Repeat("a", 40), strings.Repeat("a", 40)},
		{strings.Repeat("a", 300), strings.Repeat("a", 300)},
		{long, long},
		{[]byte{1, 2}, []byte{1, 2}},
		{[]string{"a", "b"}, []any{"a", "b"}},
		{[]any{1, "x", nil}, []any{int64(1), "x", nil}},
		{make([]any, 20), make([]any, 20)},
		{map[string]any{"a": 1, "b": []any{}}, map[string]any{"a": int64(1), "b": []any{}}},
		{Ext{Type: 0, Data: []byte{1}}, Ext{Type: 0, Data: []byte{1}}},
		{Ext{Type: 2, Data: []byte{1, 2, 3}}, Ext{Type: 2, Data: []byte{1, 2, 3}}},
	}
	for _, tt := range tests {
		if got := roundTrip(t, tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("round trip %#v: got %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

// Byte sequences from the msgpack spec's type table.
func TestDecodeSpecForms(t *testing.T) {
	tests := []struct {
		in   []byte
		want any
	}{
		{[]byte{0xcc, 0xff}, int64(255)},
		{[]byte{0xcd, 0x01, 0x00}, int64(256)},
		{[]byte{0xd0, 0x80}, int64(-128)},
		{[]byte{0xd1, 0xff, 0x00}, int64(-256)},
		{[]byte{0xca, 0x3f, 0xc0, 0x00, 0x00}, 1.5},
		{[]byte{0xd9, 0x01, 'a'}, "a"},
		{[]byte{0xd4, 0x01, 0x07}, Ext{Type: 1, Data: []byte{7}}},
		{[]byte{0x81, 0x01, 0x02}, map[any]any{int64(1): int64(2)}},
	}
	for _, tt := range tests {
		got, err := decode(bufio.NewReader(bytes.NewReader(tt.in)))
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("decode % x = %#v, %v; want %#v", tt.in, got, err, tt.want)
		}
	}
}
