---
id: files/diff
covers: [diff]
related: [files/explorer, files/commit, agent/requests]
---

# Diff

The review surface: a repo's branch against its base (the merge-base with origin's default branch, or main), your
uncommitted work included. The Explorer's Changed scope, the agent console's **view in diff** and plan mode's
**Preview diff** open it (`diff.open`). From Uncommitted (`explorer.diff`, or `diff.open` with `against` `head`), a
file's diff is against the last commit instead: what isn't committed, in a tab of its own (**Uncommitted · repo**),
each file once, staged and unstaged changes together. Opened at a line (a review's **Jump to code**, a finding's place), it
selects that line, removed or added, in the middle of the view.

## Changes {#changes}

At the left, every file the branch changes, with its size. The box beside each says how much of it you've marked
reviewed: ticked when every hunk is, half when some are. Clicking it marks the whole file, or takes the marks off;
clicking the name expands the file, folded or not, and scrolls it to the top of the view.

Under the files, plugins add their own boxes.

## Files {#files}

Each file has a header with its path and size, and how many of its hunks are reviewed. A click shows or folds it.

## Hunks {#hunks}

Each hunk starts with git's `@@` line and the function, method or type it's in. Removed lines are red and added
ones green, the words that changed in a changed line marked brighter. A click selects a line: it's filled and edged
in the accent, and the foot of the diff says where it is and the keys that act on it (`gc` comment, `ga` ask the
agent, `o` open it in the editor, `?` explain). A double click opens it in the editor. Threads open between the lines they're about, as in the editor.

## Unchanged lines {#context}

A hunk shows three unchanged lines either side of its change; the rest are hidden behind a row saying how many. Its
arrows show them as GitHub's do, 20 lines a click: **↑** above the first hunk shows lines up from it, **↓** below the
last shows lines down to the file's end, and between two hunks **↓** shows lines down from the hunk above and **↑**
lines up from the one below, until 20 or fewer are left, which **↕** shows at once. `zo` shows 20 lines each side
of the selected hunk, `zR` every hidden line of the file (`diff.context.more`). The lines stay shown until the
file's change does. Threads on them show under them, but comments are made on the diff's own lines.

## Long lines {#wrap}

**wrap** in a file's header (or `diff.wrap`) wraps long lines onto the rows below, or stops; it's the editor's
`editor.wrap`, so the editor's `:set wrap` and its status line's **wrap** switch it too. Not wrapped, a file's
lines scroll across together, the line numbers staying put: with a sideways wheel or trackpad, ⇧ and the wheel, or
`zl`/`zh` (a column) and `zL`/`zH` (half the view).

## Split or unified {#layout}

**split** shows the old file at the left and the new at the right; **unified** shows one above the other. The
choice is remembered (`diff.layout`).

## Reviewed {#review}

**Mark reviewed** at a hunk's right, or `r`, marks it reviewed. A mark belongs to the change itself, not to its
place, so it stays when other parts of the file are edited, and it's kept with the session.

## Explain {#explain}

The `?` at a hunk's left, or `?` on a selected line, asks for the code there to be explained.

## Keys {#keys}

`]h` and `[h` go between hunks, `j` and `k` between lines, `r` marks the hunk reviewed, `gc` opens a thread on the
line (or the hunk's lines), `?` explains, `o` opens the line in the editor, `⏎` shows or folds the file, `zo` and
`zR` show hidden lines, and `zl`, `zh`, `zL` and `zH` scroll across. The pointer on a line shows a **+** at the left of its gutter that opens a thread
on it too (see [Between the lines](agent/requests#inline)).

{{keys "diff.*"}}

## Images {#images}

A changed image is drawn rather than listed as binary: **Side by side**, the old version beside the new with each
one's dimensions and size, or **Slider**, the two overlaid with a divider you drag (or move with `h`/`l`): the old
left of it, the new right. An added or deleted image shows its one side. Click an image, or ⤢ on the slider, to see
the two large in the lightbox.
