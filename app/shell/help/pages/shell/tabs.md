---
id: shell/tabs
covers: [app.titlebar]
related: [shell/omnibox, errors]
---

# Session tabs

Each session has a tab across the top. Its mark is its status, by shape as well as colour: a red `!` needs you, a
violet `?` wants permission, a cyan spinner is working, a blue `◌` is waiting on CI, a pink `…` is with a reviewer, a
green `✓` is quiet, a grey `○` is off. The HUD uses the same marks. A `✕` and a number mean errors
in that session you haven't seen yet.

- Click a tab, or press `⌘1` to `⌘9`, to bring that session to the front.
- Drag a tab onto another to move it to its place (`session.tab.move`); `⌘1` to `⌘9` follow the order.
- Its `✕` ends the session: its agent stops and it's archived, asking first when something would be lost
  ([Ending a session](#end)).
- Right-click a tab for **Rename…** (edit the name in the tab; ⏎ keeps it, Escape drops it), **Close tab** and **End session…**.

## When Oriel starts {#launch}

Oriel opens as you left it: the tabs that were open, in their order, with the session that was in front in front
again, or the first tab when that one's gone. A session deleted since is left out. The start view is for when no tabs
are open: with tabs open, one is always in front.

{{keys "session.*"}}

## Ending a session {#end}

**End session…** asks how, in one dialog, and says first what would be lost, project by project: uncommitted files,
commits not pushed, an open pull request (it stays open on its host), an agent mid-turn, the subagents, background
tasks and processes it's still running (they stop with it), a running sandbox.

- **Archive**: the worktrees go; threads, transcript and test runs stay, searchable, and opening it again makes its
  worktrees again on their branches. It's the choice to start on once anything was pushed or raised.
- **Delete**: the worktrees, workspace and log go, with everything in them. The repos themselves stay. Its id isn't
  given to another session: one started later with the same name is `name-2`. A direct session's directory isn't
  Oriel's, so it can only be archived.
- With uncommitted work, Archive becomes **Discard and archive**.
- **Delete the local branches too** deletes each project's local branch; remote branches, a pull request's included, stay.

`⌘↵` ends it, `Esc` cancels. Archiving offers **Undo** in its notice while the branches are still there.

The same dialog opens from the Workspace page's Overview, and **Finish session** on the Ship
page once every pull request is merged.

Closing a session's tab (its `✕`, **Close tab**, `session.close`) archives it. When nothing would be lost it does so
without asking, and its notice offers **Undo**. When something would be (any of the list above but a sandbox), the
dialog opens first, on Archive. A session that looks finished opens the dialog too, offering Delete as well, with
**Delete the local branches too** ticked: a review whose last review you sent approved the pull request, or a
session whose pull requests are all merged with nothing uncommitted or unpushed in any of its projects. Cancel
leaves the session as it was, its tab open. A plugin says when a session is finished with `oriel.session.done`.
`session.end` with `how` (`archive` or `delete`) ends one without asking; `session.close-tab` closes only the tab.

{{options "session"}}
