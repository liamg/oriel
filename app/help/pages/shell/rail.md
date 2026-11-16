---
id: shell/rail
covers: [app.rail]
related: [layout/panes, shell/settings]
---

# The rail

The icons down the left edge open the main views in the left dock: Explorer, Search, Git, Requests, Debug and Tests,
with Settings at the bottom. Click the lit one again, or press its key again, to hide it.

The rail stays beside a page (Settings, Stats, Performance…), which covers the rest of the window down to the status
line. A click on an icon there puts the page away first; a lit icon's view stays showing.

- A dimmed icon is a view that isn't available yet: nothing has registered it.
- A number on an icon is how many things there want you.
- Below a second divider are the pages every session shares, each with a live figure: Stats (today's cost), Usage
  (the weekly window), Jobs and Settings. In the narrow sidebar their icons sit at the foot.
- Plugins add their own icons with `oriel.rail.add`.

## Projects

A session with more than one project lists them under **PROJECTS**, each with its branch and state, and its panels
(Explorer, Search, Git…) under the open one.

- Click a project's header, or press `⌃1` to `⌃9`, to open it. Click the open one's header again to fold its panels
  away, and again to bring them back (`project.fold`).
- Drag a header onto another to move the project to its place (`project.move`). `⌃1` to `⌃9` follow the order.

{{keys "view.toggle"}}
