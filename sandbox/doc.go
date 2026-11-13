// Package sandbox is Oriel's host-side account of sandboxed workspaces: which
// workspace runs sandboxed, the files that describe it (all under
// ~/.oriel/sandbox/<key>/, out of the sandbox's reach), the checks the host
// makes before it trusts anything the sandbox can write, and the Backend that
// fences the agent in.
//
// The subpackages are the rest of the sandbox: the backends runner (docker:
// an agent's container, its mounts, toolchains and egress sidecar; on top of
// container, the docker CLI) and seatbelt (macOS's sandbox-exec), egress (the proxy and the host's network policy), gateway (Oriel's host
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
//     egress.EnvRules(EndpointEnv(os.Environ(), cfg.Env, needs)); Agent
//     egress.HostRules(needs.Hosts), where needs are what the session's
//     provider said its agent needs (agent.Sandboxed, through Needs);
//     Always AddAlwaysEgress; SaveFailed tells the user an allow was not
//     kept; Approver raises the network approval card and returns its
//     answer (the connection is held meanwhile, up to Wait, 60s).
//   - A gateway.Gateway: Builtins gateway.Builtins(host), where host is the
//     worker's gateway.Host (commit, push, pull, fetch, PR, gh, repos,
//     add_project, run on host, ssh_agent, request_host, and Approve, which
//     raises the gateway approval card); Up the user's servers
//     (gateway.Discover over the provider's MCPServers); Levels from
//     GrantsPath(root), cfg.Projects[repo].Rules, cfg.Rules and
//     LoadAlways().Rules; Grant, Always (AddAlwaysRules) and Log.
//     Approve on the Gateway is host.Approve; Timeout is cfg.ApprovalSecs.
//   - An agentsock.SSHAgent, served at RunDir(root)/ssh-agent.sock; the
//     host's SSHAgent method is its Grant.
//   - The Backend is BackendFor(cfg).
//   - agentsock.Serve(ctx, &agentsock.Handler{...}, SocketPath(root), portFile)
//     with Token from Load(root).AgentToken; portFile is PortPath(root) when
//     the backend ServesPort, else "".
//   - Backend.Launch(ctx, root, cwd, argv, LaunchOpts{Config: cfg,
//     Provider, Agent, ...}) asks the provider what its agent needs at the
//     place the backend puts it (Needs: its binaries, mounts, env, hosts), and
//     returns the command to run under the agent's PTY with those needs; the
//     agent's MCP config names the gateway as
//     {"oriel-gw":{"command":"oriel","args":["mcp"]}}.
//   - The sandbox's oriel answers "mcp", "ssh-proxy" and "git-credential"
//     (and, in docker's sidecar, "egress-proxy") through agentcmd.Main, and
//     the agents' "hook" (Claude's mod) through hookcall.Main, and "hook mcp"
//     (Oriel's tools as an MCP server) through mcpstdio.Main. In docker it is
//     runner.OrielBinary: cmd/oriel-sandbox built static for Linux, which the
//     app embeds (package helper, `make helper`) and the host's sandbox
//     factory hands to the backend (HelperRunner.WithHelper). Under seatbelt
//     it is the running binary.
//   - Backend.Sweep, then Sweep, run hourly (host.SweepSandboxes).
package sandbox
