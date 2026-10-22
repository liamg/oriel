package ui

import "reflect"

// Provide gives the UI's widgets a service of type T, which they find with Service: the app's dictation
// (widget.Dictator). A nil one takes it away.
func Provide[T any](u *UI, v T) { ProvideType(u, reflect.TypeFor[T](), v) }

// ProvideType is Provide for a type known at run time: a plugin's service, given through its host.
func ProvideType(u *UI, t reflect.Type, v any) {
	if v == nil {
		delete(u.services, t)
		return
	}
	if u.services == nil {
		u.services = map[reflect.Type]any{}
	}
	u.services[t] = v
}

// Service is the UI's service of type T (Provide), if it has one.
func Service[T any](c *Ctx) (T, bool) {
	v, ok := c.u.services[reflect.TypeFor[T]()].(T)
	return v, ok
}

// Keep is the UI's value of type T, made by make the first time it's asked for: what its widgets keep between
// frames, such as a cache, for as long as the UI lasts.
func Keep[T any](c *Ctx, make func() T) T {
	if v, ok := Service[T](c); ok {
		return v
	}
	v := make()
	Provide(c.u, v)
	return v
}
