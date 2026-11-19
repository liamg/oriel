---
id: shell/update
covers: [UpdateCheck, Changelog, update.check, update.notes.open, update.changelog.open, update.install, update.restart, update.progress.open, app.overlay.update, hud.indicator.update]
related: [shell/settings, shell/quit]
---

# Updates

Oriel keeps itself up to date from its GitHub releases. Each release is signed by Oriel's release key, and anything
that doesn't verify is refused.

## Checking {#check}

With updates **automatic** (the default), Oriel checks as it starts, before anything else. Otherwise it checks
shortly after it starts. Either way it checks every hour after that. `:UpdateCheck` asks now and opens the update card
with what it finds. **Check again** on the card asks again.

A release found while Oriel runs is downloaded and verified in the background before you hear about it, even
when an older update is already installed and waiting for a restart.

{{options "updates"}}

- `updates.mode`: **automatic** (the default) updates as Oriel starts, and installs a release that comes out while
  it runs so the next restart runs it; **ask** offers a downloaded release in a notice, with **What's new** and
  **Update**; **manual** only checks when you run `:UpdateCheck`.
- `updates.channel`: **stable**, or **nightly** for pre-releases too.

## A newer version {#available}

Once a check finds a release newer than what Oriel will run from its next start, the status line shows **oriel 1.2.0
available** on its right. Click it for the update card. While the release downloads or installs, the status line says
so instead, and once it's installed it reads **update ready · restart**.

## The update card {#card}

The newer release, the version you have, and what's new: the notes of every release between the two, newest first,
each under its version, so an update that skips versions shows what each of them changed. A release's notes list its
breaking changes, features and fixes; one whose changes were all internal (refactoring, tests, build) says **No
notable changes**. **Later** (`esc`) closes it. **Update** updates now, on the Updating page. **Restart** shows once
the newest release is installed; if an older update is installed and a newer one has come out since, the card says
so and offers **Update** to the newer one.

## The changelog {#changelog}

`:Changelog` (`update.changelog.open`), or **Changelog** on the About card behind the logo, opens the card on every
release's notes, newest first, read from the releases as it opens: the version you have is marked **you have this**,
and the ones before it are there too. A newer release sits at the top, with **Update**.

## The Updating page {#page}

An update you start, or one an automatic start finds, runs on the Updating page, and every restart onto an update
shows it, however it was asked for. Each step is a line as it happens:
the release found, the download (with a progress bar), verifying it against the signed manifest, extracting it,
checking the new version starts, and swapping it in, with the old copy kept for a rollback. Then the agents hand
over, as at any quit: the page shows the sessions whose turns it waits for, and **Restart now** stops waiting. **Not
now** keeps the update installed for the next restart. Oriel then closes and opens again on the new version, which
says what it updated from.

**Hide** (`esc`) leaves the update running in the background; the status line shows how far it's got, and a click on
it (or `update.progress.open`) brings the page back. If a step fails, the page says why, and **Try again** starts over.

## Installing {#install}

An update installs the new copy next to the one that's running, keeping the old one. While a download or an install
runs, the status line says so.

## Restarting {#ready}

Once an update is installed without a restart, the status line shows **update ready · restart**. Click it, or
**Restart** on the card or in the notice, and Oriel quits (its agents hand over first, as at any quit) and opens again
on the new version. If the new version doesn't start properly, Oriel goes back to the previous one by itself.

From a terminal, `oriel update` checks and installs without the app: `--check` only says whether there's a newer
release, and `--channel nightly` picks the channel.
