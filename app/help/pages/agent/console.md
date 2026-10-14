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

## Fork

Asks the agent a side question ("btw"), answered from its context by a fork that has no tools and doesn't join the
conversation. The answer shows at the end of the transcript.

## Interrupt

Ends the agent's turn (`Esc` with the transcript focused). With the agent idle, `Esc` closes the console.

## Raw terminal

Opens the agent's own terminal, streamed from its worker, as a terminal tab: what the CLI shows, and what you type
goes to it.

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

## Tally

What the session has used: its context window, cache hits, turns and tool calls, tokens, an estimate of the cost, and
how long its turns ran. Then each provider's plan windows, this session's first, and the files this turn touched.

## Rules in force {#rules}

What lets the agent through without asking: its own gate (edits automatic, shell asks), the sandbox's network and
gateway, and every rule "Always in this workspace" made.

{{keys "agent.*"}}
