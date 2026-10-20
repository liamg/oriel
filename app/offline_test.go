package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

// offlineNet is the HTTP a test scene's plugins get unless the test gives its own: loopback only, so no test reaches
// a real server.
var offlineNet = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, _ := net.SplitHostPort(addr)
	if ip, err := netip.ParseAddr(host); host != "localhost" && (err != nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("tests don't reach the network: %s", addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}}}
