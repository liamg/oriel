//go:build !unix

package testrun

import "os/exec"

func killGroup(c *exec.Cmd) {}
