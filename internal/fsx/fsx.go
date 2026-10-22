// Package fsx is file, process and network I/O for code the frame-path check can't tell runs off the loop: under the
// oriel_framecheck build tag each call panics when it's made on a loop goroutine in a frame (Enter).
package fsx

import (
	"io/fs"
	"net/http"
	"os"
	"os/exec"
)

func Stat(name string) (fs.FileInfo, error) {
	guard("os.Stat")
	return os.Stat(name)
}

func Lstat(name string) (fs.FileInfo, error) {
	guard("os.Lstat")
	return os.Lstat(name)
}

func ReadFile(name string) ([]byte, error) {
	guard("os.ReadFile")
	return os.ReadFile(name)
}

func ReadDir(name string) ([]os.DirEntry, error) {
	guard("os.ReadDir")
	return os.ReadDir(name)
}

func WriteFile(name string, data []byte, perm fs.FileMode) error {
	guard("os.WriteFile")
	return os.WriteFile(name, data, perm)
}

// Run runs cmd and waits for it.
func Run(cmd *exec.Cmd) error {
	guard("exec " + cmd.Path)
	return cmd.Run()
}

// Output runs cmd and answers its standard output.
func Output(cmd *exec.Cmd) ([]byte, error) {
	guard("exec " + cmd.Path)
	return cmd.Output()
}

// Transport is rt, or http.DefaultTransport when nil, checking each request.
func Transport(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return transport{rt}
}

type transport struct{ rt http.RoundTripper }

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	guard("http " + r.URL.Host)
	return t.rt.RoundTrip(r)
}
