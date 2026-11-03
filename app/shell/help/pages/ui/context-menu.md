---
id: ui/context-menu
related: [shell/help]
---

# Context menus

Right-click anything for what you can do with it. The last item is always its help.

- In the editor: **Copy path**; on any pane: **Move pane…** and **Close**.
- Plugins and your config add entries for where they apply: `oriel.menu.add{ label = "Run go vet", when = { file =
  "*.go" }, run = function(at) … end }` (see plugins/lua). Entries sit in groups, with Help last.
