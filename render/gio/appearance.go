package gio

import (
	"sync"
	"time"
)

// appearanceEvery is how often the OS's light or dark preference is read.
const appearanceEvery = 3 * time.Second

// watchAppearance calls fn with the OS's preference now, then on every change, until stop. A platform that can't
// tell never calls it.
func watchAppearance(fn func(dark bool)) (stop func()) {
	done := make(chan struct{})
	go func() {
		last, known := osDark()
		if known {
			fn(last)
		}
		t := time.NewTicker(appearanceEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if dark, ok := osDark(); ok && (!known || dark != last) {
					last, known = dark, true
					fn(dark)
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}
