---
id: shell/update
covers: [UpdateCheck, update.check, update.notes, update.install, update.restart, app.overlay.update, hud.indicator.update]
related: [shell/settings, agent/workers]
---

# Updates

Oriel keeps itself and its workers up to date. Each release is signed by Oriel's release key, and anything that
doesn't verify is refused, whether it came from GitHub or over a worker's connection.

## Checking {#check}

Oriel asks GitHub for a newer release on its channel shortly after it starts, and every hour after that.
`:UpdateCheck` asks now and opens the update card with what it finds. **Check again** on the card asks again.

{{options "updates"}}

- `updates.mode`: **ask** (the default) offers a new release in a notice, with **What's new** and **Update**;
  **automatic** installs it as it comes out; **manual** only checks when you run `:UpdateCheck`.
- `updates.channel`: **stable**, or **nightly** for pre-releases too.

## The update card {#card}

The newer release, the version you have, and its release notes. **Later** (`esc`) closes it. **Update** installs it,
and **Restart** appears once it's installed.

## Installing {#install}

**Update** sends the release to every connected worker that's behind it first. Each one checks the release's
signature, runs the new build, then drains: its agents finish their turns and come back on the new build. Then Oriel
installs its own copy next to the one that's running, keeping the old one. While this happens the status line says
**updating …**. Workers never need GitHub: Oriel streams each one the build for its platform. A worker that connects
later while it's behind is updated as it connects, unless updates are manual.

## Restarting {#ready}

Once the update is installed, the status line shows **update ready · restart**. Click it, or **Restart** on the
card or in the notice, and Oriel quits (this machine's worker drains first) and opens again on the new version. If
the new version doesn't start properly, Oriel goes back to the previous one by itself, and so does a worker.

From a terminal, `oriel update` checks and installs without the app: `--check` only says whether there's a newer
release, and `--channel nightly` picks the channel.
