// Package helper carries the sandbox helper (cmd/oriel-sandbox) built for Linux on this binary's arch, gzipped, as
// `make helper` leaves it in bin/. The desktop app embeds it and hands it to the docker backend, whose containers
// run it as their oriel; only the app imports this package, so the helper never embeds itself.
package helper

import (
	"bytes"
	"compress/gzip"
	"embed"
	"errors"
	"io"
	"runtime"
)

//go:embed all:bin
var bin embed.FS

// ErrMissing is a build made without `make helper`.
var ErrMissing = errors.New("this oriel was built without its sandbox helper: build it with `make build` (or `make run`)")

// Name is the helper's file in bin/ for arch.
func Name(arch string) string { return "oriel-sandbox-linux-" + arch + ".gz" }

var carried []byte

// Use makes gz the helper in place of bin/'s: a custom build (`oriel build`) carries over the one its builder had.
func Use(gz []byte) { carried = gz }

// Embedded reports whether this build carries the helper.
func Embedded() bool {
	_, err := Gzipped()
	return err == nil
}

// Gzipped is the helper as carried.
func Gzipped() ([]byte, error) {
	if carried != nil {
		return carried, nil
	}
	gz, err := bin.ReadFile("bin/" + Name(runtime.GOARCH))
	if err != nil {
		return nil, ErrMissing
	}
	return gz, nil
}

// Binary is the helper, decompressed.
func Binary() ([]byte, error) {
	gz, err := Gzipped()
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
