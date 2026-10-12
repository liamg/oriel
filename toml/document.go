package toml

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Document is a parsed TOML document that can be edited in place: an edit rewrites only the bytes it has to,
// so comments, order and formatting survive.
type Document struct {
	src     []byte
	root    map[string]any
	keyvals []keyval
	headers []header
}

// Parse reads a TOML 1.0 document. Values decode to string, int64, float64, bool, time.Time (offset date-times),
// LocalDateTime, LocalDate, LocalTime, []any and map[string]any.
func Parse(src []byte) (*Document, error) {
	p, err := parse(src)
	if err != nil {
		return nil, err
	}
	return &Document{src: slices.Clone(src), root: p.root.toMap(), keyvals: p.keyvals, headers: p.headers}, nil
}

// Bytes is the document's source, with every edit applied.
func (d *Document) Bytes() []byte { return slices.Clone(d.src) }

// Value is the whole document as a table.
func (d *Document) Value() map[string]any { return d.root }

// Get returns the value at path: a value, or a map[string]any for a table.
func (d *Document) Get(path ...string) (any, bool) {
	var v any = d.root
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

func (d *Document) find(path []string) *keyval {
	for i, kv := range d.keyvals {
		if !kv.aot && slices.Equal(kv.path, path) {
			return &d.keyvals[i]
		}
	}
	return nil
}

func (d *Document) newline() string {
	if bytes.Contains(d.src, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// replace swaps src[start:end] for s and reparses, leaving d as it was if the result isn't valid.
func (d *Document) replace(start, end int, s string) error {
	src := slices.Concat(d.src[:start], []byte(s), d.src[end:])
	nd, err := Parse(src)
	if err != nil {
		return err
	}
	*d = *nd
	return nil
}

// Set sets the value at path, replacing an existing value's bytes or adding a key: after the last key of the table
// that holds it, next to dotted keys that define its table, or in a new table at the end.
func (d *Document) Set(path []string, v any) error {
	if len(path) == 0 {
		return fmt.Errorf("toml: set: empty path")
	}
	enc, err := encode(v)
	if err != nil {
		return fmt.Errorf("toml: set %s: %w", formatKey(path), err)
	}
	if kv := d.find(path); kv != nil {
		return d.wrap("set", path, d.replace(kv.valStart, kv.valEnd, enc))
	}
	if _, ok := d.Get(path...); ok {
		return fmt.Errorf("toml: set %s: it's a table, or in an array of tables", formatKey(path))
	}
	nl := d.newline()
	parent, best := path[:len(path)-1], -1
	for i, h := range d.headers {
		if !h.aot && len(h.path) <= len(parent) && slices.Equal(h.path, parent[:len(h.path)]) && (best < 0 || len(h.path) > len(d.headers[best].path)) {
			best = i
		}
	}
	if best < 0 && len(path) > 1 && !d.rootHas(path[0]) {
		var b strings.Builder
		if len(d.src) > 0 {
			if !bytes.HasSuffix(d.src, []byte("\n")) {
				b.WriteString(nl)
			}
			b.WriteString(nl)
		}
		b.WriteString("[" + formatKey(parent) + "]" + nl + formatKey(path[len(path)-1:]) + " = " + enc + nl)
		return d.wrap("set", path, d.replace(len(d.src), len(d.src), b.String()))
	}
	var rest []string
	if best >= 0 {
		rest = path[len(d.headers[best].path):]
	} else {
		rest = path
	}
	line := formatKey(rest) + " = " + enc + nl
	var last *keyval
	for i, kv := range d.keyvals {
		if !kv.member && kv.section == best {
			last = &d.keyvals[i]
		}
	}
	at, prefix, suffix := 0, "", ""
	switch {
	case last != nil:
		at, prefix = last.lineEnd, string(d.src[last.lineStart:last.keyStart])
	case best >= 0:
		at = d.headers[best].lineEnd
	case len(d.headers) > 0:
		at, suffix = d.headers[0].lineStart, nl
	default:
		at = len(d.src)
	}
	if at == len(d.src) && at > 0 && !bytes.HasSuffix(d.src, []byte("\n")) {
		prefix = nl + prefix
	}
	return d.wrap("set", path, d.replace(at, at, prefix+line+suffix))
}

func (d *Document) rootHas(k string) bool {
	for _, kv := range d.keyvals {
		if !kv.member && kv.section < 0 && kv.path[0] == k {
			return true
		}
	}
	return false
}

func (d *Document) wrap(op string, path []string, err error) error {
	if err != nil {
		return fmt.Errorf("toml: %s %s: %w", op, formatKey(path), err)
	}
	return nil
}

// Delete removes the key or table at path, with everything under it. Deleting what isn't there does nothing.
func (d *Document) Delete(path ...string) error {
	for range len(d.keyvals) + len(d.headers) + 1 {
		if _, ok := d.Get(path...); !ok {
			return nil
		}
		start, end, ok := d.span(path)
		if !ok {
			return fmt.Errorf("toml: delete %s: it's in an array of tables", formatKey(path))
		}
		if err := d.replace(start, end, ""); err != nil {
			return d.wrap("delete", path, err)
		}
	}
	return fmt.Errorf("toml: delete %s: still there", formatKey(path))
}

// span finds one stretch of source to remove towards deleting path.
func (d *Document) span(path []string) (int, int, bool) {
	under := func(p []string) bool { return len(p) >= len(path) && slices.Equal(p[:len(path)], path) }
	for _, kv := range d.keyvals {
		if kv.aot || !under(kv.path) {
			continue
		}
		if !kv.member {
			return kv.lineStart, kv.lineEnd, true
		}
		if d.find(kv.path[:len(kv.path)-1]) == nil || len(kv.path) == len(path) {
			return d.memberSpan(kv)
		}
	}
	for i, h := range d.headers {
		if !h.aot && under(h.path) {
			end := len(d.src)
			if i+1 < len(d.headers) {
				end = d.headers[i+1].lineStart
			}
			return h.lineStart, end, true
		}
	}
	return 0, 0, false
}

// memberSpan is an inline table member with the comma that separates it from its neighbour.
func (d *Document) memberSpan(kv keyval) (int, int, bool) {
	isWS := func(c byte) bool { return c == ' ' || c == '\t' }
	end := kv.valEnd
	for end < len(d.src) && isWS(d.src[end]) {
		end++
	}
	if end < len(d.src) && d.src[end] == ',' {
		end++
		for end < len(d.src) && isWS(d.src[end]) {
			end++
		}
		return kv.keyStart, end, true
	}
	start := kv.keyStart
	for start > 0 && isWS(d.src[start-1]) {
		start--
	}
	if start > 0 && d.src[start-1] == ',' {
		return start - 1, kv.valEnd, true
	}
	return start, kv.valEnd, true
}

func formatKey(path []string) string {
	parts := make([]string, len(path))
	for i, k := range path {
		bare := k != ""
		for j := 0; j < len(k); j++ {
			bare = bare && isBare(k[j])
		}
		if bare {
			parts[i] = k
		} else {
			parts[i] = quote(k)
		}
	}
	return strings.Join(parts, ".")
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// encode writes v as a TOML value.
func encode(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", fmt.Errorf("TOML has no null")
	case string:
		return quote(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	case float32:
		return encodeFloat(float64(v)), nil
	case float64:
		return encodeFloat(v), nil
	case time.Time:
		return v.Format(time.RFC3339Nano), nil
	case LocalDate, LocalTime, LocalDateTime:
		return v.(fmt.Stringer).String(), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if rv.Uint() > math.MaxInt64 {
			return "", fmt.Errorf("%d is out of range for a TOML integer", rv.Uint())
		}
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Slice, reflect.Array:
		parts := make([]string, rv.Len())
		for i := range parts {
			s, err := encode(rv.Index(i).Interface())
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return "", fmt.Errorf("a table's keys are strings, not %s", rv.Type().Key())
		}
		keys := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return "{}", nil
		}
		parts := make([]string, len(keys))
		for i, k := range keys {
			s, err := encode(rv.MapIndex(reflect.ValueOf(k).Convert(rv.Type().Key())).Interface())
			if err != nil {
				return "", err
			}
			parts[i] = formatKey([]string{k}) + " = " + s
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}
	return "", fmt.Errorf("can't write a %T as TOML", v)
}

func encodeFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".en") {
		s += ".0"
	}
	return s
}
