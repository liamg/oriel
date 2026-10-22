---
id: agent/reviews
covers: [Reviews, reviews.open, reviews.refresh, reviews.start, reviews.browse, reviews.filter, reviews.scope, app.overlay.reviews, review.pr.fetch, review.pr.recheck, review.pr.include, review.pr.submit, review.pr.pick, review.pr.severity, review.pr.edit, review.pr.rework, review.pr.rework.use, review.pr.rework.drop, review.pr.summary, review.pr.next, review.pr.show, review.pr.preview, review.severity_badges, review.auto_rereview]
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

## In a review session {#session}

A review session's agent gets its review as a request, **Review api#812**, so it's in Requests like any other. It
starts at once, while you read, on the PR's head, read-only, and leaves its findings as threads on their lines; the
AI review runs beside it. Ship and Git aren't offered: there's nothing of yours to commit or ship.

The session opens on the **Explorer** in Changed, which compares with the branch the PR merges into, so it lists
only the PR's files, and on its **Review** tab. The sidebar's **Review** entry (the pull request icon, in place of
Raise PR's checklist) opens the tab again, is lit while it's showing, and counts the comments still to triage. The tab's top line says where the agent and the AI review are;
when the agent resolves its request, a **Review done** notice says how many comments it has for you to triage.

### Triage {#triage}

**Your review** lists every comment the review can post, worst first: the agent's threads on lines, the AI review's
findings (one row, tagged *agent + AI review*, where both found the same lines), and your own. Each is undecided
until you keep or drop it; those rated medium or worse, and yours, go unless you drop them. The line above says how
many you've triaged. Click a row to select it (again to show it in the diff), or use its ⋯ menu. The keys are
along the bottom of the tab, and `F1` on a row opens this page; with a row selected:

| Key | Does |
|-----|------|
| `j` `k` | the next or the comment before |
| `y` `x` `u` | keep it (and on to the next undecided), drop it, undecide it (`review.pr.pick`) |
| `1`–`5` | rate it critical, high, medium, low or nit (`review.pr.severity`) |
| `e` | edit what the author gets (`review.pr.edit`) |
| `r` | ask the agent to make it shorter (`review.pr.rework`); the menu has friendlier, a suggestion block, more detail, or your own words |
| `⏎` `o` | show it at its line in the diff (`review.pr.show`) |
| `S` | ask the agent to draft the summary (`review.pr.summary`) |

You can triage as you read, too: every comment's card in the diff and in Requests (and every AI review finding's)
ends with **YOUR REVIEW**: its severity, whether it goes to the author, **Keep**, **Drop** and **In the review ↗**.
A dropped comment's thread is resolved, so Requests holds only what's left; keeping it again reopens it. In a review
the AI review's findings don't offer **Fix**: the PR isn't yours to change.

The selected comment shows **what the author gets**: the reviewer's words until you edit them or use the agent's
rewording. A rework is asked in the comment's own thread; its answer shows under it with **Use it** and **Keep
mine**. Your triage, edits and summary are kept in the session, so they survive a restart.

### Your own comments {#comments}

In a review session, `gc` on a line of the diff opens a **comment for the author**: it goes in your review, tagged
*you*, and the agent isn't asked to answer it. The composer's **To the author | Ask the agent** switch, or `ga`
(in the diff and the editor, in any session), asks the agent about the line instead.

### Sending it {#send}

**Draft summary** has the agent write two or three sentences on what blocks merging, into the summary box, which you
can edit. The verdict the kept comments call for is highlighted: **Request changes** for anything high or worse,
**Comment** for anything at all, else **Approve**. Each opens a **preview** of exactly what's posted (the summary,
then each comment at its line, as the author gets it); **Post** sends it (`review.pr.preview`, `review.pr.submit`),
at the commit the review read. A comment the host won't put on the diff goes in the summary instead. With
`review.severity_badges` on (the default), each comment is headed with a badge of its severity.

### After it's sent {#after}

The comments come back from GitHub as its review threads, each in the thread it was posted from, and **Sent** lists
them with where each stands: *waiting on author*, *author replied*, *code changed* or *resolved*. The author's
replies reach the agent, which can draft an answer for you to forward; it never posts. Resolving a thread in Oriel
resolves it on GitHub too, and reopening it reopens it there (a code host plugin's `resolve` does the same for its
host).

Open review sessions are synced with their pull requests every two minutes, and the one in front every 30 seconds.
When the author pushes, the checkout moves to the new head, and with `review.auto_rereview` on (the default) the
agent is asked to review what changed once its last review is done (`review.pr.recheck`): where each earlier comment
stands, closing those the new commits fixed. **Fetch latest** (`review.pr.fetch`) does it now.

When the author replies on a comment you posted, a notice says so, with the first line of the reply. Once the PR is
merged or closed, the tab says so, nothing more can be posted, and syncing stops; **Archive this review** (there, or
under **Sent** once you've posted) puts the session away.

A review the assistant started in the background opens on its Explorer and tab the first time you bring it to the
front. If the agent answers its request without resolving it, the review counts as done once it stops.

The PR's description, files, checks and reviewers are on the tab too, as its host has them. A code host that can't
take a review from Oriel (no `submit`) says so where the verdicts would be, with **Open in** it.
