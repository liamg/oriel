---
id: git/ship
covers: [ship, pull-request, pr, ship.open, ship.commit, ship.push, ship.sync, ship.raise, ship.raise-all, ship.pr.open]
related: [shell/rail, agent/requests, files/explorer]
---

# Ship

Each project's way from uncommitted work to a merged pull request: **uncommitted → behind its base → unpushed → PR →
checks**. **Ship** in the sidebar is the session's page, every project side by side; a project's **Pull request**
panel is the same checklist, a line a step, in the dock. Every step runs on the
session's worker with your own git and gh: the agent can draft a commit or a PR, never send one.

## The page {#page}

One column per project, its steps numbered down the left: a step that's done is ticked and folded to a line, the next
one to do is open. The chip by a project's name says where it stands: needs you (uncommitted work or conflicts), CI
running, CI failing, ready, merged. At the right: what has to happen before it ships, the order the PRs merge in, and
the ticket. `ship.refresh` reads every project again, fetching and asking GitHub.

## Commit {#commit}

The uncommitted files, each with its lines added and removed. Untick a file to leave it out. The message starts as the
agent's draft; type to make it yours. **Commit** commits with your git identity; **Commit all** on the page commits
each project with its message.

## Drafts {#drafts}

The agent saves a commit message (`commit_draft`) and a PR (`pr_draft`) for each project with changes when its work
goes quiet, without being asked; they show here marked with who drafted them, and **stale** once the code has moved on
since. When a turn ends with a draft missing or stale, Oriel asks the agent once for that code. What you type into the
commit box or the Raise PR tab is kept as yours ("written by you"): the agent isn't asked for it and can't overwrite
it. **Redraft** hands it back and asks the agent for a new one.

## Gates {#gates}

"Before this ships" lists the gates: the plan approved, threads resolved, tests run, and what plugins add (the review
ledger, the verify quiz, AI review). A plugin can stop a commit with a veto hook on `ShipCommit`: its reason shows in
the commit box, and **Commit anyway…** commits past it, which the hook may record (a trailer). A veto on `ShipRaise`
stops a raise the same way: the verify quiz (`git/verify`) does.

## Sync {#sync}

**Behind main** counts the commits the base has that the branch lacks, and says whether a rebase would conflict.
**Rebase** (or **Sync all**) fetches and rebases onto it, carrying uncommitted work over; one that conflicts is undone
and says where.

## Push {#push}

The commits origin hasn't got. **Push** pushes the session's branch, and only it.

## Raise PR {#raise}

**Raise PR…** opens a main tab: the title, the body (the agent's draft, else the repo's template) with a preview,
reviewers (CODEOWNERS of the change first; add or take off any), and how it links to the session's other PRs. **Push +
raise PR** pushes and raises it, **Raise as draft** raises it as a draft, **Save draft** keeps what's written. Once
it's raised the tab is **Edit PR** and **Update PR** saves it. **Push + raise all** raises every project's in merge
order, and each PR's description lists the others.

## The PR {#pr}

The PR's number, title and state, its reviewers, **Ready for review** for a draft, and **Open on GitHub ↗**.

## Checks {#checks}

The checks on the PR's branch, with how long each took and, for a failing GitHub Actions job, the line of its log that
says what failed. **Fix CI with agent** writes a request asking the agent to fix it, with that line attached.

{{keys "ship.*"}}
