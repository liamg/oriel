package ui

import (
	"fmt"
	"strconv"
)

func childID(parent ID, key any) ID {
	h := uint64(fnvBasis)
	for i := range 8 {
		h = fnvByte(h, byte(parent>>(8*i)))
	}
	return ID(hashKey(h, key))
}

func fnvByte(h uint64, b byte) uint64 { return (h ^ uint64(b)) * fnvPrime }

func fnvString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = fnvByte(h, s[i])
	}
	return h
}

func fnvUint(h uint64, v uint64) uint64 {
	for i := range 8 {
		h = fnvByte(h, byte(v>>(8*i)))
	}
	return h
}

// hashKey folds key into h by its type and value, each type under a tag of its own so keys of different types
// don't meet.
func hashKey(h uint64, key any) uint64 {
	switch k := key.(type) {
	case string:
		return fnvString(fnvByte(h, 's'), k)
	case int:
		return fnvUint(fnvByte(h, 'i'), uint64(k))
	case ID:
		return fnvUint(fnvByte(h, 'd'), uint64(k))
	case motionKey:
		return fnvString(fnvByte(h, 'm'), k.kind)
	case animKey:
		return hashKey(fnvByte(h, 'a'), k.key)
	case easeKey:
		return hashKey(fnvByte(h, 'e'), k.key)
	}
	return fnvString(fnvByte(h, 'f'), fmt.Sprintf("%T:%v", key, key))
}

func keyName(key any) string {
	switch k := key.(type) {
	case string:
		return k
	case int:
		return strconv.Itoa(k)
	}
	return fmt.Sprint(key)
}

// name is e's key, as Dump shows it.
func (e *elem) name() string {
	if e.parent == nil {
		return "root"
	}
	return keyName(e.key)
}

// dupKey is a child's key as its siblings' duplicates are counted.
type dupKey struct {
	s   string
	i   int
	num bool
}

func dupOf(key any) dupKey {
	switch k := key.(type) {
	case string:
		return dupKey{s: k}
	case int:
		return dupKey{i: k, num: true}
	}
	return dupKey{s: fmt.Sprint(key)}
}

// dupIndex is how many children an elem has before it counts their keys in a map rather than by looking.
const dupIndex = 8

// built is how many of e's children were built from d.
func (e *elem) built(d dupKey) int {
	if e.keys != nil {
		return e.keys[d]
	}
	n := 0
	for _, k := range e.kids {
		if k.base == d {
			n++
		}
	}
	return n
}

// nextKey is the key the next child of e built from key gets: key itself, its index when nil, or key#n for
// the nth duplicate among its siblings.
func (e *elem) nextKey(key any) (any, dupKey) {
	if key == nil {
		key = len(e.kids)
	}
	d := dupOf(key)
	if n := e.built(d); n > 0 {
		key = fmt.Sprintf("%v#%d", key, n)
	}
	return key, d
}

const (
	fnvBasis = 14695981039346656037
	fnvPrime = 1099511628211
)
