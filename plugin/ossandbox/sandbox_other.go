//go:build !linux && !darwin

package ossandbox

import "os/exec"

// Available says this OS can't confine a process.
func Available() error { return ErrUnavailable }

// Main does nothing here.
func Main() {}

func command(Profile, string, []string) (*exec.Cmd, error) { return nil, ErrUnavailable }
