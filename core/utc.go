package core

import (
	"reflect"
	"time"
)

// InUTC is ev with every time in it in UTC: events are stored in one clock, and shown in local time. ev itself is
// left as it is; it's returned as is when it holds no other zone.
func InUTC(ev Event) Event {
	if v, ok := inUTC(reflect.ValueOf(ev)); ok {
		return v.Interface().(Event)
	}
	return ev
}

var timeType = reflect.TypeFor[time.Time]()

// inUTC is v with its times in UTC, and true, when any wasn't; v and what it points to are never changed.
func inUTC(v reflect.Value) (reflect.Value, bool) {
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == timeType {
			t := v.Interface().(time.Time)
			if t.Location() == time.UTC {
				return v, false
			}
			return reflect.ValueOf(t.UTC()), true
		}
		var out reflect.Value
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			f, ok := inUTC(v.Field(i))
			if !ok {
				continue
			}
			if !out.IsValid() {
				out = reflect.New(v.Type()).Elem()
				out.Set(v)
			}
			out.Field(i).Set(f)
		}
		return out, out.IsValid()
	case reflect.Pointer:
		if v.IsNil() {
			return v, false
		}
		e, ok := inUTC(v.Elem())
		if !ok {
			return v, false
		}
		p := reflect.New(v.Type().Elem())
		p.Elem().Set(e)
		return p, true
	case reflect.Interface:
		if v.IsNil() {
			return v, false
		}
		e, ok := inUTC(v.Elem())
		if !ok {
			return v, false
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(e)
		return out, true
	case reflect.Slice, reflect.Array:
		var out reflect.Value
		for i := range v.Len() {
			e, ok := inUTC(v.Index(i))
			if !ok {
				continue
			}
			if !out.IsValid() {
				if v.Kind() == reflect.Slice {
					out = reflect.MakeSlice(v.Type(), v.Len(), v.Len())
				} else {
					out = reflect.New(v.Type()).Elem()
				}
				reflect.Copy(out, v)
			}
			out.Index(i).Set(e)
		}
		return out, out.IsValid()
	case reflect.Map:
		var out reflect.Value
		for it := v.MapRange(); it.Next(); {
			e, ok := inUTC(it.Value())
			if !ok {
				continue
			}
			if !out.IsValid() {
				out = reflect.MakeMapWithSize(v.Type(), v.Len())
				for jt := v.MapRange(); jt.Next(); {
					out.SetMapIndex(jt.Key(), jt.Value())
				}
			}
			out.SetMapIndex(it.Key(), e)
		}
		return out, out.IsValid()
	}
	return v, false
}
