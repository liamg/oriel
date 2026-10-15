package main

import "fmt"

type point struct {
	X, Y int
}

func half(v int) int {
	scaled := v * 10
	p := point{X: v, Y: scaled}
	out := p.Y / 2
	return out
}

func main() {
	total := 0
	for i := 1; i <= 4; i++ {
		total += half(i)
	}
	fmt.Println("total", total)
}
