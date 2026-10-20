---
id: shell/tabs
covers: [app.titlebar]
related: [shell/omnibox, errors]
---

# Session tabs

Each session has a tab across the top. Its dot is its status: red needs you, violet wants permission, cyan is
working, blue is waiting on CI, pink is with a reviewer, green is quiet, grey is off. A `✕` and a number mean errors
in that session you haven't seen yet.

- Click a tab, or press `⌘1` to `⌘9`, to bring that session to the front.
- Its `✕` closes the tab only: the session carries on, and the omnibox or mission control brings it back.
- Right-click a tab for **Close tab** and **End session…**.

{{keys "session.*"}}

## Ending a session {#end}

**End session…** asks how, in one dialog, and says first what would be lost, project by project: uncommitted files,
commits not pushed, an open pull request (it stays open on its host), an agent mid-turn, a running sandbox.

- **Archive**: the worktrees go; threads, transcript and test runs stay, searchable, and opening it again makes its
  worktrees again on their branches. It's the choice to start on once anything was pushed or raised.
- **Delete**: the worktrees, workspace and log go, with everything in them. The repos themselves stay. A direct
  session's directory isn't Oriel's, so it can only be archived.
- With uncommitted work, Archive becomes **Discard and archive**.
- **Delete the branches too** deletes each project's local branch; the remote's stays.

`⌘↵` ends it, `Esc` cancels. Archiving offers **Undo** in its notice while the branches are still there.

The same dialog opens from mission control (`e`), the Workspace page's Overview, and **Finish session** on the Ship
page once every pull request is merged. `session.end` with `how` (`archive` or `delete`) ends one without asking.

{{options "session"}}
