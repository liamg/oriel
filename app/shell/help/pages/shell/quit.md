---
id: shell/quit
covers: [quit, quit.now]
related: [agent/console, agent/jobs, shell/start]
---

# Quitting

Sessions and their agents run inside Oriel, on this machine. Only one Oriel runs at a time: opening it again while
it's running says so and leaves the first one be.

## Quitting with agents running {#quit}

Quitting Oriel (its close button, the window manager's close, or the `quit` command) stops its agents where they are. They come back, each resuming its
conversation, when Oriel opens again.

With no agent mid-turn and no scheduled job, Oriel quits straight away. Otherwise a dialog says what's in the way:

- **Sessions mid-turn**: Oriel quits once their turns end, or after 60 seconds, cutting short a turn still going.
  **Quit now** (`⌘↵`) doesn't wait.
- **Scheduled jobs**: they don't run while Oriel is closed. One that comes due meanwhile runs once when Oriel opens
  again. With nothing mid-turn, Oriel waits for **Quit**.

**Cancel** (`Esc`) keeps Oriel open, with its agents as they were. Asking to quit again while the dialog waits quits
now.

Then Oriel stops its agents, all at once. Each takes its sandbox down, which can take a few seconds, so while they
stop the window says **Shutting down**, listing each agent as stopping or stopped. Nothing else takes keys meanwhile.

Once the window has closed, the process ends within 15 seconds whatever is left: background work the session host
still has running (a fetch, a sign-in nobody finished) gets 5 of them, and is logged by name if it hasn't ended.
Something still stuck after that is logged with every goroutine's stack, and Oriel exits anyway, saying so in the
terminal it was started from.

Sent `SIGTERM` or `SIGINT` (logging out, `kill`, `^C` in the terminal it was started from), Oriel quits the same
way, without waiting on scheduled jobs. A second signal quits now; a third exits without handing the agents over.

## After a crash {#crash}

If Oriel stops without quitting (it crashed, or the machine went down), its agents stop with it. When it opens
again, the sessions whose agents were running start them again, each resuming its conversation.
