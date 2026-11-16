---
id: layout/panes
covers: [app.root]
related: [editor/pane]
---

# Panes and docks

Every view lives in the layout: splits and tabs in the middle, docks along the left, right and bottom, floats,
and a drop-down.

## Tabs {#tabs}

A pane can hold several views as tabs. Click one to show it; drag it along its tab strip to reorder the tabs, or
elsewhere to move it. `⌘W` closes the tab in front, from the editor in any mode too (`⌃W c` and `q` do the same); a
session's tab along the top isn't one of these, and stays.

## Splits {#splits}

Drag the line between two panes to resize them. `⌃W =` evens them out.

## Drag and drop {#drag-and-drop}

Drag a tab onto another pane's edge to split it, onto its middle or its tab strip to join its tabs, or onto a window
edge to dock it. A floating pane's tab strip takes the tab too.

While you drag, the target lights up and a label says what letting go does: *Split right*, *Tab with main.go*,
*Dock bottom*, *Move tab*. Joining a pane's tabs, or a dock that already has views, outlines its tab strip, shows the
new tab's slot and lights the body where its content will show. Over a tab strip, the tab goes where the pointer is,
shown by a bar between two tabs; over the body, it goes at the end. Over the rest of a floating pane the drop floats,
shown as a dashed outline.

{{keys "layout.*"}}
