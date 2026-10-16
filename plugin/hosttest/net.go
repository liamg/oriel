package hosttest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
)

// Net is a client whose every connection reaches srv, a TLS test server it trusts for any host name: a request to
// https://hooks.slack.com/… is answered by srv, with the host checks as they'd be for the real one.
func Net(srv *httptest.Server) *http.Client {
	c := srv.Client()
	tr := c.Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig.ServerName = "example.com" // the name httptest's certificate is for
	return &http.Client{Transport: tr}
}
