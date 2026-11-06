# The Oriel agent protocol

A plugin brings an agent to Oriel (another CLI, a local model, anything that can hold a conversation and use
tools) by serving this protocol from a process. The agent then sits in the launcher beside Claude and Codex, and
sessions run on it with nothing lost: streamed messages, tool calls with diffs, permissions and questions, its
terminal, resume, fork, usage, sign-in, the sandbox. Claude and Codex are themselves `agent.Provider`s (`agent/agent.go`),
and the protocol carries that interface whole, both ways, so any provider can be served over it
(`agent/rpcagent`), and a client of it is a provider like any other.

An agent that already speaks the [Agent Client Protocol](https://agentclientprotocol.com) needs no process of its
own: the bundled ACP bridge serves it (see [ACP agents](#acp-agents)).

## Registering

From the plugin's `init.lua` ([api.md](api.md), `oriel.agent.provider`):

```lua
oriel.agent.provider{
  name = "mine", title = "My agent", mark = "MA", order = 30,
  program = "bin/my-agent", args = { "--serve" }, env = { MY_AGENT_MODE = "oriel" },
  features = { terminal = false, fork = true }, -- what it's taken to do until its process has said
}
```

The plugin needs the `agent.provider` capability. A provider named as a built-in (`claude`, `codex`) takes its
place. The process starts on first use (the launcher asking `detect`), runs in the plugin's OS sandbox like any
process plugin, and starts again after it stops; a session it was running exits.

## Transport

JSON-RPC 2.0 over the process's stdin and stdout, one message a line (`"jsonrpc": "2.0"` on each). Either side may
call the other. A request whose caller stops waiting is cancelled with `$/cancelRequest` `{id}`. Binary data
(terminal and process output) is base64. Errors past JSON-RPC's own:

| Code | Means | Data |
|---|---|---|
| -32021 | the session has nothing to fork from yet (`agent.ErrNoFork`) | |
| -32022 | the agent's login has stopped working | an `authExpired` [event](#events) |
| -32023 | a usage probe read nothing | `{provider, started}` |
| -32024 | a process can't be run here: no such program, or the plugin isn't granted it | |

## Oriel to the plugin

| Method | Params | Result |
|---|---|---|
| `initialize` | `{protocolVersion: 1, name, config}`: Oriel's name for the provider, and a bridge's config | `{protocolVersion: 1, id, features: {terminal, fork, images, sandbox, login, notes, stopProcesses}, logged, discovered, mcp}` |
| `detect` | `{}` | `{bin, version, loggedIn, login, account, note}`; `login` marks the sign-in, changing when it's renewed |
| `models` | `{}` | `[{id, name, efforts, context, default, small}]` |
| `usage` | `{}` | `{account, accountName, plan, tier, windows: [{name, used, resets, length}], credits, sampled, source}` |
| `session/start` | `{session, resume, spec}`: see [Sessions](#sessions) | `{id, terminal}` |
| `session/send` | `{session, input: {text, images, urgent}}` | `{}` |
| `session/interrupt` | `{session}` | `{}` |
| `session/fork` | `{session, prompt}` | `{answer}`, or -32021 |
| `session/answer` | `{session, id, decision: {allow, always, note, answers}}` | `{}` |
| `session/stopProcess` | `{session, process}`: stop a process a turn left `running`, by its `id`, when `initialize` said `stopProcesses` | `{}` |
| `session/close` | `{session}` | `{}`, once its last events are sent |
| `terminal/write` | `{session, data}` | `{}` |
| `terminal/resize` | `{session, cols, rows}` | `{}` |
| `login/start` | `{login}` | `{url}`: where the user signs in |
| `sandbox/needs` | `{call, root, cwd, dir, home, tmp, os, arch, shared, uid, stateDir, environ, cache}` | `{bins, tools, mounts: [{host, sandbox, readOnly}], env, passEnv, skipEnv, urlEnv, urls, hosts, problems: [{code, message, fix, blocking, signIn}]}` (`signIn`: a blocking problem that signing in on the host fixes, as the `hostCommand` that does) |
| `sandbox/mcpServers` | `{}` | `[{name, source, config, vars}]`: the user's MCP servers for it, in Claude's `mcpServers` form |
| `logs` | `{cwd, session}` | `[{path, format}]`, when `initialize` said `logged` |
| `discover` | `{}` | `[{session, logs, modified}]`, when it said `discovered` |

Notifications: `session/stop` `{session}` (the context the session was started in has ended: stop it as you would a
process), `login/cancel` `{login}`, and a process's output and end (`exec/output`, `exec/exit`, below).

## The plugin to Oriel

| Method | Params | Result |
|---|---|---|
| `session/tool` | `{session, name, arguments}`: call one of the session's tools | `{output}`; the tool's error as the error |
| `exec/start` | `{session, process, path, args, dir, env, stderr, pty: {cols, rows}}`: run a process | `{}` |
| `exec/write` | `{process, data}` | `{}` |
| `exec/eof` | `{process}`: close its stdin | `{}` |
| `exec/resize` | `{process, cols, rows}` | `{}` |
| `exec/kill` | `{process}` | `{}` |
| `sandbox/env` | `{call, name}`: a host variable as the sandbox passes it on, during `sandbox/needs` | `{value}` |

Notifications: `session/event` `{session, event}`, `session/closed` `{session}` (its events have ended),
`terminal/output` `{session, data}`, `login/done` `{login, error}`, `sandbox/report` `{call, step, note}`.

## Sessions

`session/start`'s `spec` is everything a session starts with: `cwd`, `model`, `effort`, `pack` (`{name, skill, text,
tools}`: the session kind's instructions), `prompt` (the first thing to say), `tools` (`[{name, description,
inputSchema, readOnly}]`), `policy` (`ask`, `light`, `auto` or `bypass`: how much the agent's own gate asks; `auto` is the agent's own judgement, light where it has none), `protect`
(directories it must never edit), `env`, `sandbox` (`{name, hookSocket, gatewayMCP, gateway}`), `stateDir`,
`memory` and `noMCP`. `session` is Oriel's handle for it, which every message about it carries, from before
`session/start` answers; `id` in the answer is the agent's own, which a later `resume` names.

**Processes.** Run the agent's processes with `exec/start`, never yourself: Oriel runs them through the session's
own exec, so when the session has a sandbox they run in it, exactly as Claude's and Codex's do. `process` is your
handle for one. With `pty` it's a terminal and its output comes as `exec/output` stream `pty`; without, it has pipes,
`stdout` comes as stream `stdout`, and with `stderr: true` its stderr as stream `stderr`. `exec/exit` `{process, error,
code}` follows its last output. Outside a sandbox each program needs the plugin's `exec` grant.

**Tools.** Call one of `spec.tools` with `session/tool`. Or, when `initialize` said `mcp`, Oriel serves them as an
MCP stdio server for your agent to connect to: `spec.mcp` is `{name, command, args, env, socket}`, ready to put in
the agent's MCP config, and every process Oriel runs for the session has `ORIEL_HOOK_SOCK` set to `socket`, which is
how a sandbox carries it in. A sandboxed session's gateway (the user's MCP servers) is `spec.sandbox.gateway`, the
contents of an MCP config file.

**Terminal.** A session with a terminal the user may watch and type in answers `terminal: true`, sends what it
shows as `terminal/output` (everything it has shown first), and takes `terminal/write` and `terminal/resize`.

## Events

A `session/event`'s `event` is one of these, by `type`, with only the fields that are set:

| `type` | Fields |
|---|---|
| `ready` | `id`, `model` |
| `turnStart` | |
| `message` | `id`, `role` (`assistant`, `user`, `thinking`), `text` (the whole message so far), `partial` (while it streams), `agent` (a subagent's id) |
| `toolCall` | `id`, `tool` (`Bash`, `Edit`, `mcp__oriel__…`), `input`, `agent` |
| `toolResult` | `id`, `output`, `isError`, `diff` (`{path, added, removed, lines: [{op, old, new, text}]}`) |
| `permission` | `id`, `tool`, `summary`, `input`, `rule` (what Always would allow): answered with `session/answer` |
| `question` | `id`, `questions: [{header, question, options, multi}]`: answered with `session/answer`'s `answers`, by question |
| `subagent` | `id`, `description`, `kind`, `prompt`, `done`, `result` |
| `turnEnd` | `usage: {input, output, cacheRead, cacheWrite, context, contextMax, cost, durationMs}`, `background` (what the turn left running that will wake the agent: Oriel holds the turn's work open until it's done) and `running` (what it left running that won't: processes the user may stop), each `[{id, kind, description, pid, cpu, memory}]`; `cpu` is percent of a core, `memory` resident bytes |
| `failure` | `error`, `retryable` |
| `usage` | `plan`: the plan's windows, as `usage` answers |
| `authExpired` | `provider`, `account`, `detail`, `terminal` (typed into the terminal, signs in again: `/login`), `shell` (run in a shell, signs in again), `hostCommand` (argv run on the host in a terminal, signs it and its sandboxes in again: `["codex", "login"]`) |
| `authRestored` | `provider`, `account` |
| `exited` | `error` |

## Writing one in Go

`rpcagent.Serve(os.Stdin, os.Stdout, factory)` serves any `agent.Provider`: its sessions get a `StartSpec` whose
`Exec` and `Tools` call back to Oriel, so a provider written for Oriel's own use runs unchanged in a plugin. The
conformance suite (`agent/agenttest`) holds a provider served this way to the same scenarios as Claude and Codex.

## ACP agents

The bundled ACP bridge (`integ/acp`) registers the bridge `acp`. A provider whose spec has a table under its name is
served by the bridge, which runs the agent through the session's exec and is its ACP client:

```lua
oriel.agent.provider{ name = "gemini", title = "Gemini", mark = "G",
  acp = { command = { "gemini", "--experimental-acp" }, login = "gemini",
          models = { { id = "gemini-2.5-pro", name = "Gemini 2.5 Pro", default = true } } } }
```

with the plugin's `exec` grant for `gemini`. What ACP gives, Oriel gets: streamed messages and thinking, tool calls
and their diffs, plans (as calls of `plan`), permissions (Always is `allow_always`), form elicitations as questions,
the workspace's files through `fs/*` (never outside it, nor under a protected directory), terminals in a sandbox,
images when the agent takes them, `session/load` or `session/resume` to resume, and the agent's `model` and
`thought_level` options set from the session's model and effort. ACP has no side questions, so an ACP agent can't
fork. `docs/examples/acp` is a plugin that adds Gemini CLI.

A bridge of your own is `oriel.agent.bridge{ name, program }`: its process gets each provider's table in
`initialize`'s `config`.
