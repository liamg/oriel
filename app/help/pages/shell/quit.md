---
id: agent/worker
covers: [worker.hand-over-now]
related: [agent/console, agent/jobs, shell/start]
---

# The worker

Sessions and their agents run in the worker (`oriel worker`), a process of its own on this machine. The window
starts it, and stops it as Oriel quits.

## Quitting {#quit}

Quitting Oriel (its close button, or Quit) stops the worker too. Its agents stop where they are, and come back,
each resuming its conversation, when Oriel opens again.

With no agent mid-turn and no scheduled job, Oriel quits straight away. Otherwise a dialog says what's in the way:

- **Sessions mid-turn**: Oriel quits once their turns end, or after 60 seconds, cutting short a turn still going.
  **Quit now** (`⌘↵`) doesn't wait.
- **Scheduled jobs**: they don't run while Oriel is closed. One that comes due meanwhile runs once when Oriel opens
  again. With nothing mid-turn, Oriel waits for **Quit**.

**Cancel** (`Esc`) keeps Oriel open, with the worker as it was. Asking to quit again while the dialog waits quits
now. Workers on other machines carry on.

If Oriel crashes, the worker keeps running, and the next Oriel connects to it.

## Restarting for a new build {#restart}

When Oriel is rebuilt, the worker still running is from the earlier build. The window restarts it: the old worker
stops its agents and quits, and this build's worker starts and brings them back, each resuming its conversation.

With no agent mid-turn this happens as the window opens. Otherwise a dialog names the sessions mid-turn and counts
down: the worker restarts once their turns end, or after 60 seconds, cutting short a turn still going.
**Restart now** (`⌘↵`) doesn't wait. **Hide** (`Esc`) closes the dialog; the restart still happens.
