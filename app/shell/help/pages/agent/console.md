---
id: agent/console
covers: [agent.console]
related: [agent/asks, terminal, layout/panes]
---

# Agent console

The console shows what the agent of the session in front is doing, as it does it. It docks at the bottom (`⌘⇧A`, or
the rail's `❯`) and moves like any pane (`⌃W M`).

## Header

The provider, model and effort, a chip for what the agent is doing (working, wants permission, needs you, idle), and
the ticket, session and branch.

## Working {#working}

While a turn runs, the end of the transcript has a working line: three dots in the working colour with a wave
crossing them, who's working and for how long ("Claude is working · 0:42"). The thread it's on shows the same line
under its last comment, in the Request view and between the editor's lines. With reduced motion the dots hold still.

## In the background {#background}

When the agent's turn ends with work still running that will wake it again (a shell left running, a background
subagent, a monitor), the header says **working in the background** and the end of the transcript lists each, by
kind and what it's for. The threads that turn was handling stay busy and the session working until a turn ends with
nothing left running.

## Running {#running}

What the agent's last turn left running that won't wake it (a dev server, a watcher: Codex's background terminals)
is listed under **RUNNING** at the end of the transcript: each command, with its pid, CPU and memory when the
provider says them. It doesn't keep the session working. **Stop** ends one, when the agent's provider can stop them
(`agent.process.stop`). The list is what the last turn's end found running; it's cleared when the agent stops.

## Fork

Asks the agent a side question ("btw"), answered from its context by a fork that has no tools and doesn't join the
conversation. The answer shows at the end of the transcript.

## Interrupt

Ends the agent's turn (`Esc` with the transcript focused). With the agent idle, `Esc` closes the console.

## Starting {#starting}

While the agent starts, a card with a spinner says what its start is doing and for how long: "Building the sandbox
image", "Installing go@1.26 in the sandbox". A step that only happens once says so; later sessions reuse what it
made and start in seconds. Your request is queued meanwhile and goes to the agent once it's up. If the start fails,
the session says why.

## Raw terminal

Opens the agent's own terminal as a terminal tab: what the CLI shows, and what you type
goes to it. While the agent is still starting there is no terminal yet, and the tab says so. An agent that has no
terminal of its own (Codex runs as a server) has no **Raw terminal** button.

## Transcript

Each block is one thing the agent did: its prose, the prompt it was given, a tool call, an error. `j` and `k` move
between blocks, `⏎` folds or unfolds the selected one, and a click on a block's head does the same. The request the
turn works on shows above it, with its file and line.

## Tool calls

A dot says how the call went: cyan running, green done, red failed. At the right: lines read, hits found, how long
it took. An edit shows its lines as a small diff; **view in diff** opens the file. A shell command's output is
folded until you open it.

## Thinking

The model's reasoning, dimmed and folded.

## Composer

`i` writes to the agent. `⏎` sends; while it works, the message waits as **QUEUED** and goes when the turn ends.
`⌘⏎` sends now, interrupting the turn. Name a file with `@path`. The chips are the session's model, effort and gate.

## Session stats {#stats}

What the session has used: its context window, cache hits, turns and tool calls, tokens, an estimate of the cost, and
how long its turns ran. Then each provider's plan windows, this session's first, and the files this turn touched.

## Rules in force {#rules}

What lets the agent through without asking: its own gate (edits automatic, shell asks), the sandbox's network and
gateway, and every rule "Always in this workspace" made.

{{keys "agent.*"}}
