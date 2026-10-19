---
id: agent/reviews
covers: [Reviews, reviews.open, reviews.refresh, reviews.start, reviews.browse, reviews.filter, reviews.scope, app.overlay.reviews]
related: [agent/launcher, plugins/secrets]
---

# Reviews

The pull requests waiting on your review, from GitHub and any code host a plugin adds (a review source). `:Reviews`,
`⌘⇧V` or the sidebar's **Reviews** opens the page; `:Reviews github` lists one source's. The sidebar's violet pill
is how many are waiting on you.

## The page {#page}

The header counts what's waiting on you and what's in progress, and says when the sources were last asked. They're
asked every five minutes while Oriel is open (`reviews.poll`), when the page opens if it's been a minute, and on
**Refresh** (`r`). **all / to me / my teams** (`t`) narrows the list to asks of you or of a team you're on. `esc` or
`✕` closes the page.

## The list {#list}

**Waiting on you** comes first, the oldest ask first, then **In progress**: a review a session of yours has, or one
where you asked for changes or commented and it's their move. Drafts are last in each.

- **new**: you were asked and haven't reviewed it. It counts toward the pill.
- **re-requested**: you reviewed, they pushed and asked again. It counts.
- A team's ask counts unless `reviews.count_teams` is off.
- **reviewing**: a session of yours is on it. It doesn't count; `↵` continues it.
- **changes requested** / **commented**: their move. It doesn't count, and comes back as re-requested.
- **draft**: never counts.

`j`/`k` move, `/` filters by title, repo or author. A source that needs its token, or failed, says so at the foot,
with **Set token** or **Retry**. A new ask that counts is a notice ("mkhan asked you to review api#812") unless
`reviews.notify` is off.

## The pull request {#pr}

The selected pull request shows what the list knows at once, then asks its source for the rest: its branches,
checks, size, whether it merges cleanly, labels, the registered checkout it's reviewed in, its reviewers and where
they stand, its description and its files. The description is shown to you only; the agent never gets it from here.

- **Start review…** (`↵`) opens the new session launcher on Review PR with the pull request filled in: pick the
  agent and sandbox, then start. The session checks the PR's head out read-only and reviews it.
- **Continue review…** brings back the session that has it, resumed if it's stopped.
- **Open in GitHub** (`o`) opens it in the browser.
