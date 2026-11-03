// Package agentcmd is what runs inside a sandbox's containers: the gateway's
// stdio MCP bridge, the egress proxy, ssh's ProxyCommand and git's credential
// helper. The binary mounted at /usr/local/bin/oriel dispatches to Main.
package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/agentsock"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/mcp"
	"github.com/liamg/oriel/sandbox/runner"
)

// sandboxClient is the session socket Oriel mounted into the sandbox.
func sandboxClient() (*agentsock.Client, error) {
	sock := os.Getenv("ORIEL_SOCKET")
	if sock == "" {
		return nil, errors.New("not in an oriel sandbox: ORIEL_SOCKET is not set")
	}
	return agentsock.NewSocket(sock, os.Getenv("ORIEL_TOKEN")), nil
}

// MCP serves the gateway to the agent as an MCP server on stdio.
func MCP() error {
	c, err := sandboxClient()
	if err != nil {
		return err
	}
	h := &gatewayHandler{c: c}
	srv := &mcp.Server{
		Name: "oriel-gw", Version: "1",
		Handler: h,
		Instructions: "Oriel's gateway: the only way out of this sandbox. oriel__* tools act on the host " +
			"(commit, push, open the PR, CI, add a project); the rest are the user's MCP servers. " +
			"A call may wait while the user approves it.",
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go h.watch(ctx, srv)
	return srv.Serve(ctx, os.Stdin, os.Stdout)
}

type gatewayHandler struct {
	c       *agentsock.Client
	mu      sync.Mutex
	version int
}

func (h *gatewayHandler) Tools(ctx context.Context) ([]mcp.Tool, error) {
	raw, v, err := h.c.GatewayTools(ctx)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.version = v
	h.mu.Unlock()
	out := make([]mcp.Tool, 0, len(raw))
	for _, r := range raw {
		var t mcp.Tool
		if json.Unmarshal(r, &t) == nil {
			out = append(out, t)
		}
	}
	return out, nil
}

func (h *gatewayHandler) Call(ctx context.Context, name string, args json.RawMessage) (mcp.CallResult, error) {
	raw, err := h.c.GatewayCall(ctx, name, args)
	if err != nil {
		return mcp.CallResult{}, err
	}
	var res mcp.CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return mcp.CallResult{}, err
	}
	return res, nil
}

// watch tells the agent when the tool list changes: a grant revoked, a
// server added.
func (h *gatewayHandler) watch(ctx context.Context, srv *mcp.Server) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		_, v, err := h.c.GatewayTools(ctx)
		if err != nil {
			continue
		}
		h.mu.Lock()
		changed := v != h.version
		h.version = v
		h.mu.Unlock()
		if changed {
			srv.NotifyToolsChanged()
		}
	}
}

// EgressProxy runs the sandbox's egress proxy, asking the session about
// each destination.
func EgressProxy(args []string) error {
	fs := flag.NewFlagSet("egress-proxy", flag.ContinueOnError)
	listen := fs.String("listen", ":3128", "address to listen on")
	relay := fs.String("relay", "", "serve the agent's session sockets in this directory, carried to the session's port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	sock, token := os.Getenv("ORIEL_SOCKET"), os.Getenv("ORIEL_TOKEN")
	dialSession := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}
	c := agentsock.NewSocket(sock, token)
	if *relay != "" {
		addr := egress.PortFileAddr(sandbox.HostGateway, os.Getenv("ORIEL_PORT_FILE"))
		dialSession = func(ctx context.Context) (net.Conn, error) {
			a, err := addr()
			if err != nil {
				return nil, err
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", a)
		}
		c = agentsock.NewDial(dialSession, token)
		for name, path := range map[string]string{"oriel.sock": "", "ssh-agent.sock": agentsock.SSHAgentPath, sandbox.HookSocket: agentsock.HookPath} {
			r := egress.Relay{Addr: addr, Token: token, UpgradePath: path}
			go func() {
				if err := r.Serve(ctx, filepath.Join(*relay, name)); err != nil {
					fmt.Fprintf(os.Stderr, "oriel egress proxy: relay %s: %v\n", name, err)
				}
			}()
		}
	} else if sock == "" {
		return errors.New("not in an oriel sandbox: ORIEL_SOCKET is not set")
	}
	p := NewEgressProxy(ctx, c, dialSession, token, parsePorts(os.Getenv(runner.HostPortsEnv)))
	fmt.Fprintf(os.Stderr, "oriel egress proxy on %s\n", *listen)
	return p.ListenAndServe(ctx, *listen)
}

// NewEgressProxy is the sandbox's egress proxy: it asks the session over c
// about each destination and dials through dialSession, except the host's
// own endpoints at hostPorts, which it dials itself.
func NewEgressProxy(ctx context.Context, c *agentsock.Client, dialSession func(context.Context) (net.Conn, error), token string, hostPorts []int) *egress.Proxy {
	logs := make(chan egress.Event, 256)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-logs:
				_ = c.EgressLog(ev)
			}
		}
	}()
	return &egress.Proxy{
		Decider: newSocketDecider(c),
		// The session dials: the host reaches networks the sandbox cannot.
		Dial: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return nil, err
			}
			// The host's own endpoints, which the session would refuse as
			// loopback, are dialled from here, at the ports the environment
			// names.
			if host == sandbox.HostGateway {
				if !slices.Contains(hostPorts, port) {
					return nil, fmt.Errorf("%s:%d is not an endpoint the sandbox was given", host, port)
				}
				var d net.Dialer
				return d.DialContext(ctx, "tcp", addr)
			}
			return egress.DialViaSession(ctx, dialSession, token, host, port)
		},
		Log: func(ev egress.Event) {
			// Also to stderr: the session may be what cannot be reached.
			if !ev.Allowed || ev.Err != "" {
				fmt.Fprintf(os.Stderr, "oriel egress proxy: %s %s:%d allowed=%t %s\n", ev.Method, ev.Host, ev.Port, ev.Allowed, ev.Err)
			}
			select {
			case logs <- ev:
			default:
			}
		},
	}
}

// socketDecider asks the session on the host, and keeps why it refused each destination for the refusal.
type socketDecider struct {
	c   *agentsock.Client
	mu  *sync.Mutex
	why map[string]string
}

func newSocketDecider(c *agentsock.Client) socketDecider {
	return socketDecider{c: c, mu: &sync.Mutex{}, why: map[string]string{}}
}

func (d socketDecider) Why(host string, port int) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.why[net.JoinHostPort(host, strconv.Itoa(port))]
}

func (d socketDecider) Decide(ctx context.Context, host string, port int) egress.Decision {
	ok, why, err := d.c.EgressDecide(ctx, host, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "oriel egress proxy: could not ask the session about %s:%d: %v\n", host, port, err)
		why = "the session couldn't be asked"
	}
	d.mu.Lock()
	if len(d.why) > 512 {
		clear(d.why)
	}
	d.why[net.JoinHostPort(host, strconv.Itoa(port))] = why
	d.mu.Unlock()
	if err != nil || !ok {
		return egress.Deny
	}
	return egress.Allow
}

// SSHProxy is ssh's ProxyCommand in a sandbox: a CONNECT tunnel through
// the egress proxy, so ssh to a git host is asked about like anything else.
// It is also where a sandbox can tell why ssh is about to fail: no key.
func SSHProxy(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: oriel ssh-proxy <host> <port>")
	}
	port, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("bad port %q", args[1])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if c, err := sandboxClient(); err == nil {
		if live, err := c.SSHStatus(ctx); err == nil && !live {
			fmt.Fprintf(os.Stderr, "oriel: ssh to %s runs without the user's key: no ssh-agent grant is open. "+
				"To push, use oriel__push. If this needs their key (a private repository or module), call oriel__ssh_agent, then retry.\n", args[0])
		}
	}
	var c net.Conn
	if proxy := os.Getenv("HTTPS_PROXY"); proxy != "" {
		c, err = egress.DialVia(ctx, proxy, args[0], port)
	} else {
		var d net.Dialer
		c, err = d.DialContext(ctx, "tcp", net.JoinHostPort(args[0], args[1]))
	}
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return pipe(c, os.Stdin, os.Stdout)
}

// pipe copies in to c and c to out until both directions are done,
// half-closing c's write side when in ends.
func pipe(c net.Conn, in io.Reader, out io.Writer) error {
	up := make(chan error, 1)
	go func() {
		_, err := io.Copy(c, in)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		up <- err
	}()
	_, err := io.Copy(out, c)
	if uerr := <-up; err == nil {
		err = uerr
	}
	return err
}

func parsePorts(s string) []int {
	var out []int
	for _, f := range strings.Split(s, ",") {
		if p, err := strconv.Atoi(strings.TrimSpace(f)); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// GitCredential is git's credential helper in a sandbox. It has nothing to
// give, and says so where git's own error would only ask for a username.
func GitCredential(args []string) error {
	if len(args) > 0 && args[0] == "get" {
		fmt.Fprintln(os.Stderr, "oriel sandbox: git has no credentials here. Push, pull and fetch with the oriel-gw gateway tools "+
			"(oriel__push, oriel__pull, oriel__fetch), which run on the host; for private dependencies over ssh, ask for oriel__ssh_agent.")
	}
	return nil
}

// Main runs the subcommand args[0] names, reporting false for one it does
// not know.
func Main(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "mcp":
		return true, MCP()
	case "egress-proxy":
		return true, EgressProxy(args[1:])
	case "ssh-proxy":
		return true, SSHProxy(args[1:])
	case "git-credential":
		return true, GitCredential(args[1:])
	}
	return false, nil
}
