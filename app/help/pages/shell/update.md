---
id: shell/update
covers: [UpdateCheck, update.check, update.notes, update.install, update.restart, update.progress, app.overlay.update, hud.indicator.update]
related: [shell/settings, shell/quit]
---

# Updates

Oriel keeps itself up to date from its GitHub releases. Each release is signed by Oriel's release key, and anything
that doesn't verify is refused.

## Checking {#check}

With updates **automatic** (the default), Oriel checks as it starts, before anything else. Otherwise it checks
shortly after it starts. Either way it checks every hour after that. `:UpdateCheck` asks now and opens the update card
with what it finds. **Check again** on the card asks again.

A release found while Oriel runs is downloaded and verified in the background before you hear about it.

{{options "updates"}}

- `updates.mode`: **automatic** (the default) updates as Oriel starts, and installs a release that comes out while
  it runs so the next restart runs it; **ask** offers a downloaded release in a notice, with **What's new** and
  **Update**; **manual** only checks when you run `:UpdateCheck`.
- `updates.channel`: **stable**, or **nightly** for pre-releases too.

## The update card {#card}

The newer release, the version you have, and its release notes. **Later** (`esc`) closes it. **Update** updates
now, on the Updating page. **Restart** shows once an update is installed.

## The Updating page {#page}

An update you start, or one an automatic start finds, runs on the Updating page. Each step is a line as it happens:
the release found, the download (with a progress bar), verifying it against the signed manifest, extracting it,
checking the new version starts, and swapping it in, with the old copy kept for a rollback. Then the agents hand
over, as at any quit: the page shows the sessions whose turns it waits for, and **Restart now** stops waiting. **Not
now** keeps the update installed for the next restart. Oriel then closes and opens again on the new version, which
says what it updated from.

**Hide** (`esc`) leaves the update running in the background; the status line shows how far it's got, and a click on
it (or `update.progress`) brings the page back. If a step fails, the page says why, and **Try again** starts over.

## Installing {#install}

An update installs the new copy next to the one that's running, keeping the old one. While a download or an install
runs, the status line says so.

## Restarting {#ready}

Once an update is installed without a restart, the status line shows **update ready · restart**. Click it, or
**Restart** on the card or in the notice, and Oriel quits (its agents hand over first, as at any quit) and opens again
on the new version. If the new version doesn't start properly, Oriel goes back to the previous one by itself.

From a terminal, `oriel update` checks and installs without the app: `--check` only says whether there's a newer
release, and `--channel nightly` picks the channel.
