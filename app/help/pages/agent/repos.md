---
id: agent/repos
covers: [repos.open, repos.new, repos.add, repos.clone, repos.remove, repos.scan, repos.fetch, repos.housekeep, repos.tag, app.overlay.repos, app.overlay.repos.new]
related: [agent/launcher, agent/jobs, agent/stats]
---

# Repos

Your checkouts, registered once, in `~/.oriel/repos.json`. Sessions make worktrees of them and never write to the
checkouts themselves; the Repos page does, when you press a button and confirm.

## The Repos page {#page}

`:Repos`, or **Repos** in the sidebar or on the start view: the repos on the left, the selected one on the right.
**+ Add repo** (`a`) adds one, **Scan for repos** looks for more, `esc` or **Close** closes it.

## The list {#list}

Every registered repo with its default branch and path. A dot and "to tidy" mean housekeeping found something to
clean; **found** marks one Oriel found for you, until a session uses it. The filter narrows by name or path.

## Finding repos {#discovery}

When Oriel starts, and when you add a repo, it looks in the folders your repos are in, one level deep, and adds the
git repos it finds there (not worktrees, never your home folder itself). A repo you remove stays removed.
`repos.discover` turns this off; **Scan for repos** runs it now, reading every folder again.

## A repo {#detail}

Its path, remote and default branch, where its branch is against its upstream, its last commit, and when it was added
and last used. **Fetch** (`f`) fetches and prunes; **Reveal** shows it in the file manager; **Remove** asks once,
then forgets the repo (its files stay); **New session** opens the launcher with it ticked.

## Housekeeping {#housekeeping}

Local branches merged into the default branch, and branches whose upstream was deleted (a squash-merged pull
request), each with its worktree if it has one, and worktrees whose folder is gone. The default branch and the one
checked out are never offered; a branch a live session works on is shown but locked. A worktree with uncommitted
changes is left unticked: tick it to remove it anyway. **Clean up** asks once more, then removes each worktree and
deletes each branch, and says how each went.

## Tags {#tags}

**New tag…**: the branch (the default), the tag (the next patch after the latest version tag; **patch**, **minor**
and **major** bump it), and its message (`repos.tag.message`, `{tag}` by default). **Pull latest first**
fast-forwards the branch, and refuses if it has uncommitted changes. **Signed** is on when a signing key is set up.
**Push this tag** pushes only the new tag. Tags are always annotated. A push that fails keeps the tag, with **Push
again**.

## Add repo {#add}

From the Repos page, the launcher's **+ Add repo**, the sidebar's `+` ("Add a new repo…"), or `repos.new`. **Existing directory** takes a checkout or a worktree of one (it adds the checkout), and says what it
found as you type. **Clone from URL** shows where the clone goes before it starts: a parent folder (where most of
your repos are, else `repos.dir`), the folder name (from the URL) and **Clones into**. It clones with this machine's
git credentials; `esc` stops it and takes away what it made. Added from the launcher, the repo comes back ticked.
