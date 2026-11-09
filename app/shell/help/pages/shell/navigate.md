---
id: shell/navigate
covers: [nav.back, nav.forward, editor.back, editor.forward]
related: [shell/tabs, shell/help]
---

# Back and forward

Oriel remembers where you've been, like a browser: each session you brought to the front, and each full page you
opened over it (Stats, Jobs, Settings…). Back puts the one before up again, session and page
together; forward undoes it. Going somewhere new after going back drops what was forward. A session archived since
is passed over.

The mouse's side buttons are back and forward, wherever they make sense:

- **In the editor**, back first goes through the jumplist, as `⌃o` does, and forward as `⌃i`. Once the jumplist
  has nothing older, back goes to the session or page before.
- **In help**, they go through the pages you've read.
- **Everywhere else**, they go through the sessions and pages.

The buttons are the keys `<X1Mouse>` (back) and `<X2Mouse>` (forward), Vim's names for them, so you can map them
like any key. On Linux they work under Wayland, not yet under X11.

{{keys "nav.*"}}
