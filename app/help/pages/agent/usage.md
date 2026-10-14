---
id: agent/usage
covers: [usage.open]
related: [agent/stats, agent/console]
---

# Usage

`:Usage` or `⌘⇧U`, or the title bar's meters: each provider's plan windows. Nothing is prompted to read them, so
reading costs no tokens.

## Accounts {#accounts}

A plan belongs to an account, not to a provider or a machine: a work laptop on a company subscription and a personal
one on Claude Max have separate windows, and they're never added up. Two workers signed in to the same account show
one. The switcher at the top picks the account; it starts on the one the session in front runs on, else this
machine's. Name accounts in `usage.accounts` ("Work", "Personal"); unnamed, an account shows its email or
organisation.

## Indicators {#indicators}

The title bar's meters are the plan of the account the session in front runs on (this machine's with no session).
Hovering shows every account's windows with their resets and burn; a click opens this page. An account whose login
has stopped working on a worker is marked **signed out** until you log in again.

## Windows {#windows}

Each window as a meter: how much is used, when it resets, the burn rate (the slope over the last 30 minutes for the
5-hour window, the last 24 hours for a weekly one) and where that rate puts it at the reset, or when it runs out first.

## History {#history}

7 days of each window, with its resets; dashed, where the burn rate takes it.

## What burned the window {#burned}

What used the current window, by repo: the window's points spread over the repos by their share of its tokens, each
hour at a time. Use with no Oriel session behind it (claude.ai, another machine) is its own row. `r` reads the windows
again.
