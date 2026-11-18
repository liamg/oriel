package registry

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ArgType is the kind of value a command's argument takes, as Args reads it.
type ArgType string

const (
	ArgAny    ArgType = "any"    // anything
	ArgString ArgType = "string" // Args.String
	ArgText   ArgType = "text"   // Args.Trimmed: a string, or a number as it's written
	ArgInt    ArgType = "int"    // Args.Int: a whole number, or a string of digits
	ArgFloat  ArgType = "float"  // Args.Float
	ArgBool   ArgType = "bool"   // Args.Bool
	ArgList   ArgType = "list"   // Args.Strings or Args.Maps: a list, or a string of comma-separated items
	ArgMap    ArgType = "map"    // a table
)

// ArgTypes are the types an argument may declare.
var ArgTypes = []ArgType{ArgAny, ArgString, ArgText, ArgInt, ArgFloat, ArgBool, ArgList, ArgMap}

// Arg is one argument a command takes.
type Arg struct {
	Name     string
	Type     ArgType // ArgAny when ""
	Required bool
	Doc      string
}

// StringArg and the rest are an argument of their type, by name.
func StringArg(name string) Arg { return Arg{Name: name, Type: ArgString} }
func TextArg(name string) Arg   { return Arg{Name: name, Type: ArgText} }
func IntArg(name string) Arg    { return Arg{Name: name, Type: ArgInt} }
func FloatArg(name string) Arg  { return Arg{Name: name, Type: ArgFloat} }
func BoolArg(name string) Arg   { return Arg{Name: name, Type: ArgBool} }
func ListArg(name string) Arg   { return Arg{Name: name, Type: ArgList} }
func MapArg(name string) Arg    { return Arg{Name: name, Type: ArgMap} }
func AnyArg(name string) Arg    { return Arg{Name: name, Type: ArgAny} }

// MoreArgs is the Arg of a command that takes keys it doesn't name too: one that hands its args to another command,
// or to Lua.
var MoreArgs = Arg{Name: "*", Type: ArgAny, Doc: "keys it hands on"}

// NoArgs is the Args of a command that takes none.
var NoArgs = []Arg{}

// ArgError is a command run with an argument it doesn't take, of the wrong type, or without one it needs.
type ArgError struct {
	Command, Key, Why string
}

func (e *ArgError) Error() string {
	return fmt.Sprintf("command %q: argument %q %s", e.Command, e.Key, e.Why)
}

// CheckArgs says what's wrong with args for c: a key it doesn't take, a value of the wrong type, a required one
// missing. A command that declares no Args (nil) takes anything. A ! typed after its ex name comes as "bang".
func CheckArgs(c Command, args Args) error {
	if c.Args == nil {
		return nil
	}
	more := slices.ContainsFunc(c.Args, func(a Arg) bool { return a.Name == MoreArgs.Name })
	for _, k := range sortedArgKeys(args) {
		v := args[k]
		i := slices.IndexFunc(c.Args, func(a Arg) bool { return a.Name == k })
		switch {
		case i >= 0:
			if v != nil && !c.Args[i].Type.takes(v) {
				return &ArgError{Command: c.ID, Key: k, Why: fmt.Sprintf("is %s, want %s", describe(v), cmpType(c.Args[i].Type))}
			}
		case more, k == "bang" && c.Ex != "", v == nil:
		default:
			return &ArgError{Command: c.ID, Key: k, Why: "isn't one it takes" + takesList(c.Args)}
		}
	}
	for _, a := range c.Args {
		if a.Required && args[a.Name] == nil {
			return &ArgError{Command: c.ID, Key: a.Name, Why: "is required"}
		}
	}
	return nil
}

// Takes says c takes the argument key: it names it, takes more, or declares nothing.
func (c Command) Takes(key string) bool {
	return c.Args == nil || key == "bang" && c.Ex != "" ||
		slices.ContainsFunc(c.Args, func(a Arg) bool { return a.Name == key || a.Name == MoreArgs.Name })
}

// ContextArgs are the keys of where (what a menu or a card knows of where it was clicked: the view, the file, the
// session) that id's command takes, with own over them: a command isn't handed context it doesn't take.
func ContextArgs(r interface{ CommandFor(string) (Command, bool) }, id string, where, own Args) Args {
	c, ok := r.CommandFor(id)
	out := Args{}
	for k, v := range where {
		if !ok || c.Takes(k) {
			out[k] = v
		}
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}

func sortedArgKeys(args Args) []string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func takesList(spec []Arg) string {
	var names []string
	for _, a := range spec {
		if a.Name != MoreArgs.Name {
			names = append(names, a.Name)
		}
	}
	if len(names) == 0 {
		return ": it takes none"
	}
	return ": it takes " + strings.Join(names, ", ")
}

func cmpType(t ArgType) ArgType {
	if t == "" {
		return ArgAny
	}
	return t
}

// takes says v is a value of type t, as the Args reader for it would read it.
func (t ArgType) takes(v any) bool {
	switch cmpType(t) {
	case ArgAny:
		return true
	case ArgString:
		_, ok := v.(string)
		return ok
	case ArgText:
		switch v.(type) {
		case string, int, float64:
			return true
		}
	case ArgInt:
		switch n := v.(type) {
		case int:
			return true
		case float64:
			return n == math.Trunc(n)
		case string:
			_, err := strconv.Atoi(strings.TrimSpace(n))
			return err == nil
		}
	case ArgFloat:
		switch v.(type) {
		case int, float64:
			return true
		}
	case ArgBool:
		_, ok := v.(bool)
		return ok
	case ArgList:
		switch v.(type) {
		case []string, []any, []Args, []map[string]any, string:
			return true
		}
	case ArgMap:
		switch v.(type) {
		case map[string]any, Args:
			return true
		}
	}
	return false
}

func describe(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case int, float64:
		return "a number"
	case []string, []any, []Args, []map[string]any:
		return "a list"
	case map[string]any, Args:
		return "a table"
	}
	return fmt.Sprintf("a %T", v)
}
