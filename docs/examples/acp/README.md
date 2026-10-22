# Gemini CLI, as an ACP agent

A worked example: an agent that speaks the [Agent Client Protocol](https://agentclientprotocol.com) added to Oriel
in four lines of Lua. The bundled ACP bridge does the rest (`docs/agent-protocol.md`, "ACP agents"):
`app/plugintest/agent_test.go` loads this directory and runs a fake ACP agent the same way, so the example stays in
step with the API.

## Installing it

Install Gemini CLI and sign in once (`gemini` in a terminal), then add this directory to `[plugins]` in
`settings.toml`:

```toml
[plugins]
"example/gemini" = { path = "~/src/oriel/docs/examples/acp" }
```

Oriel shows what it asks for (to be an agent, and to run `gemini`) and installs it on your say. **Gemini** then
sits in the launcher's agents.

## Another ACP agent

Change `command` (goose: `{ "goose", "acp" }`; opencode: `{ "opencode", "acp" }`; Claude Code: `{ "claude-code-acp" }`),
the `exec` grant to match, and `models` to what it offers; the agent's own `model` option is set from the session's
model when the agent offers one of that id.
