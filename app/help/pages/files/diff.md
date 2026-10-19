---
id: files/diff
covers: [diff]
related: [files/explorer, files/commit, agent/requests]
---

# Diff

The review surface: a repo's branch against its base (the merge-base with origin's default branch, or main), your
uncommitted work included. The Explorer's Changed scope, the agent console's **view in diff** and plan mode's
**Preview diff** open it (`diff.open`).

## Changes {#changes}

At the left, every file the branch changes, with its size. The box beside each says how much of it you've marked
reviewed: ticked when every hunk is, half when some are. Clicking it marks the whole file, or takes the marks off;
clicking the name opens the file and goes to it.

Under the files, plugins add their own boxes. **Authorship** counts who wrote the lines the change adds: blame for
those committed (an agent, you, someone else), and for those not yet, the open buffer's authors.

## Files {#files}

Each file has a header with its path and size, and how many of its hunks are reviewed. A click shows or folds it.

## Hunks {#hunks}

Each hunk starts with git's `@@` line and the function, method or type it's in. Removed lines are red and added
ones green, the words that changed in a changed line marked brighter. A click selects a line; threads open between
the lines they're about, as in the editor.

## Split or unified {#layout}

**split** shows the old file at the left and the new at the right; **unified** shows one above the other. The
choice is remembered (`diff.layout`).

## Reviewed {#review}

**Mark reviewed** at a hunk's right, or `r`, marks it reviewed. A mark belongs to the change itself, not to its
place, so it stays when other parts of the file are edited, and it's kept with the session.

## Review ledger {#ledger}

Above the files, the **review ledger** counts the lines an agent wrote in this change: read, skimmed and unread.
A line is the agent's when blame says its commit is an agent's (by name, or a Co-Authored-By trailer), when the
open buffer says the agent typed it, or, not committed yet, when the agent's edits in the session added it. Blank
lines don't count.

A line is **read** when its hunk is marked reviewed, or when it stays on screen in the diff long enough to read:
about a second, and longer the more agent lines show at once. Scrolled past more briefly than that, but not in a
blur, it's **skimmed**. Both are kept with the session. A folded file shows how many of its lines are unread.

The chip at the right is the commit gate (`ledger.gate`): **commit gated until 100%** when a commit is blocked
until every agent-written line it takes is read, **commit asks first** when it only asks. **Commit** opens the
commit dialog.

## Explain {#explain}

The `?` at a hunk's left, or `?` on a selected line, asks for the code there to be explained.

## Keys {#keys}

`]h` and `[h` go between hunks, `j` and `k` between lines, `r` marks the hunk reviewed, `gc` opens a thread on the
line (or the hunk's lines), `?` explains, `o` opens the line in the editor, `⏎` shows or folds the file.

{{keys "diff.*"}}

## Images {#images}

A changed image is drawn rather than listed as binary: **Side by side**, the old version beside the new with each
one's dimensions and size, or **Slider**, the two overlaid with a divider you drag (or move with `h`/`l`): the old
left of it, the new right. An added or deleted image shows its one side. Click an image, or ⤢ on the slider, to see
the two large in the lightbox.
