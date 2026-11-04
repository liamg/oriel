---
id: files/commit
covers: [commit]
related: [files/diff, files/explorer]
---

# Commit

`git.commit` commits what's staged in a repo (everything uncommitted with **all**). With no message, a dialog
asks for one: `⌘⏎` commits, `esc` cancels. The Explorer's Uncommitted scope (`c`), the diff's review ledger and
scripts all commit this way, so every commit passes the same gates.

## Gates {#gates}

Before anything is committed, each commit gate looks at it. The review ledger's gate counts the agent-written
lines the commit takes that you haven't read (`ledger.gate`):

- **hard** (work and plan sessions): the commit is blocked. **Review** (`r`) opens the diff; **Commit anyway**
  (`a`) commits, and the session's log says how many lines went unread.
- **soft** (jobs): one confirmation (`⏎`), then it commits; the session's log says so.
- **off** (reviews and direct sessions, and outside a session): it commits.

A project's `.oriel/settings.toml` can set `ledger.gate` for its own repo. Plugins add gates of their own, and a
`Commit` hook can still veto a commit that got past them.

{{keys "git.commit*"}}
