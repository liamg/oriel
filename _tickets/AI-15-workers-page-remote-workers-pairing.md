---
id: AI-15
title: "Workers page: the workers (this machine's and remote ones), pairing a new one, revoke, rename, forget, restart"
phase: P6 follow-up
status: dropped
area: Sessions and AI
module: app, core, wire, worker, cmd/oriel
depends_on: [AI-1, AI-12, SE-5]
---

# AI-15 · Workers page and remote workers

**Dropped:** one Oriel per machine runs its own sessions in process; another machine runs its own Oriel and
the two never talk. Workers, `wire`, pairing and the Workers page were removed.

**Phase** 6 · Review (follow-up) · **After** [AI-1](AI-1-worker-wire-sessions-workspaces-with-multi-repo-.md), [AI-12](AI-12-stats-and-usage-pages-grouped-by-repo.md), [SE-5](SE-5-jobs-and-login-follow-ups.md) · **Unblocks** nothing yet

A UI to manage the workers, this machine's and remote ones, and to add new ones. The worker side of a remote worker
exists (`oriel worker --connect host:port --fingerprint … --key … --name …`, `worker.ServeApp`), and `wire` has the
pinned-TLS handshake (`wire.Accept`, `ServerTLS`, `KeyAuth`, `LoadOrCreateCert`, `Words`), but the app never listens,
so no remote worker can connect, and nothing shows the workers or adds one.

## Design

- `_design/ui.md` 45 (Workers; no mockup: it follows 43 and 44), 01h (the sidebar's global group), `_design/sessions.md`
  "As built (P5)", `_design/architecture.md` (wire, workers), `_design/security.md` ("Remote workers"),
  `_design/settings.md` (`workers.listen`), `_design/crit-reference.md` (crit's manager is the listener; workers dial
  it over pinned TLS with yamux).

## What it is

- **The app listens** for remote workers while `workers.listen` is set (an address, `:7437`; empty, the default, is
  off): TLS 1.3 with a persistent self-signed certificate kept in the app's state directory (owner-only), and the
  pinned handshake, letting in a worker whose name and key are in the keys file (`workers.json` in the config
  directory, owner-only, each key stored as its SHA-256). A worker that's let in joins the store the way this
  machine's does (the same `sessions` path), and may only speak for itself: events about another worker or another
  worker's sessions are dropped. A worker that goes away shows offline; one that comes back reconnects; a revoked
  key is refused (`wire.ErrRejected`), so `oriel worker --connect` stops.
- **The Workers page** (`:Workers {name}`, `workers.open {name}`, the sidebar's global **Workers** with "2 online", the
  omnibox's Pages; key mode `workers`): the list (this machine's first, then by name: online or offline and since,
  host, OS and arch), the selected worker's detail (version and build, load from `sysload`, the agents and their
  logins or signed-out state, its sessions and agents running, how it connects), and the actions.
- **Add a worker** (`a`, `workers.add {name, listen}`): a dialog that takes a name, makes a key, starts listening (if
  `workers.listen` is empty it asks for an address, `:7437` by default, and saves it), then shows the command to run
  on the other machine with **Copy**, the certificate's fingerprint as words to check against what the worker prints,
  and waits for the worker, saying when it connects.
- **Revoke** (`workers.revoke {name}`, asks once): the key goes, the worker is cut off, and it's refused after.
- **Rename** (`r`, `workers.rename {name, to}`): the app's name for a worker (`core.WorkerState.Label`), everywhere a
  worker is named.
- **Forget** (`workers.forget {name}`, an offline worker, asks once): it leaves the list and its key goes.
- **Restart** (`workers.restart`): this machine's worker, through the drain that a rebuild uses (sessions.md).

## Done when

- A real worker (`worker.Worker` running `ServeApp`) over TLS to the app's listener on `127.0.0.1:0`, with the key
  from Add a worker, shows online on the page and in the store, and its sessions arrive; the dialog says it connected.
- A wrong key and a revoked key are refused with `wire.ErrRejected`; a wrong fingerprint is refused by the worker; a
  worker claiming another's name is cut off; revoking cuts an online worker off.
- A worker that goes away shows offline with its time, and shows online again when it reconnects; Forget removes an
  offline one; Rename renames it in the store and on the page.
- The keys file and certificate are owner-only and the file holds no key in the clear.
- The page passes the help lint; the sidebar entry and the omnibox's Pages show the figure; Restart drains this
  machine's worker.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
