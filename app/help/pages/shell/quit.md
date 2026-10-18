---
id: shell/quit
covers: [quit.now]
related: [agent/console, agent/jobs, shell/start]
---

# Quitting

Sessions and their agents run inside Oriel, on this machine. Only one Oriel runs at a time: opening it again while
it's running says so and leaves the first one be.

## Quitting with agents running {#quit}

Quitting Oriel (its close button, or Quit) stops its agents where they are. They come back, each resuming its
conversation, when Oriel opens again.

With no agent mid-turn and no scheduled job, Oriel quits straight away. Otherwise a dialog says what's in the way:

- **Sessions mid-turn**: Oriel quits once their turns end, or after 60 seconds, cutting short a turn still going.
  **Quit now** (`⌘↵`) doesn't wait.
- **Scheduled jobs**: they don't run while Oriel is closed. One that comes due meanwhile runs once when Oriel opens
  again. With nothing mid-turn, Oriel waits for **Quit**.

**Cancel** (`Esc`) keeps Oriel open, with its agents as they were. Asking to quit again while the dialog waits quits
now.

## After a crash {#crash}

If Oriel stops without quitting (it crashed, or the machine went down), its agents stop with it. When it opens
again, the sessions whose agents were running start them again, each resuming its conversation.
