---
id: editor/pane
covers: [editor]
related: [layout/panes, shell/pickers]
---

# The editor

A pane shows one window on a buffer. Keys go to the input engine (vim by default) after the keymaps you've made.

- The tab is the buffer's file, with a dot while it has changes not written.
- `:w` writes, `:e file` opens, `:split` and `⌃W` move between panes, which are the layout's.
- `y`, `d` and `p` use the system clipboard.
- A file that changes on disk reloads by itself, unless you've changed it too: then Oriel asks.

{{options "editor"}}
{{keys "editor.*"}}

## Git hunks {#hunks}

The gutter marks lines that differ from the index: `▎` added or changed, `▁` where lines were deleted. Unsaved
edits count. On a hunk, `␣hs` stages it, `␣hu` unstages the staged hunk there, `␣hr` puts it back as the index has
it (one undo step), and `␣hp` shows the lines it replaced. `]c` and `[c` move between hunks. After the cursor's line,
who last changed it and when: you, an agent (by its name, or a Co-Authored-By trailer), someone else, or "Not
committed yet". `␣hb` (or `git.blame.inline`) turns that off.

{{keys "git.hunk.*"}}
