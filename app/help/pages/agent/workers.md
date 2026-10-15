---
id: agent/workers
covers: [workers.open, workers.add, workers.rename, workers.revoke, workers.forget, workers.restart, app.overlay.workers, app.overlay.workers.dialog]
related: [agent/worker, agent/jobs, agent/stats]
---

# Workers

Sessions and their agents run in workers, one per machine: this machine's, which Oriel starts, and workers on other
machines, which dial Oriel over TLS.

## The Workers page {#page}

`:Workers`, or **Workers** in the sidebar or the omnibox's Pages: every worker on the left, the selected one on the
right. The header counts the workers online and offline, and says whether Oriel listens for workers elsewhere, with
its certificate's fingerprint as words. `esc` or **Close** closes it.

## The list {#list}

This machine's worker first, then the others by name, each online (and for how long), offline since when, never
connected, or revoked. `j`/`k` move. The foot says where the keys live: `workers.json` in your config directory, each
key kept only as its hash, readable by you alone.

## A worker {#worker}

Its host, OS and architecture, version and build, and its cards: **LOAD** (the machine's CPU and memory),
**AGENTS** (each provider, its account and plan, or signed out and why), **SESSIONS** (its sessions and the agents
running) and **CONNECTION** (how it reaches Oriel, when it was added, where it last came from, and any recent
refusals). **Rename** (`r`) gives it a name of your own in Oriel, wherever a worker is named; the worker keeps its
own.

## Add a worker {#add}

`a` on the page. Give it a name; if Oriel isn't listening for workers yet, say where to listen (`:7437` by default),
which is saved as `workers.listen`. **Add** (`⌘↵`) makes a key and shows the command to run on the other machine,
with **Copy**:

```
oriel worker --connect host:7437 --fingerprint … --key … --name devbox
```

The key is shown this once. The worker checks Oriel's certificate against the fingerprint, and prints it as words as
it starts: they should read as the dialog's. The dialog waits, and says when the worker connects.

## Revoke {#revoke}

Asks once more, then the worker's key goes: it's cut off, and refused if it dials again. It stays listed as revoked
until you forget it.

## Forget {#forget}

For a worker that isn't connected: asks once more, then it leaves the list, with its key.

## Restart {#restart}

This machine's worker stops its agents and quits, and a new one starts and brings them back, each resuming its
conversation. With an agent mid-turn it waits for the turn, as a restart for a new build does.
