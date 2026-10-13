package b

import "example.com/watch/a"

func Quadruple(n int) int { return a.Double(a.Double(n)) }
