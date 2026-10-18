---
id: shell/update
covers: [UpdateCheck, update.check, update.notes, update.install, update.restart, app.overlay.update, hud.indicator.update]
related: [shell/settings, shell/quit]
---

# Updates

Oriel keeps itself up to date. Each release is signed by Oriel's release key, and anything that doesn't verify is
refused.

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

**Update** installs the new copy next to the one that's running, keeping the old one. While this happens the status
line says **updating …**.

## Restarting {#ready}

Once the update is installed, the status line shows **update ready · restart**. Click it, or **Restart** on the
card or in the notice, and Oriel quits (its agents hand over first, as at any quit) and opens again on the new version. If
the new version doesn't start properly, Oriel goes back to the previous one by itself.

From a terminal, `oriel update` checks and installs without the app: `--check` only says whether there's a newer
release, and `--channel nightly` picks the channel.
