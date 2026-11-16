---
id: agent/plugins
related: [agent/launcher, plugins/install, plugins/lua]
---

# Adding agents with plugins

Claude and Codex are plugins too: any plugin can add an agent beside them, and one named `claude` or `codex` takes
the built-in's place. The agent then sits in the launcher's **Agent** list with its models, and sessions run on it
as on any other: its replies stream, its tool calls and edits show in the console, its permission prompts and
questions come to you, and it resumes where it left off.

## An ACP agent

An agent that speaks the Agent Client Protocol (Gemini CLI, goose, opencode, claude-code-acp…) needs only Lua. In a
plugin's `init.lua`:

```lua
oriel.agent.provider{ name = "gemini", title = "Gemini", mark = "G",
  acp = { command = { "gemini", "--experimental-acp" }, login = "gemini" } }
```

and in its `oriel.toml`:

```toml
[permissions]
"agent.provider" = { why = "Gemini CLI as an agent sessions run" }
exec             = { commands = ["gemini"], why = "Run Gemini CLI" }
```

The bundled **ACP bridge** plugin serves it. The agent gets Oriel's tools, and in a sandboxed session it runs in the
sandbox with the gateway's MCP servers: macOS's sandbox runs your installed program; a container installs what `tools`
names with mise, once for each version, as it does a project's toolchains (`{ "node@22",
"npm:@google/gemini-cli@0.9.0" }`), else runs the image's. Say what else it needs there in `acp`: `hosts` it reaches,
`pass_env` (the variables it inherits, `{ "GEMINI_*" }`) and `home` (where in your home it keeps its sign-in,
`{ ".gemini" }`, which it sees at the sandbox's home, read-write). It reads and writes files only in the session's workspace, never in your own
checkouts. An ACP agent can't answer side questions (fork). `docs/examples/acp` is this plugin, ready to install.

## Any other agent

A plugin's process can serve an agent of its own over the Oriel agent protocol (`docs/agent-protocol.md`), with
`program = 'bin/my-agent'` in place of `acp`. It runs in the plugin's OS sandbox, and the agent's processes run where
the session's run.

## What it may do

Adding an agent needs the plugin's `agent.provider` grant, which install marks **critical**: the agent is given your
prompts and acts in your workspaces. Outside a sandbox, each program it runs needs the plugin's `exec` grant too.
