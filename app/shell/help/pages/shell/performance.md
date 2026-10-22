---
id: shell/performance
covers: [perf.open, perf.close, app.overlay.perf]
related: [shell/hud, agent/stats]
---

# Performance

Click the status line's **CPU** for the Performance page: what Oriel is costing, and what it has started. It
samples every 2 seconds while it's up, and nothing while it's closed. Escape or **Close** puts it away.

## Oriel {#oriel}

- **CPU**: Oriel's own process, as a share of one core, and of the whole machine.
- **Memory**: what's resident, with the Go heap's live objects and all the Go runtime holds.
- **Goroutines**, and how many garbage collections have run.
- **Frames**: how many the window drew a second over the last 5 seconds, and how long one took to build (p50, p99).
  An idle window draws none; what asks for the most frames (a spinner, a progress bar) is named by its keys.
- **Pointer lag**: how far behind the pointer the frames were, from a move to a frame built with it.

## This machine {#machine}

The machine's CPU across all cores (the status line's figure: it counts everything running, not just Oriel), its
memory, and the space free on the disk the window's directory is on, read every 5 minutes.

## Sessions and processes {#processes}

**Processes** lists Oriel and every process under it, heaviest first: agents, language servers, terminals, git. CPU
is a share of one core since the sample before. A process an agent started carries its session, and **Sessions** adds
up each session's. An agent in a container isn't Oriel's child, so it doesn't show.

## Watches {#watches}

What Oriel keeps up to date in the background, and who for. Each session has a freshness manager, and the app one of
its own; a view, a panel or a plugin subscribes to a source (a work tree's git status, a pull request's checks,
files) and the manager watches or polls it only while something does. **Watches** lists each source: its manager, its
state (*watching* files, *polling*, *paused* behind another app, *reading*, or *idle* as it's let go), who subscribes
(a source read from another counts as one), how many times it was read in the last minute, when it was last
read, and why: the file that changed, the source it reads from, a *refresh* or a *poll*. An idle window reads nothing: a source that reads every few seconds with nothing changing is a bug.
