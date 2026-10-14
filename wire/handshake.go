package wire

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

// Protocol is bumped whenever the RPC surface changes shape. It is the only thing that gates a connection.
const Protocol = 1

// helloMax bounds what an unauthenticated peer can make us read.
const helloMax = 2048

const handshakeTimeout = 15 * time.Second

// Hello is the first and only thing a worker sends before it is authenticated. Every field is the peer's claim.
type Hello struct {
	Name     string `json:"name"`
	Key      string `json:"key,omitempty"`
	Protocol int    `json:"protocol"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Home     string `json:"home,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Version  string `json:"version,omitempty"`
}

// The outcomes of a hello.
const (
	StatusOK       = "ok"
	StatusDenied   = "denied"
	StatusMismatch = "protocol-mismatch"
)

// Welcome is the app's answer to a Hello.
type Welcome struct {
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Protocol int    `json:"protocol"`
}

// Authenticator decides what to do with a worker that has just said hello, given the observed address.
type Authenticator interface {
	Authenticate(remote net.Addr, h Hello) Welcome
}

type AuthenticatorFunc func(net.Addr, Hello) Welcome

func (f AuthenticatorFunc) Authenticate(remote net.Addr, h Hello) Welcome { return f(remote, h) }

// KeyAuth lets in a worker whose Hello carries one of keys, by worker name.
func KeyAuth(keys func(name string) (string, bool)) Authenticator {
	return AuthenticatorFunc(func(_ net.Addr, h Hello) Welcome {
		want, ok := keys(h.Name)
		if !ok || h.Key == "" || subtle.ConstantTimeCompare([]byte(want), []byte(h.Key)) != 1 {
			return Welcome{Status: StatusDenied, Reason: "unknown worker or wrong key"}
		}
		return Welcome{Status: StatusOK}
	})
}

// ErrRejected is returned when a worker was not let in.
type ErrRejected struct {
	Welcome Welcome
	Hello   Hello
}

func (e *ErrRejected) Error() string {
	if e.Welcome.Reason != "" {
		return e.Welcome.Status + ": " + e.Welcome.Reason
	}
	return e.Welcome.Status
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 20 * time.Second
	cfg.LogOutput = io.Discard
	return cfg
}

func writeHandshake(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > helloMax {
		return fmt.Errorf("frame too large: %d bytes", len(b))
	}
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(b)))
	if _, err := w.Write(n[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readHandshake(r io.Reader, v any) error {
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return err
	}
	size := int(binary.BigEndian.Uint16(n[:]))
	if size > helloMax {
		return fmt.Errorf("frame too large: %d bytes", size)
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Accept completes the handshake on a freshly accepted connection (the app's side) and returns the
// multiplexed connection. raw is closed on any error.
func Accept(raw net.Conn, tlsCfg *tls.Config, auth Authenticator) (*Peer, error) {
	remote := raw.RemoteAddr()
	tc := tls.Server(raw, tlsCfg)
	_ = tc.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := tc.Handshake(); err != nil {
		_ = tc.Close()
		return nil, fmt.Errorf("tls handshake from %s: %w", remote, err)
	}
	var h Hello
	if err := readHandshake(tc, &h); err != nil {
		_ = tc.Close()
		return nil, fmt.Errorf("hello from %s: %w", remote, err)
	}
	w := Welcome{Protocol: Protocol}
	if h.Protocol != Protocol {
		w.Status = StatusMismatch
		w.Reason = fmt.Sprintf("the app speaks protocol %d, the worker %d", Protocol, h.Protocol)
	} else {
		w = auth.Authenticate(remote, h)
		w.Protocol = Protocol
	}
	if err := writeHandshake(tc, w); err != nil {
		_ = tc.Close()
		return nil, fmt.Errorf("welcome to %s: %w", remote, err)
	}
	if w.Status != StatusOK {
		_ = tc.Close()
		return nil, &ErrRejected{Welcome: w, Hello: h}
	}
	_ = tc.SetDeadline(time.Time{})
	sess, err := yamux.Server(tc, yamuxConfig())
	if err != nil {
		_ = tc.Close()
		return nil, err
	}
	return &Peer{Hello: h, Remote: remote, Conn: New(sess)}, nil
}

// Dial connects a worker to the app, pinning the app's certificate by fingerprint. The Welcome is returned
// even when it is a refusal, so the worker can say why.
func Dial(ctx context.Context, addr, fingerprint string, h Hello) (*Peer, Welcome, error) {
	h.Protocol = Protocol
	if fingerprint == "" {
		return nil, Welcome{}, errors.New("no fingerprint to pin")
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, Welcome{}, err
	}
	tc := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true, // verified by the pin below, not by name or CA
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("the app sent no certificate")
			}
			got := FingerprintDER(rawCerts[0])
			if subtle.ConstantTimeCompare([]byte(got), []byte(fingerprint)) != 1 {
				return fmt.Errorf("the app's fingerprint is %s, expected %s", got, fingerprint)
			}
			return nil
		},
	})
	_ = tc.SetDeadline(time.Now().Add(handshakeTimeout))
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = tc.Close()
		return nil, Welcome{}, err
	}
	if err := writeHandshake(tc, h); err != nil {
		_ = tc.Close()
		return nil, Welcome{}, err
	}
	var w Welcome
	if err := readHandshake(tc, &w); err != nil {
		_ = tc.Close()
		return nil, Welcome{}, err
	}
	if w.Status != StatusOK {
		_ = tc.Close()
		return nil, w, &ErrRejected{Welcome: w, Hello: h}
	}
	_ = tc.SetDeadline(time.Time{})
	sess, err := yamux.Client(tc, yamuxConfig())
	if err != nil {
		_ = tc.Close()
		return nil, w, err
	}
	return &Peer{Hello: h, Remote: raw.RemoteAddr(), Conn: New(sess)}, w, nil
}

// Peer is an authenticated connection and what the worker said about itself.
type Peer struct {
	Hello  Hello
	Remote net.Addr // observed, not claimed
	Conn   Conn
}

// FingerprintDER is the SHA-256 of a DER certificate, lower-case hex: what a worker pins.
func FingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

var words = []string{
	"amber", "anchor", "apple", "arrow", "basin", "beacon", "birch", "bolt",
	"brass", "cable", "canyon", "cedar", "chalk", "cliff", "clover", "cobalt",
	"comet", "copper", "coral", "crane", "delta", "dune", "ember", "falcon",
	"fern", "flint", "forge", "garnet", "glacier", "granite", "harbour", "hazel",
	"heron", "indigo", "ivory", "jasper", "kestrel", "lantern", "linen", "lumen",
	"maple", "marble", "meadow", "mesa", "onyx", "orbit", "otter", "pebble",
	"pewter", "pine", "quarry", "quartz", "raven", "reef", "ridge", "saffron",
	"sable", "slate", "spruce", "thistle", "timber", "tundra", "walnut", "willow",
}

// Words renders a fingerprint as six words, for comparing two screens by eye.
func Words(fingerprint string) string {
	sum := sha256.Sum256([]byte(fingerprint))
	out := make([]byte, 0, 48)
	for i := range 6 {
		if i > 0 {
			out = append(out, '-')
		}
		out = append(out, words[int(sum[i])%len(words)]...)
	}
	return string(out)
}
