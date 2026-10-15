//go:build !unix

package update

import "errors"

// Reexec replaces this process with exe; only unix can.
func Reexec(string) error { return errors.New("re-exec is unix-only") }
