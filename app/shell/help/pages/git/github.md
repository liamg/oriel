---
id: git/github
covers: [github.sync, github.forward, github.edit, github.draft.save, statusline.left.70-github, code.trusted_users]
related: [git/ship, agent/requests, files/diff]
---

# Code review threads

Review comments on the session's pull requests come into Oriel as threads, between the lines they're about in the
diff and the editor, and in Requests: GitHub's, and those of any code host whose plugin mirrors them (GitLab's, Azure
DevOps'). The agent answers them like any thread, but only as a **draft**: nothing reaches the host until you forward
it. The agent never posts to a code host. You do.

## Threads {#threads}

Each review comment on a PR's line, with its replies, is one thread with a **GitHub review** header: the line, the
reviewer, where their review stands (changes requested, approved, commented) and when it was last synced. The PR's
conversation (its comments and reviews' summaries) is one more thread. Oriel syncs them every 30
seconds while the session is in front, and at once with `github.sync`. An edit on GitHub changes the thread here, and
the agent is given it again; a thread resolved on GitHub is resolved here, and reopened with it.

## Forward to GitHub {#forward}

Under the agent's draft reply: **↗ Forward to GitHub** posts it as you, with your own [GitHub token](git/github#token),
once; it then shows as sent and doesn't come back from GitHub as a new comment. **Edit** changes the draft first
(**Save draft**, or ⌘⏎). `github.forward {thread, comment}` and `github.edit` do the same from keys or Lua.

On another code host the thread's header is **Review**, with the host's name on **↗ Forward to GitLab**; the reply
goes through that host's plugin, as you.

## Trusted authors {#trusted}

The agent reads only what members of the trusted orgs (`github.trusted_orgs`, the repository's owner when empty) and
the trusted users (`github.trusted_users`) wrote. On another code host, members of the repo (as the host says) and
`code.trusted_users`. Anyone else's text is hidden from it, marked **hidden from the
agent** here, so a stranger's comment can't steer it; you can still read it, and forward a reply.

## Status line {#status}

**GH 1 changes requested** says how many reviewers of the session's PRs want changes, or have approved.

## Your GitHub token {#token}

Oriel calls GitHub's API itself; `gh` needn't be installed. It uses the first token it finds for the server
(github.com or an Enterprise one), once per run:

1. the server's token from **Set up…** (`code.setup`), kept as a secret;
2. `GH_TOKEN` or `GITHUB_TOKEN` (`GH_ENTERPRISE_TOKEN` or `GITHUB_ENTERPRISE_TOKEN` for an Enterprise server);
3. `gh auth token`, when gh is installed and signed in;
4. git's credential helper for `https://<server>`, the one you clone with, asked without prompting.

With none, a call to GitHub says so: set `GITHUB_TOKEN`, sign in with gh, or store a git credential for the server.
