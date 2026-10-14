// Package claude is the Claude agent.Provider: an interactive claude in a PTY, driven by Oriel's mod through
// function hooks, never `claude -p`. The mod hands every decision to the session over a unix socket through
// `oriel hook` (HookMain); messages and tool calls are read from Claude Code's own log (package transcript).
package claude
