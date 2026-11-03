package c

import "example.com/watch/b"

func Sixteen(n int) int { return b.Quadruple(b.Quadruple(n)) }
