---
id: git/ship
covers: [ship, git, pull-request, pr, ship.open, ship.commit, ship.push, ship.push-all, ship.sync, ship.raise, ship.raise-all, ship.pr.open, ship.rename, ship.merge, ship.merge-default, ship.next-pr, ship.browse, ship.copy-link, ship.merge_method, code.setup, code.hosts, code.api]
related: [shell/rail, agent/requests, files/explorer]
---

# Ship

Each project's way from uncommitted work to a merged pull request: **uncommitted → behind its base → unpushed → PR →
checks**. **Ship** in the sidebar is the session's page, every project side by side; a project's **Pull request**
panel is the same in the dock. Every step runs with your own
git and your code host's account: the agent can draft a commit or a PR, never send one. The PR's steps work on any code
host a plugin brings (GitHub is built in; GitLab and Azure DevOps are bundled), in its own words: **Raise MR** on GitLab.

## The page {#page}

One column per project, its steps numbered down the left: a step that's done is ticked and folded to a line, the next
one to do is open. The chip by a project's name says where it stands: needs you (uncommitted work or conflicts), CI
running, CI failing, ready, merged. At the right: what has to happen before it ships, the order the PRs merge in, and
the ticket. `ship.refresh` reads every project again, fetching and asking GitHub.

More columns than fit scroll sideways. A project with nothing to ship (nothing uncommitted, no commits on its
branch, no pull request) gets no column: the line over the columns names them, and **Show** brings their columns
back.

## The Git panel {#git}

A project's **Git** panel, in the dock: its branch and how far it is from its base, **Rebase on main**, **Push** and
**Rename…**, then what's uncommitted with the commit box, and the commits left to push. **↺** on a file throws its
changes away, after asking. An opened directory is a session too (a direct one, with no agent), so the panel, Pull
request and Ship work there as they do in any session.

## Branch {#branch}

**Rename…** renames the branch (`ship.rename`). It stops tracking the old name on origin, so the next push makes the
new one there. A branch whose PR is open isn't renamed: the PR would stay on the old one.

## Commit {#commit}

The uncommitted files, each with its lines added and removed. Untick a file to leave it out. The message starts as the
agent's draft; type to make it yours. **Commit** commits with your git identity; **Commit all** on the page commits
each project with its message. While it commits the button says **Committing…** and the box can't be changed, until
the project's been read again and shows the commit; one git refuses says why, with the message kept.

## Drafts {#drafts}

The agent saves a commit message (`commit_draft`) and a PR (`pr_draft`) for each project with changes when its work
goes quiet, without being asked; they show here marked with who drafted them, and **stale** once the code has moved on
since. When a turn ends with a draft missing or stale, Oriel asks the agent once for that code. What you type into the
commit box or the Raise PR tab is kept as yours ("written by you"): the agent isn't asked for it and can't overwrite
it. **Redraft** hands it back and asks the agent for a new one.

## Gates {#gates}

"Before this ships" lists the gates: the plan approved, threads resolved, tests run, and what plugins add (the verify
quiz, AI review). A gate can offer a button on its row that works towards it: **Verify…** starts
the verify quiz (`git/verify`), **Review** an AI review. A gate that doesn't apply is greyed out: the plan of work
started with no plan or ticket. A plugin can stop a commit with a veto hook on `ShipCommit`:
its reason shows in the commit box, and a rewrite can change the commit (add a trailer, say). A veto on `ShipRaise`
stops a raise the same way.

## Sync {#sync}

**Behind main** counts the commits the base has that the branch lacks, and says whether a rebase would conflict.
**Rebase** (or **Sync all**) fetches and rebases onto it, carrying uncommitted work over; one that conflicts is undone
and says where.

## Push {#push}

The commits origin hasn't got. **Push** pushes the session's branch, and only it. In an opened directory that's the
branch it's on now. Once every project's PR is open, the header offers **Push all** in place of a raise, for any with
commits to push (`ship.push-all`).

After a rebase, the branch has rewritten commits origin already has ("n rewritten"), and Push reads **Force push**.
It replaces origin's branch with a lease: only while origin still has what you last fetched and rebased from. If
someone else pushed to the branch since, the push is refused and nothing of theirs is lost: rebase again, then push.

## Raise PR {#raise}

**Raise PR…** opens a main tab: the title, the body (the agent's draft, else the first commit's message and the repo's template) with a preview,
reviewers (CODEOWNERS of the change first; add or take off any), and the session's other PRs it ships with, in merge
order. **Push +
raise PR** pushes and raises it, **Raise as draft** raises it as a draft, **Save draft** keeps what's written. Once
it's raised the tab shows the PR itself (below); **Edit PR** there brings the form back, **Update PR** saves it and
**Cancel** leaves it as it was. **Push + raise all** raises every project's in merge order. A description is sent as
written: Oriel adds nothing to it, and takes out the list of linked PRs earlier versions added.

## The PR {#pr}

The PR's number, title and state, its reviewers, **Ready for review** for a draft, **Merge…**, and **Open on GitHub ↗**
(or the project's code host). Plugins add buttons beside them (`oriel.ship.action`), such as one that asks your team's
chat to take a look.

The **Pull request** panel in the dock reads top to bottom:

- **Header**: the title, a state chip (open, draft, merged, closed, drafting, not raised), the number and commits, and
  the branch it merges into. **↻** reads it again: fetches origin and asks the code host.
- **Checks**: one line for all of them (all passed, how many fail, how many run, none reported), then each failing or
  running check. The footer says when the code host was last asked and at which commit (`checked 28s ago · at
  c3f1a9e`); it turns amber when the answer is stale, red with why when the host couldn't be reached.
- **Reviews**: each reviewer and where they stand: approved, changes, commented or waiting.
- **Branch**: committed, up to date with the base, pushed, and **On origin**: **new commits** when the PR's head isn't
  the commit checked out and nothing here waits to be pushed (someone else pushed). The footer says when git was read.

Once a project's PR is raised, its tab (**View PR** on the Pull request panel) is the PR as it stands: its title, its
link (click it to open the PR in the browser) with **Copy link** to share it, the checks as they run, the description
as it reads on the code host, and the review: each reviewer's thread and the conversation, with **Open** to take one
up in Requests.

## Merge {#merge}

**Merge…** first shows what stands in the way: review comments not resolved and the session's own open threads, each
counted ("2 reviewer comments, 1 Oriel thread unresolved"), changes still asked for, failing or running checks, a draft,
conflicts, behind its base, missing required reviews, and a PR before it in the merge order. **Show** opens one.
**Continue** merges anyway, **Cancel** doesn't; where the host would refuse (a conflict, a draft, required reviews),
Continue is off. With nothing in the way it's **Merge**. The method is `ship.merge_method` when the repo allows it, else
the repo's own default, else squash, and can be changed for this merge; **Delete the branch** deletes it on the host
after. It merges the commit the dialog showed: a push since makes the host refuse it. Plugins add reasons with the
`PrMerge` hook. The agent can't merge.

Once every project's pull request is merged, the page's main button is **Finish session**, and so is the merged PR's:
it opens the End session dialog (see Session tabs). `session.archive_on_merge` archives the session by itself instead,
once nothing in its worktrees is uncommitted.

## Merge into main, without a PR {#merge-default}

**⋯ → Merge into main…** on the Git panel (`ship.merge-default`) merges the branch's commits into the repo's default
branch (origin's, as a local branch: `main` here) on this machine, after asking in a dialog in the middle of the window. It's a fast-forward where it can be,
else a merge commit; one that would conflict is refused, naming the files, and nothing changes. Where `main` is checked
out (your own checkout, say), that checkout moves to it, and only when it has no uncommitted changes. Nothing is
pushed: push `main` yourself when you're ready. Uncommitted work on the branch stays where it is.

## The next PR {#next}

Once a project's PR is merged, or closed with nothing left on its branch, **Start next PR** (`ship.next-pr`) moves it
on: it brings the default branch up to date and cuts a fresh branch from it, in the same worktree. It won't while the
branch has uncommitted changes, or commits that aren't pushed or on the default branch: commit, push or drop them
first. A PR closed with work still on its branch offers **Raise a new PR…** instead.

Merged from Oriel (**Merge…**, or **Merge into main**) with the agent idle and nothing uncommitted, the next PR starts
by itself. Merged on GitHub, or with the agent mid-turn or changes uncommitted, a question asks first. After **Merge
into main**, the new branch starts from your local `main`: push `main` before raising its PR, or the PR would show the
merged commits again (the Pull request panel says **main pushed · n to push**). A project that moved on from a merged
PR still counts as merged for **Finish session**; with `session.archive_on_merge` on, a merge that leaves every PR
merged archives the session instead of starting the next PR.

**Previous** on the PR tab and the Ship page lists the PRs the session has moved on from, each with where it ended;
click one to open it on its code host.

## Code hosts {#hosts}

A project's PR goes to the code host its origin is on. github.com, gitlab.com and dev.azure.com are known by name. For
a server of your own, Oriel looks in turn at `code.hosts` (hostname → host), the servers gh and glab are logged into,
the URL's shape (`/_git/` is Azure DevOps), and then asks the server itself, once and without a token. If none of those
tells, **Set up…** on the PR step asks you. **Set up…** (`code.setup`) is also where a server's token goes: the API URL
(`code.api` when it isn't the usual one), the token with the host's instructions and **Create a token ↗**, kept as
its own secret (`GITLAB_TOKEN:git.acme.corp`) and sent only to that server, by that host's plugin. It's asked once per
server and used for every repo on it. **None of these** leaves the server's PR steps off.

## Checks {#checks}

The checks on the PR's branch, with how long each took and, for a failing GitHub Actions job, the line of its log that
says what failed. **Fix CI with agent** writes a request asking the agent to fix it, with that line attached.

{{keys "ship.*"}}
