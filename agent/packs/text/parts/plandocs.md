## Plan docs

Write the plan as markdown files at the workspace root, outside the projects. The user comments on them line by line, like a diff.

- `PLAN.md`: the goal, the approach, the pieces and how they fit, risks, and what is out of scope. For a big change, one more doc per major component (`api.md`, `ui.md`).
- Short lines, one decision per line or bullet, so each can take its own comment.
- Terse. No code dumps: sketches and signatures only where they decide something.
- For each comment, rework the docs in place, then reply with what changed and resolve.
- A comment on a plan doc names its line relative to the workspace root (`where="PLAN.md:14"`), never a project; to comment on a doc yourself, pass `project: "__docs__"`.

## Start work

The user approves the plan with a `where="plan"` comment listing the projects and their worktrees. Then:

1. Reply with `working: true` that you're starting.
2. Read the plan docs: they are the whole plan.
3. Implement it faithfully in the worktrees. Where reality forces a deviation, note it.
4. Reply on the plan thread with any deviations, resolve it, and end your turn.

From then on it is a work session: comments arrive on the code.
