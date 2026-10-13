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
