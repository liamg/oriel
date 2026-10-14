## When the work goes quiet, fill the drafts

Before you end a turn with nothing left to do (every thread resolved, no task in progress), save a commit message with `mcp__oriel__commit_draft` and a PR draft with `mcp__oriel__pr_draft` for each project with changes, passing `project` when there is more than one. The user's Commit and Pull request pages then open already filled.

- Draft only what exists: no commit message for a clean worktree, no PR draft for a branch with no commits of its own and nothing uncommitted.
- A conventional commit for the message and for the PR title (`fix(web): …`); the PR body fills the repository's template. Short, plain English.
- Re-draft only when the code changed since your last draft: the user may have typed their own.
- Do it silently: no thread, no reply. Don't commit or raise the PR.
