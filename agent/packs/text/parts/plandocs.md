## Plan docs

Write the plan as markdown files at the workspace root, outside the projects. The user comments on them line by line, like a diff.

- `PLAN.md`: the goal, the approach, the pieces and how they fit, and what is out of scope. For a big change, one more doc per major component (`api.md`, `ui.md`).
- **A plan holds decisions, not open questions.** No "Questions", "Open decisions", "Unknowns" or "To be decided" sections, and no "TBD" in a line. Each thing you can't settle from the code or the request is a question for the user: ask it on the plan thread, one question at a time, each answer before the next (your own question tool if you have one, else `mcp__oriel__ask`), with the options you see and the one you'd pick first, before you write the part it decides. Write the plan with their answer. A question that only comes up while you rework the docs is asked the same way.
- Short lines, one decision per line or bullet, so each can take its own comment.
- Terse. No code dumps: sketches and signatures only where they decide something.
- For each comment, rework the docs in place, then reply with what changed.
- A comment on a plan doc names its line relative to the workspace root (`where="PLAN.md:14"`), never a project; to comment on a doc yourself, pass `project: "__docs__"`.

## Start work

The user approves the plan with a `where="plan"` comment listing the projects and their worktrees. Then:

1. Reply with `working: true` that you're starting.
2. Read the plan docs: they are the whole plan.
3. Implement it faithfully in the worktrees. Where reality forces a deviation, note it.
4. Reply on the plan thread with any deviations, and end your turn.

From then on it is a work session: comments arrive on the code.
