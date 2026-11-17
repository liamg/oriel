// Package words is the small text a count is written with.
package words

import "fmt"

// Plural is one when n is 1, else many.
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Counted is n of what: "1 session", "64 sessions".
func Counted(n int, what string) string { return fmt.Sprintf("%d %s", n, Plural(n, what, what+"s")) }
