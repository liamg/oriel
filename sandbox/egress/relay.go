package egress

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

// Relay serves a unix socket at path and carries each connection to the
// session over TCP. Off Linux the proxy makes the sandbox's session sockets
// this way: Docker Desktop cannot share a host socket that keeps working.
type Relay struct {
	// Addr is the session's host:port, read for each connection: a
	// restarted session listens on a new port.
	Addr func() (string, error)
	// Token and UpgradePath, when UpgradePath is set, turn each connection
	// into a raw stream to that route, for what is not HTTP (an ssh-agent).
	Token       string
	UpgradePath string
}

// Serve listens on path until ctx ends.
func (r Relay) Serve(ctx context.Context, path string) error {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	context.AfterFunc(ctx, func() { _ = ln.Close() })
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go r.carry(ctx, c)
	}
}

func (r Relay) carry(ctx context.Context, c net.Conn) {
	up, err := r.dial(ctx)
	if err != nil {
		_ = c.Close()
		return
	}
	_, _, _ = splice(c, c, up)
}

func (r Relay) dial(ctx context.Context) (net.Conn, error) {
	addr, err := r.Addr()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	up, err := d.DialContext(ctx, "tcp", addr)
	if err != nil || r.UpgradePath == "" {
		return up, err
	}
	return Upgrade(ctx, up, r.Token, r.UpgradePath, url.Values{})
}

// PortFileAddr reads a port from file and joins it to host.
func PortFileAddr(host, file string) func() (string, error) {
	return func() (string, error) {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		port := strings.TrimSpace(string(b))
		if port == "" {
			return "", errors.New("the session has not written its port")
		}
		return net.JoinHostPort(host, port), nil
	}
}
