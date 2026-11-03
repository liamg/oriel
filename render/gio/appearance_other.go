//go:build !darwin && !linux

package gio

func osDark() (dark, ok bool) { return false, false }

func appearanceSignals(func()) (stop func()) { return func() {} }
