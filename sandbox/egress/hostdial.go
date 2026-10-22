package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"syscall"
	"time"
)

// DialPath is the session route that dials upstream for the proxy.
const DialPath = "/api/egress/dial"

// dialProto is the Upgrade token of a DialPath connection.
const dialProto = "oriel-egress"

// Scope is how far a destination's addresses may reach.
type Scope int

const (
	// Public reaches public addresses only.
	Public Scope = iota
	// Private also reaches private and tailnet addresses: for an allow the
	// user marked as reaching them.
	Private
)

var privateNets = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT: Tailscale
	netip.MustParsePrefix("fc00::/7"),
}

var (
	nat64      = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour  = netip.MustParsePrefix("2002::/16")
	teredo     = netip.MustParsePrefix("2001::/32")
	v4Compat   = netip.MustParsePrefix("::/96")
)

// CheckAddr refuses an address scope may not reach. Loopback, link-local
// (cloud metadata) and unspecified addresses are never reachable, nor is an
// IPv6 address that carries one of them (NAT64, 6to4, Teredo).
func CheckAddr(ip netip.Addr, scope Scope) error {
	ip = ip.Unmap()
	if err := checkOne(ip, scope); err != nil {
		return err
	}
	if v4, ok := embeddedV4(ip); ok {
		if err := checkOne(v4, scope); err != nil {
			return fmt.Errorf("%s carries %s: %w", ip, v4, err)
		}
	}
	if nat64Local.Contains(ip) && scope != Private {
		return fmt.Errorf("%s is a local NAT64 address: allow the host with private network access to reach it", ip)
	}
	return nil
}

func checkOne(ip netip.Addr, scope Scope) error {
	switch {
	case ip.IsLoopback(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(),
		ip.IsUnspecified(), ip.IsMulticast():
		return fmt.Errorf("%s is not reachable from the sandbox", ip)
	}
	for _, p := range privateNets {
		if p.Contains(ip) && scope != Private {
			return fmt.Errorf("%s is a private address: allow the host with private network access to reach it", ip)
		}
	}
	return nil
}

// embeddedV4 is the IPv4 address an IPv6 transition address leads to.
func embeddedV4(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() {
		return netip.Addr{}, false
	}
	b := ip.As16()
	switch {
	case nat64.Contains(ip), nat64Local.Contains(ip), v4Compat.Contains(ip):
		return netip.AddrFrom4([4]byte(b[12:16])), true
	case sixToFour.Contains(ip):
		return netip.AddrFrom4([4]byte(b[2:6])), true
	case teredo.Contains(ip):
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), true
	}
	return netip.Addr{}, false
}

// DialControl is a net.Dialer Control that refuses an address scope may not reach, so a dialer checks the
// address it actually connects to.
func DialControl(scope Scope) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("%s is not an address the sandbox dials: %w", address, err)
		}
		return CheckAddr(ap.Addr(), scope)
	}
}

// Resolver looks up a host's addresses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialChecked resolves host once and dials the first address scope allows,
// so the address checked is the address dialled.
func DialChecked(ctx context.Context, r Resolver, host string, port int, scope Scope) (net.Conn, error) {
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		if r == nil {
			r = net.DefaultResolver
		}
		if ips, err = r.LookupNetIP(ctx, "ip", host); err != nil {
			return nil, err
		}
	}
	d := net.Dialer{Timeout: defaultDialTimeout}
	var errs []error
	for _, ip := range ips {
		if err := CheckAddr(ip, scope); err != nil {
			errs = append(errs, err)
			continue
		}
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(port)))
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("%s has no addresses", host)
	}
	return nil, errors.Join(errs...)
}

// DialViaSession has the session at the end of dialSession dial host:port
// from the host machine, which reaches networks the container cannot (a
// tailnet, a VPN). The returned connection is the raw upstream stream.
func DialViaSession(ctx context.Context, dialSession func(context.Context) (net.Conn, error), token, host string, port int) (net.Conn, error) {
	conn, err := dialSession(ctx)
	if err != nil {
		return nil, err
	}
	c, err := Upgrade(ctx, conn, token, DialPath, url.Values{"host": {host}, "port": {strconv.Itoa(port)}})
	if err != nil {
		return nil, fmt.Errorf("the session did not dial %s:%d: %w", host, port, err)
	}
	return c, nil
}

// Upgrade asks the session at the other end of conn to turn a POST to path
// into a raw stream, and returns that stream. conn is closed on failure.
func Upgrade(ctx context.Context, conn net.Conn, token, path string, q url.Values) (net.Conn, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	req := fmt.Sprintf("POST %s?%s HTTP/1.1\r\nHost: oriel\r\nX-Oriel-Token: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\nContent-Length: 0\r\n\r\n",
		path, q.Encode(), token, dialProto)
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodPost})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = conn.Close()
		return nil, fmt.Errorf("%s %s", resp.Status, b)
	}
	if !stop() && ctx.Err() != nil {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// ServeDial answers a DialViaSession request: it hijacks w and pipes it to
// up, which the caller has already dialled.
func ServeDial(w http.ResponseWriter, up net.Conn) error {
	client, err := Hijack(w)
	if err != nil {
		_ = up.Close()
		return err
	}
	_, _, err = splice(client, client, up)
	return err
}

// Hijack answers an Upgrade request and returns its raw stream.
func Hijack(w http.ResponseWriter) (net.Conn, error) {
	c, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+dialProto+"\r\n\r\n"); err != nil {
		_ = c.Close()
		return nil, err
	}
	if brw.Reader.Buffered() > 0 {
		return &bufConn{Conn: c, r: brw.Reader}, nil
	}
	return c, nil
}

// IsUpgrade reports whether r is an Upgrade request.
func IsUpgrade(r *http.Request) bool {
	return r.Method == http.MethodPost && r.Header.Get("Upgrade") == dialProto
}
