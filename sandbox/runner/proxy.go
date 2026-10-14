package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
	"github.com/liamg/oriel/sandbox/egress"
)

// ensureProxy starts root's egress sidecar: the only container on both the
// internal network and the outside one, running `oriel egress-proxy`. Off
// Linux it also carries the agent's session sockets to the session's port.
// hostPorts are the only ports it may dial on the host.
func ensureProxy(ctx context.Context, root, image, orielBin string, id sandbox.Identity, hostPorts []int) error {
	net := NetworkName(root)
	if err := container.EnsureNetwork(ctx, net, true); err != nil {
		return err
	}
	run, err := runMounts(ctx, root, id, true)
	if err != nil {
		return err
	}
	ports := make([]string, len(hostPorts))
	for i, p := range hostPorts {
		ports[i] = strconv.Itoa(p)
	}
	env := []string{"ORIEL_SOCKET=" + sandbox.ContainerSocket, "ORIEL_TOKEN=" + mustToken(root), HostPortsEnv + "=" + strings.Join(ports, ",")}
	cmd := []string{"egress-proxy", "--listen", ":" + strconv.Itoa(ProxyPort)}
	if !sandbox.SocketsMount() {
		env = append(env, "ORIEL_PORT_FILE="+hostRunDir+"/port")
		cmd = append(cmd, "--relay", sandbox.ContainerRunDir)
	}
	spec := container.Spec{
		Name:    ProxyName(root),
		Image:   image,
		User:    fmt.Sprintf("%d:%d", id.UID, id.GID),
		Home:    "/tmp",
		Workdir: "/tmp",
		Mounts: append([]container.Mount{
			{Source: orielBin, Target: "/usr/local/bin/oriel", ReadOnly: true},
			{Source: filepath.Join(sandbox.Dir(root), "etc", "passwd"), Target: "/etc/passwd", ReadOnly: true},
		}, run...),
		Hosts:      []string{sandbox.HostGateway + ":host-gateway"},
		Env:        env,
		Labels:     map[string]string{"oriel.root": root, "oriel.role": "egress"},
		Entrypoint: "/usr/local/bin/oriel",
		Command:    cmd,
	}
	if err := container.PrepareMountpoints(spec.Mounts); err != nil {
		return err
	}
	if _, err := container.Ensure(ctx, spec); err != nil {
		return err
	}
	return container.Connect(ctx, net, spec.Name)
}

// HostPortsEnv tells the egress proxy which host ports it may dial.
const HostPortsEnv = "ORIEL_HOST_PORTS"

// HostPorts are the host ports the sandbox's environment points at.
func HostPorts(env map[string]string) []int {
	var out []int
	for _, r := range egress.EnvRules(env) {
		if r.Host == sandbox.HostGateway && !slices.Contains(out, r.Port) {
			out = append(out, r.Port)
		}
	}
	slices.Sort(out)
	return out
}
