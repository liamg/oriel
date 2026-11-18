package api

import (
	"errors"
	"fmt"

	"github.com/liamg/oriel/script"
)

// fields reads a record-shaped Lua table (an inbox card, say), noting each field that's there but of the wrong
// type, so a plugin hears of `mine = "true"` rather than getting false.
type fields struct {
	t    script.Table
	what string
	errs []error
}

func (f *fields) wrong(k, want string) {
	f.errs = append(f.errs, fmt.Errorf("%s's %s is a %s, not a %s", f.what, k, luaType(f.t[k]), want))
}

func (f *fields) str(k string) string {
	v, ok := f.t[k]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		f.wrong(k, "string")
	}
	return s
}

func (f *fields) boolean(k string) bool {
	v, ok := f.t[k]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	if !ok {
		f.wrong(k, "boolean")
	}
	return b
}

func (f *fields) number(k string) (float64, bool) {
	v, ok := f.t[k]
	if !ok || v == nil {
		return 0, false
	}
	n, ok := v.(float64)
	if !ok {
		f.wrong(k, "number")
	}
	return n, ok
}

// err is every wrongly typed field, or nil.
func (f *fields) err() error { return errors.Join(f.errs...) }

// luaType is the Lua name of v's type.
func luaType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64, int, int64:
		return "number"
	case bool:
		return "boolean"
	case *script.Fn:
		return "function"
	case script.Table, []any:
		return "table"
	}
	return fmt.Sprintf("%T", v)
}
