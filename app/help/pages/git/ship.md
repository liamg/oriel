---
id: git/ship
covers: [ship, git, pull-request, pr, ship.open, ship.commit, ship.push, ship.sync, ship.raise, ship.raise-all, ship.pr.open, ship.rename, ship.merge, ship.browse, ship.merge_method, code.setup, code.hosts, code.api]
related: [shell/rail, agent/requests, files/explorer]
---

# Ship

Each project's way from uncommitted work to a merged pull request: **uncommitted → behind its base → unpushed → PR →
checks**. **Ship** in the sidebar is the session's page, every project side by side; a project's **Pull request**
panel is the same checklist, a line a step, in the dock. Every step runs with your own
git and your code host's account: the agent can draft a commit or a PR, never send one. The PR's steps work on any code
host a plugin brings (GitHub is built in; GitLab and Azure DevOps are bundled), in its own words: **Raise MR** on GitLab.

## The page {#page}

One column per project, its steps numbered down the left: a step that's done is ticked and folded to a line, the next
one to do is open. The chip by a project's name says where it stands: needs you (uncommitted work or conflicts), CI
running, CI failing, ready, merged. At the right: what has to happen before it ships, the order the PRs merge in, and
the ticket. `ship.refresh` reads every project again, fetching and asking GitHub.

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
each project with its message.

## Drafts {#drafts}

The agent saves a commit message (`commit_draft`) and a PR (`pr_draft`) for each project with changes when its work
goes quiet, without being asked; they show here marked with who drafted them, and **stale** once the code has moved on
since. When a turn ends with a draft missing or stale, Oriel asks the agent once for that code. What you type into the
commit box or the Raise PR tab is kept as yours ("written by you"): the agent isn't asked for it and can't overwrite
it. **Redraft** hands it back and asks the agent for a new one.

## Gates {#gates}

"Before this ships" lists the gates: the plan approved, threads resolved, tests run, and what plugins add (the review
ledger, the verify quiz, AI review). A gate can offer a button on its row that works towards it: **Verify…** starts
the verify quiz (`git/verify`), **Review** an AI review. A plugin can stop a commit with a veto hook on `ShipCommit`:
its reason shows in the commit box, and **Commit anyway…** commits past it, which the hook may record (a trailer). A
veto on `ShipRaise` stops a raise the same way.

## Sync {#sync}

**Behind main** counts the commits the base has that the branch lacks, and says whether a rebase would conflict.
**Rebase** (or **Sync all**) fetches and rebases onto it, carrying uncommitted work over; one that conflicts is undone
and says where.

## Push {#push}

The commits origin hasn't got. **Push** pushes the session's branch, and only it. In an opened directory that's the
branch it's on now.

## Raise PR {#raise}

**Raise PR…** opens a main tab: the title, the body (the agent's draft, else the repo's template) with a preview,
reviewers (CODEOWNERS of the change first; add or take off any), and how it links to the session's other PRs. **Push +
raise PR** pushes and raises it, **Raise as draft** raises it as a draft, **Save draft** keeps what's written. Once
it's raised the tab is **Edit PR** and **Update PR** saves it. **Push + raise all** raises every project's in merge
order, and each PR's description lists the others.

## The PR {#pr}

The PR's number, title and state, its reviewers, **Ready for review** for a draft, **Merge…**, and **Open on GitHub ↗**
(or the project's code host). Plugins add buttons beside them (`oriel.ship.action`), such as one that asks your team's
chat to take a look.

## Merge {#merge}

**Merge…** first shows what stands in the way: review comments not resolved and the session's own open threads, each
counted ("2 reviewer comments, 1 Oriel thread unresolved"), changes still asked for, failing or running checks, a draft,
conflicts, behind its base, missing required reviews, and a PR before it in the merge order. **Show** opens one.
**Continue** merges anyway, **Cancel** doesn't; where the host would refuse (a conflict, a draft, required reviews),
Continue is off. With nothing in the way it's **Merge**. The method is `ship.merge_method` when the repo allows it, else
the repo's own default, else squash, and can be changed for this merge; **Delete the branch** deletes it on the host
after. It merges the commit the dialog showed: a push since makes the host refuse it. Plugins add reasons with the
`PrMerge` hook. The agent can't merge.

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
