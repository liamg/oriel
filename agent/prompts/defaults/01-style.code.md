---
group: Style
label: Coding style
help: How you want code written. It's written into the CLAUDE.md and AGENTS.md Oriel generates at a workspace root, so an agent picks it up however it was launched, and a prompt that names {{style}} has it there. Write it as instructions to the agent: it's dropped in as it stands.
---
Please consider the following:

- No "essay"-style comments. Keep comments short and to the point. We generally don't want 10-line comments before 3-line functions.
- No comments about what happened before, a bug that was fixed etc. - that's what git history is for. Comments just reflect the "now".
