package vim

import (
	"slices"
	"strconv"
	"strings"
)

func (e *Engine) exSet(args string) error {
	for _, a := range strings.Fields(args) {
		name, val, hasVal := strings.Cut(a, "=")
		var op byte
		if n := len(name); hasVal && n > 0 && strings.IndexByte("+-^", name[n-1]) >= 0 {
			op, name = name[n-1], name[:n-1]
		}
		on, invert := true, false
		switch {
		case hasVal:
		case strings.HasSuffix(name, "!"):
			invert, name = true, name[:len(name)-1]
		case strings.HasPrefix(name, "inv"):
			if _, ok := optionDef(name[3:]); ok {
				invert, name = true, name[3:]
			}
		case strings.HasPrefix(name, "no"):
			on = false
			name = name[2:]
		}
		if d, ok := optionDef(name); ok {
			if d.Kind != BoolKind && (!on || invert) {
				return errUnknownOption.with(a)
			}
			if invert {
				on = !d.get(&e.opts).(bool)
			}
			v, err := e.setValue(d, a, val, hasVal, on, op)
			if err != nil {
				return err
			}
			if err := e.setOption(d, v); err != nil {
				return err
			}
			continue
		}
		switch name {
		case "fdm", "foldmethod":
			s := &e.window.fold
			if val != s.method {
				s.method = val
				if val != "manual" {
					s.folds = nil
					e.updateFolds(e.window)
				}
			}
		case "fdl", "foldlevel":
			n, err := strconv.Atoi(val)
			if err != nil {
				return errNumberRequired.with(a)
			}
			e.window.fold.level = n
			e.window.fold.manual = true
			e.window.fold.applyLevel()
		case "fen", "foldenable":
			e.window.fold.enable = on
		case "mod", "modified":
			e.buffer.modified = on
		default:
			return errUnknownOption.with(name)
		}
	}
	e.updateFolds(e.window)
	return nil
}

// listOption applies :set's =, +=, -= or ^= to a comma-separated option.
func listOption(cur, val string, op byte) string {
	items := strings.FieldsFunc(cur, func(r rune) bool { return r == ',' })
	switch op {
	case '+':
		if !slices.Contains(items, val) {
			items = append(items, val)
		}
	case '^':
		if !slices.Contains(items, val) {
			items = append([]string{val}, items...)
		}
	case '-':
		items = slices.DeleteFunc(items, func(s string) bool { return s == val })
	default:
		return val
	}
	return strings.Join(items, ",")
}
