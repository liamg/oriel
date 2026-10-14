// Package agentsock is the session socket a sandboxed agent reaches the host
// through: the host side (Handler, Serve, SSHAgent) and the container side
// (Client).
package agentsock

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
	"github.com/liamg/oriel/sandbox/mcp"
)

// TokenHeader carries the agent's token on every request.
const TokenHeader = "X-Oriel-Token"

// The routes the socket serves.
const (
	ToolsPath     = "/api/gateway/tools"
	CallPath      = "/api/gateway/call"
	DecidePath    = "/api/egress/decide"
	LogPath       = "/api/egress/log"
	SSHStatusPath = "/api/sandbox/ssh"
	SSHAgentPath  = "/api/sandbox/ssh-agent"
	HookPath      = "/api/sandbox/hook"
)

// maxBody bounds a request body.
const maxBody = 8 << 20

// Handler serves a sandboxed agent's routes to whoever holds Token.
type Handler struct {
	Token   string
	Gateway *gateway.Gateway
	Egress  *egress.Session
	SSH     *SSHAgent
	// Hook is the host socket the agent's hooks call back to its provider over, "" while there is none.
	Hook func() string
	// ToolsVersion changes whenever the gateway's tool list does.
	ToolsVersion func() int
	// Next serves any other route the agent may use; nil is a 404.
	Next http.Handler
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	got := r.Header.Get(TokenHeader)
	if h.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) != 1 {
		http.Error(w, "missing or wrong oriel token", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == ToolsPath && h.Gateway != nil:
		h.tools(w, r)
	case r.Method == http.MethodPost && r.URL.Path == CallPath && h.Gateway != nil:
		h.call(w, r)
	case r.Method == http.MethodPost && r.URL.Path == DecidePath && h.Egress != nil:
		h.decide(w, r)
	case r.Method == http.MethodPost && r.URL.Path == LogPath && h.Egress != nil:
		var ev egress.Event
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&ev); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.Egress.Record(ev)
		writeJSON(w, map[string]any{"ok": true})
	case r.Method == http.MethodPost && r.URL.Path == egress.DialPath && h.Egress != nil:
		h.Egress.ServeDial(w, r)
	case r.Method == http.MethodGet && r.URL.Path == SSHStatusPath && h.SSH != nil:
		writeJSON(w, map[string]any{"live": h.SSH.Live(), "until": h.SSH.Until(), "refused": h.SSH.Refused()})
	case r.Method == http.MethodPost && r.URL.Path == SSHAgentPath && h.SSH != nil:
		h.SSH.ServeUpgrade(w, r)
	case r.Method == http.MethodPost && r.URL.Path == HookPath && h.Hook != nil:
		h.hook(w, r)
	case h.Next != nil:
		h.Next.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) tools(w http.ResponseWriter, r *http.Request) {
	tools, err := h.Gateway.Tools(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	v := 0
	if h.ToolsVersion != nil {
		v = h.ToolsVersion()
	}
	writeJSON(w, map[string]any{"tools": tools, "version": v})
}

// call blocks while the user decides.
func (h *Handler) call(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil || in.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	res, err := h.Gateway.Call(r.Context(), in.Name, in.Arguments)
	if err != nil {
		res = mcp.TextResult(err.Error(), true)
	}
	writeJSON(w, res)
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil || in.Host == "" {
		http.Error(w, "host is required", http.StatusBadRequest)
		return
	}
	ok := h.Egress.Allow(r.Context(), in.Host, in.Port)
	if r.Context().Err() != nil {
		return
	}
	writeJSON(w, map[string]any{"allow": ok, "why": h.Egress.Why(in.Host, in.Port)})
}

// hook carries a connection the sidecar relays to the provider's hook socket.
func (h *Handler) hook(w http.ResponseWriter, r *http.Request) {
	path := h.Hook()
	if !egress.IsUpgrade(r) || path == "" {
		http.Error(w, "no hook socket", http.StatusBadRequest)
		return
	}
	up, err := net.Dial("unix", path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	_ = egress.ServeDial(w, up)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Serve answers h on the unix socket at path until ctx ends. With portFile
// set, it also listens on a loopback port, written to portFile, for the
// egress proxy to relay to where the socket cannot be mounted.
func Serve(ctx context.Context, h http.Handler, path, portFile string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	stop := context.AfterFunc(ctx, func() {
		_ = srv.Close()
		_ = os.Remove(path)
		if portFile != "" {
			_ = os.Remove(portFile)
		}
	})
	defer stop()
	if portFile != "" {
		tl, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = ln.Close()
			return err
		}
		port := strconv.Itoa(tl.Addr().(*net.TCPAddr).Port)
		if err := os.WriteFile(portFile, []byte(port), 0o600); err != nil {
			_ = tl.Close()
			_ = ln.Close()
			return err
		}
		go func() { _ = srv.Serve(tl) }()
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
