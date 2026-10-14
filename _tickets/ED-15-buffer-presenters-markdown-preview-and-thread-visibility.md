---
id: ED-15
title: "Buffer presenters with a source map, a built-in markdown preview, and showing or hiding a file's threads"
phase: P5
status: todo
area: Editor
module: app, registry, script
depends_on: [ED-7, AI-5, AI-8, SH-17]
---

# ED-15 · Buffer presenters with a source map, a built-in markdown preview, and showing or hiding a file's threads

**Phase** 5 · AI (follow-up) · **After** [ED-7](ED-7-decorations-between-lines-threads-explain-cards-.md), [AI-5](AI-5-requests-inline-threads-plus-the-main-requests-v.md), [AI-8](AI-8-plan-mode-rendered-docs-margin-threads-readiness.md), [SH-17](SH-17-project-sidebar-per-project-panels-and-shared-tabs.md) · **Unblocks** nothing yet

Added after P5, from review: a markdown buffer wants a Preview / Edit button, threads should show (and be written)
in a preview, and a file's threads can be hidden behind gutter bubbles.

## Design

- `_design/ui.md` (01 · the editor; 05 · Plan mode; 01b · threads)
- `_design/plugins.md` (editor actions, decorations, views)
- `_design/scripting.md` (`oriel.editor`)

## What it is

- **Editor actions toggle their label:** `registry.EditorAction` gains `LabelOn`/`IconOn`, shown while `On()` says
  so (Preview ↔ Edit).
- **Buffer presenters** (`registry.BufferPresenter{Name, Label, When(path), Comments bool, Build(ctx)}`): a way to
  draw a buffer other than as text, per window. `editor.present {name}` switches the focused window to it and back;
  the buffer stays open and editable, and the presentation rebuilds as the text changes.
- **The source map:** as it builds, a presenter marks each block with the source lines it came from
  (`ctx.Block(from, to, body)`). From the map the host gives every presenter, once:
  - threads anchored in the file, as margin cards level with their block (the nearest block before an unmapped line);
  - commenting on a block when the presenter allows it (`Comments`): a click, or `j`/`k` then `c`, anchors a thread to
    that block's lines, a real code anchor that shows in the editor too;
  - scroll sync between Edit and the presentation, "go to source" on a block, and decorations (diagnostics, the
    agent's edit) on the block holding their line.
- **A built-in `markdown` plugin**: a **Preview / Edit** action on markdown files and `:MarkdownPreview`, rendering
  with the shared markdown renderer (`app/markdown.go`) with comments on. Replaceable and removable, like any built-in.
- **Plan mode on it:** AI-8's plan doc view becomes the markdown presenter with comments on; its margin, block
  numbers and edit highlight come from the host, not a copy.
- **Threads shown or hidden per file:** a **💬 n** editor action and `threads.toggle` (`␣rh`) per window;
  `editor.threads = "shown" | "hidden"` sets how files open. Hidden, each line with threads gets a speech-bubble sign
  coloured by state (with a count when there are several). Hovering it shows a popover with the thread (title, state,
  last messages as markdown) that goes when the pointer leaves the sign and the popover, never taking focus or keys;
  a click, or `⏎` on the line, shows the threads again scrolled to that one with its reply field ready. Presenters get
  the same, with bubbles beside blocks.
- **Lua:** `oriel.editor.action{…}` and `oriel.editor.presenter{ name, when, comments, build = fn(ctx) }` with
  `ctx.block(from_line, to_line, fn)`.

## Done when

- A Lua presenter (a CSV table, say) maps rows to lines; a thread on line 4 shows beside its row, and a comment made on
  a row anchors to that row's line and shows inline in Edit.
- On a `.md` file, Preview renders it with its threads in the margin; the button reads Edit; Edit returns at the same
  place. Two windows on one file can be in Edit and Preview at once, and an edit in one shows in the other.
- Plan mode's snapshot is unchanged, built on the markdown presenter.
- `threads.toggle` hides a file's threads behind gutter bubbles; hovering one shows its popover, which goes when the
  pointer leaves; clicking shows the threads at that one. `editor.threads = "hidden"` opens files that way.

## Rules

- Start only when everything in **After** is `done`.
- No special cases: build on the general plugin APIs, and extend them generally if they're missing something (`_design/plugins.md`).
- Test-first, with verified spec references (`_design/testing.md`).
- No new dependency without approval (`_design/deps.md`).
- Update the design doc in the same PR if behaviour differs from it.
