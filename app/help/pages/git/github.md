---
id: git/github
covers: [github.sync, github.forward, github.edit, github.draft.save, statusline.left.70-github]
related: [git/ship, agent/requests, files/diff]
---

# GitHub reviews

Review comments on the session's pull requests come into Oriel as threads, between the lines they're about in the
diff and the editor, and in Requests. The agent answers them like any thread, but only as a **draft**: nothing reaches
GitHub until you forward it. The agent never posts to GitHub. You do.

## Threads {#threads}

Each review comment on a PR's line, with its replies, is one thread with a **GitHub review** header: the line, the
reviewer, where their review stands (changes requested, approved, commented) and when it was last synced. The PR's
conversation (its comments and reviews' summaries) is one more thread. The session's worker syncs them every 30
seconds while the session is in front, and at once with `github.sync`. An edit on GitHub changes the thread here, and
the agent is given it again; a thread resolved on GitHub is resolved here, and reopened with it.

## Forward to GitHub {#forward}

Under the agent's draft reply: **↗ Forward to GitHub** posts it as you, from the session's worker with your own gh,
once; it then shows as sent and doesn't come back from GitHub as a new comment. **Edit** changes the draft first
(**Save draft**, or ⌘⏎). `github.forward {thread, comment}` and `github.edit` do the same from keys or Lua.

## Trusted authors {#trusted}

The agent reads only what members of the trusted orgs (`github.trusted_orgs`, the repository's owner when empty) and
the trusted users (`github.trusted_users`) wrote. Anyone else's text is hidden from it, marked **hidden from the
agent** here, so a stranger's comment can't steer it; you can still read it, and forward a reply.

## Status line {#status}

**GH 1 changes requested** says how many reviewers of the session's PRs want changes, or have approved.
