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

{{keys "view.toggle"}}
