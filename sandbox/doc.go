// Package sandbox is Oriel's host-side account of sandboxed workspaces: which
// workspace runs in a container, the files that describe it (all under
// ~/.oriel/sandbox/<key>/, out of the container's reach), and the checks the
// host makes before it trusts anything the container can write.
//
// The subpackages are the rest of the sandbox: container (the docker CLI),
// runner (an agent's container, its mounts, toolchains and egress sidecar),
// egress (the proxy and the host's network policy), gateway (Oriel's host
// tools and the user's MCP servers behind allow/ask/deny rules), mcp,
// agentsock (the session socket) and agentcmd (what runs in the container).
//
// # Wiring, for the worker
//
// Per sandboxed workspace root:
//
//   - Create(root, ModeSandbox) and SaveManifest(root, ...) with its projects.
//     Host git in the workspace runs through Git or GitEnv.
//   - An egress.Session: GrantsPath EgressGrantsPath(root); Settings the
//     projects' and user's EgressRules (with LoadAlways().Egress); Env
//     egress.EnvRules(EndpointEnv(os.Environ(), cfg.Env, ClaudeSettings()));
//     Always AddAlwaysEgress; Approver raises the network approval card and
//     returns its answer (the connection is held meanwhile, up to Wait, 60s).
//   - A gateway.Gateway: Builtins gateway.Builtins(host), where host is the
//     worker's gateway.Host (commit, push, pull, fetch, PR, gh, repos,
//     add_project, run on host, ssh_agent, request_host, and Approve, which
//     raises the gateway approval card); Up the user's servers
//     (gateway.Discover); Levels from GrantsPath(root), cfg.Projects[repo].Rules,
//     cfg.Rules and LoadAlways().Rules; Grant, Always (AddAlwaysRules) and Log.
//     Approve on the Gateway is host.Approve; Timeout is cfg.ApprovalSecs.
//   - An agentsock.SSHAgent, served at RunDir(root)/ssh-agent.sock; the
//     host's SSHAgent method is its Grant.
//   - agentsock.Serve(ctx, &agentsock.Handler{...}, SocketPath(root), portFile)
//     with Token from Load(root).AgentToken; portFile is PortPath(root) off
//     Linux (SocketsMount false), "" on Linux.
//   - runner.Launch(ctx, root, cwd, argv, Options{Config: cfg, ...}) returns
//     the docker command to run under the agent's PTY; the agent's MCP config
//     names the gateway as {"oriel-gw":{"command":"oriel","args":["mcp"]}}.
//   - The container's /usr/local/bin/oriel is runner.OrielBinary: on Linux
//     the running binary, so cmd/oriel must hand "mcp", "egress-proxy",
//     "ssh-proxy" and "git-credential" to agentcmd.Main; elsewhere a Linux
//     build of cmd/oriel-sandbox.
//   - runner.Sweep and runner.Health run on timers.
package sandbox
