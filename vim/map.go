package vim

import (
	"slices"
	"strings"

	"github.com/liamg/oriel/registry/keymap"
)

// mapping is a :map: keys typed as lhs act as rhs.
type mapping struct {
	rhs     []string
	noremap bool
}

// maxMapDepth stops a mapping or register that runs itself, as 'maxmapdepth'.
const maxMapDepth = 1000

func mapKey(keys []string) string { return strings.Join(keys, "\x00") }

// mapMode is the mode letter mappings are looked up in: n, v, o, i or c.
func (e *Engine) mapMode() byte {
	switch {
	case e.cmdline != nil:
		return 'c'
	case e.mode != Normal:
		return 'i'
	case e.visual != nil:
		return 'v'
	case len(e.pend) > 0 && e.opPending():
		return 'o'
	}
	return 'n'
}

// opPending reports whether the keys pending are an operator waiting for its motion.
func (e *Engine) opPending() bool {
	for _, k := range e.pend {
		if _, ok := operators[k]; ok {
			return true
		}
	}
	return false
}

// argPending reports whether the next key is a command's character argument, which isn't mapped.
func (e *Engine) argPending() bool {
	if e.mode != Normal || e.cmdline != nil || len(e.pend) == 0 {
		return false
	}
	last := e.pend[len(e.pend)-1]
	return takesArg[last] || last == `"`
}

// input takes a key through the mappings of the current mode.
func (e *Engine) input(k string) {
	if e.noremap > 0 || e.argPending() || len(e.maps[e.mapMode()]) == 0 {
		e.dispatch(k)
		return
	}
	e.mapBuf = append(e.mapBuf, k)
	e.resolveMaps(false)
}

// resolveMaps runs the buffered keys once they can't be the start of a longer mapping; flush runs them
// anyway, as when 'timeout' expires.
func (e *Engine) resolveMaps(flush bool) {
	for len(e.mapBuf) > 0 {
		m := e.maps[e.mapMode()]
		buf := e.mapBuf
		key := mapKey(buf)
		longer := false
		for lhs := range m {
			if len(lhs) > len(key) && strings.HasPrefix(lhs, key+"\x00") {
				longer = true
				break
			}
		}
		if longer && !flush {
			return
		}
		n := len(buf)
		for ; n > 0; n-- {
			if _, ok := m[mapKey(buf[:n])]; ok {
				break
			}
		}
		if n == 0 {
			e.mapBuf = buf[1:]
			e.dispatch(buf[0])
			continue
		}
		mp := m[mapKey(buf[:n])]
		rest := slices.Clone(buf[n:])
		e.mapBuf = nil
		e.runMapping(mp)
		e.mapBuf = append(e.mapBuf, rest...)
	}
}

func (e *Engine) runMapping(mp mapping) {
	if e.mapDepth >= maxMapDepth {
		e.notify(errRecursiveMapping.with())
		e.failed = true
		return
	}
	e.mapDepth++
	e.feeding++
	if mp.noremap {
		e.noremap++
	}
	saved := e.mapBuf
	e.mapBuf = nil
	for _, k := range mp.rhs {
		if e.failed && e.playing > 0 {
			break
		}
		e.input(k)
	}
	e.resolveMaps(true)
	e.mapBuf = saved
	if mp.noremap {
		e.noremap--
	}
	e.feeding--
	e.mapDepth--
}

// Mapper is a Host that keeps :map's mappings in its own keymap, so they show where the IDE lists keys. Modes
// are vim's letters: n, v, o, i and c.
type Mapper interface {
	Map(mode byte, lhs, rhs []string, noremap bool)
	Unmap(mode byte, lhs []string) bool
	MapClear(mode byte)
}

// TypeMapped types keys as a mapping's right-hand side: remapped unless noremap, and undone as one change.
func (e *Engine) TypeMapped(keys []string, noremap bool) {
	e.runMapping(mapping{rhs: keys, noremap: noremap})
	e.updateTop(e.view())
	e.sync()
}

// exMap runs :map and its family: name is the command, which says the modes and whether it remaps.
func (e *Engine) exMap(name, args string, bang bool) error {
	unmap := strings.Contains(name, "unmap") || strings.HasSuffix(name, "unm") || name == "unm"
	clear := strings.HasSuffix(name, "mapclear")
	noremap := strings.Contains(name, "nore") || strings.HasPrefix(name, "no") && !strings.HasPrefix(name, "nm")
	var modes string
	switch name[0] {
	case 'n':
		modes = "n"
		if strings.HasPrefix(name, "no") {
			modes = "nvo"
		}
	case 'v', 'x':
		modes = "v"
	case 'o':
		modes = "o"
	case 'i':
		modes = "i"
	case 'c':
		modes = "c"
	default:
		modes = "nvo"
	}
	if bang && (name == "map" || name == "noremap" || name == "unmap" || name == "mapclear") {
		modes = "ic"
	}
	if strings.HasPrefix(name, "nun") || strings.HasPrefix(name, "nm") || strings.HasPrefix(name, "nn") {
		modes = "n"
	}
	mapper, hosted := e.host.(Mapper)
	if clear {
		for _, m := range []byte(modes) {
			if hosted {
				mapper.MapClear(m)
			}
			delete(e.maps, m)
		}
		return nil
	}
	args = strings.TrimLeft(args, " \t")
	for {
		t, rest, _ := strings.Cut(args, " ")
		low := strings.ToLower(t)
		if low != "<silent>" && low != "<buffer>" && low != "<unique>" && low != "<nowait>" && low != "<special>" {
			break
		}
		args = strings.TrimLeft(rest, " \t")
	}
	lhsText, rhsText, _ := strings.Cut(args, " ")
	rhsText = strings.TrimLeft(rhsText, " \t")
	if lhsText == "" {
		return nil
	}
	lhs := mapKey(keymap.Parse(lhsText))
	if hosted {
		if unmap {
			found := false
			for _, m := range []byte(modes) {
				found = mapper.Unmap(m, keymap.Parse(lhsText)) || found
			}
			if !found {
				return errNoMapping.with()
			}
			return nil
		}
		if rhsText == "" {
			return nil
		}
		rhs := keymap.Parse(rhsText)
		if strings.EqualFold(rhsText, "<Nop>") {
			rhs = nil
		}
		for _, m := range []byte(modes) {
			mapper.Map(m, keymap.Parse(lhsText), rhs, noremap)
		}
		return nil
	}
	if unmap {
		found := false
		for _, m := range []byte(modes) {
			if _, ok := e.maps[m][lhs]; ok {
				delete(e.maps[m], lhs)
				found = true
			}
		}
		if !found {
			return errNoMapping.with()
		}
		return nil
	}
	if rhsText == "" {
		return nil
	}
	rhs := keymap.Parse(rhsText)
	if strings.EqualFold(rhsText, "<Nop>") {
		rhs = nil
	}
	if e.maps == nil {
		e.maps = map[byte]map[string]mapping{}
	}
	for _, m := range []byte(modes) {
		if e.maps[m] == nil {
			e.maps[m] = map[string]mapping{}
		}
		e.maps[m][lhs] = mapping{rhs: rhs, noremap: noremap}
	}
	return nil
}
