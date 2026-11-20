---
id: ui/context-menu
related: [shell/help]
---

# Context menus

Right-click anything for what you can do with it. The last item is always its help.

- In the editor: **Cut**, **Copy** and **Paste**, and **Copy path**; on any pane: **Move pane…** and **Close**.
- On text you've selected in a page (a comment, a doc, help): **Copy**.
- On a picture (an image file's tab, one in a comment or a doc): **Copy image**, as PNG. On Linux it needs `wl-copy`
  (Wayland) or `xclip` (X11).
- In a text field: **Cut**, **Copy**, **Paste** and **Select all**. A field of hidden text (a token) won't cut or copy.
- In a text field: **Fix spelling & grammar** sends what you've selected, or the whole field with nothing selected,
  to a small model, and puts its fix in place as one edit (undo puts it back). The field shows a spinner while it
  waits; if you change that text meanwhile, the fix is dropped rather than put over what you typed.
- Plugins and your config add entries for where they apply: `oriel.menu.add{ label = "Run go vet", when = { file =
  "*.go" }, run = function(at) … end }` (see plugins/lua). Entries sit in groups, with Help last.
